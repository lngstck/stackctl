package publish

import (
	"os"
	"strings"
	"testing"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/paths"
)

func directCfg(t *testing.T) *config.Config {
	t.Helper()
	t.Setenv(paths.EnvLearningstackDir, t.TempDir())
	return &config.Config{
		School: config.School{Slug: "phoenix"},
		Public: config.Public{
			Transport:  config.TransportDirect,
			BaseDomain: "ls.gym-phoenix.de",
		},
	}
}

func caddyfile(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(paths.CaddyConfigFile())
	if err != nil {
		t.Fatalf("read Caddyfile: %v", err)
	}
	return string(data)
}

func TestDirectPublishesAppAndAuth(t *testing.T) {
	d := NewDirect(directCfg(t))

	if err := d.EnsureAuth(); err != nil {
		t.Fatalf("EnsureAuth: %v", err)
	}
	host, err := d.Enable(App{ID: "pylearn", LocalPort: 8330, ContainerPort: 8000})
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if want := "pylearn.ls.gym-phoenix.de"; host != want {
		t.Errorf("host = %q, want %q", host, want)
	}

	got := caddyfile(t)
	for _, want := range []string{
		"auth.ls.gym-phoenix.de {",
		"reverse_proxy ls-dex:5556",
		"pylearn.ls.gym-phoenix.de {",
		"reverse_proxy ls-pylearn:8000",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Caddyfile missing %q:\n%s", want, got)
		}
	}
}

// Disabling one app must leave the others alone. The whole file is rewritten
// every time, so this is the property that guards against a regeneration bug
// taking unrelated sites offline.
func TestDirectDisableKeepsOtherRoutes(t *testing.T) {
	d := NewDirect(directCfg(t))
	mustEnable(t, d, "pylearn", 8000)
	mustEnable(t, d, "sponsorenlauf", 8000)
	if err := d.EnsureAuth(); err != nil {
		t.Fatalf("EnsureAuth: %v", err)
	}

	if err := d.Disable("pylearn"); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	got := caddyfile(t)
	if strings.Contains(got, "pylearn.ls.gym-phoenix.de") {
		t.Errorf("withdrawn app still routed:\n%s", got)
	}
	if !strings.Contains(got, "sponsorenlauf.ls.gym-phoenix.de") {
		t.Errorf("unrelated app lost its route:\n%s", got)
	}
	if !strings.Contains(got, "auth.ls.gym-phoenix.de") {
		t.Errorf("login lost its route:\n%s", got)
	}

	// Withdrawing something that was never published stays a no-op.
	if err := d.Disable("handheld"); err != nil {
		t.Errorf("Disable unknown app = %v, want nil", err)
	}
}

// Without the in-container port there is nothing to proxy to. Guessing would
// produce a route that silently 502s, which is worse than a refusal.
func TestDirectRefusesAppWithoutContainerPort(t *testing.T) {
	d := NewDirect(directCfg(t))

	if _, err := d.Enable(App{ID: "pylearn", LocalPort: 8330}); err == nil {
		t.Fatal("Enable without a container port should fail")
	}
	if d.Status("pylearn") != StatusStopped {
		t.Error("a refused app must not be tracked as published")
	}
	if _, err := os.Stat(paths.CaddyConfigFile()); err == nil {
		t.Error("a refused publish should not have written a Caddyfile")
	}
}

// An install without an address cannot publish anything — the setup wizard
// has not written one yet.
func TestDirectRefusesWithoutBaseDomain(t *testing.T) {
	t.Setenv(paths.EnvLearningstackDir, t.TempDir())
	d := NewDirect(&config.Config{Public: config.Public{Transport: config.TransportDirect}})

	if err := d.EnsureAuth(); err == nil {
		t.Error("EnsureAuth without an address should fail")
	}
	if _, err := d.Enable(App{ID: "pylearn", ContainerPort: 8000}); err == nil {
		t.Error("Enable without an address should fail")
	}
}

// Restore is the startup path: whatever state.yaml said was public comes back
// in one Caddyfile.
func TestDirectRestore(t *testing.T) {
	d := NewDirect(directCfg(t))

	d.Restore([]App{
		{ID: "pylearn", LocalPort: 8330, ContainerPort: 8000},
		{ID: "sponsorenlauf", LocalPort: 8340, ContainerPort: 8000},
		{ID: "broken", LocalPort: 8350}, // no container port — skipped, logged
	})

	got := caddyfile(t)
	if !strings.Contains(got, "pylearn.ls.gym-phoenix.de") ||
		!strings.Contains(got, "sponsorenlauf.ls.gym-phoenix.de") {
		t.Errorf("restore lost routes:\n%s", got)
	}
	if strings.Contains(got, "broken.ls.gym-phoenix.de") {
		t.Errorf("unroutable app was published anyway:\n%s", got)
	}
	// One app it could not publish must not stop the others.
	if d.Status("pylearn") == StatusStopped {
		t.Error("pylearn should be tracked as published")
	}
}

// The proxy is what makes a route real. A published app with no proxy running
// is an error, not "stopped" — the admin asked for it and it is not there.
func TestDirectStatusReflectsProxy(t *testing.T) {
	d := NewDirect(directCfg(t))

	if got := d.Status("pylearn"); got != StatusStopped {
		t.Errorf("unpublished app status = %q, want %q", got, StatusStopped)
	}
	mustEnable(t, d, "pylearn", 8000)
	// No docker in the test environment, so the proxy is not running.
	if got := d.Status("pylearn"); got != StatusError {
		t.Errorf("published app without a running proxy = %q, want %q", got, StatusError)
	}
}

func mustEnable(t *testing.T, d *Direct, id string, containerPort int) {
	t.Helper()
	if _, err := d.Enable(App{ID: id, LocalPort: 8000, ContainerPort: containerPort}); err != nil {
		t.Fatalf("Enable %s: %v", id, err)
	}
}

// The UI is the one upstream that is not a container: stackctl runs under
// systemd, so the proxy has to reach it through the host, not by service name.
func TestDirectPublishesAdminUIViaHostAddress(t *testing.T) {
	d := NewDirect(directCfg(t))
	d.hostAddress = func() (string, error) { return "172.18.0.1", nil }

	if err := d.StartAdmin(8090); err != nil {
		t.Fatalf("StartAdmin: %v", err)
	}

	conf := caddyfile(t)
	if !strings.Contains(conf, "admin.ls.gym-phoenix.de") {
		t.Errorf("Caddyfile has no admin route:\n%s", conf)
	}
	if !strings.Contains(conf, "172.18.0.1:8090") {
		t.Errorf("admin route does not point at the host:\n%s", conf)
	}
	if got := d.AdminStatus(); got == StatusStopped {
		t.Error("AdminStatus still reports stopped after publishing")
	}
}

func TestDirectStopAdminRemovesRoute(t *testing.T) {
	d := NewDirect(directCfg(t))
	d.hostAddress = func() (string, error) { return "172.18.0.1", nil }

	if err := d.StartAdmin(8090); err != nil {
		t.Fatalf("StartAdmin: %v", err)
	}
	if err := d.StopAdmin(); err != nil {
		t.Fatalf("StopAdmin: %v", err)
	}

	if conf := caddyfile(t); strings.Contains(conf, "admin.ls.gym-phoenix.de") {
		t.Errorf("admin route survived StopAdmin:\n%s", conf)
	}
	if got := d.AdminStatus(); got != StatusStopped {
		t.Errorf("AdminStatus = %q, want %q", got, StatusStopped)
	}
}

// Publishing the UI must not disturb what is already published. The whole
// Caddyfile is rewritten on every change, so this is worth pinning.
func TestDirectAdminRouteLeavesOthersAlone(t *testing.T) {
	d := NewDirect(directCfg(t))
	d.hostAddress = func() (string, error) { return "172.18.0.1", nil }

	if err := d.EnsureAuth(); err != nil {
		t.Fatalf("EnsureAuth: %v", err)
	}
	if _, err := d.Enable(App{ID: "pylearn", LocalPort: 8330, ContainerPort: 8000}); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if err := d.StartAdmin(8090); err != nil {
		t.Fatalf("StartAdmin: %v", err)
	}

	conf := caddyfile(t)
	for _, want := range []string{
		"auth.ls.gym-phoenix.de",
		"pylearn.ls.gym-phoenix.de",
		"admin.ls.gym-phoenix.de",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("route %q missing:\n%s", want, conf)
		}
	}
}

// Without a reachable host address there is no route worth writing. Failing
// here beats a route that resolves and answers nothing.
func TestDirectAdminFailsWithoutHostAddress(t *testing.T) {
	d := NewDirect(directCfg(t))
	d.hostAddress = func() (string, error) { return "", errNoGateway }

	if err := d.StartAdmin(8090); err == nil {
		t.Fatal("StartAdmin succeeded without a host address")
	}
	if got := d.AdminStatus(); got != StatusStopped {
		t.Errorf("AdminStatus = %q after a failed publish, want %q", got, StatusStopped)
	}
}

func TestDirectAdminRejectsUnknownPort(t *testing.T) {
	d := NewDirect(directCfg(t))
	d.hostAddress = func() (string, error) { return "172.18.0.1", nil }

	if err := d.StartAdmin(0); err == nil {
		t.Error("StartAdmin accepted port 0")
	}
}

type gatewayErr string

func (e gatewayErr) Error() string { return string(e) }

const errNoGateway = gatewayErr("no gateway")
