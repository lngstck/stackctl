package publish

import (
	"testing"

	"github.com/lngstck/stackctl/internal/config"
)

// The concrete publishers must satisfy the interface — the compiler is the
// test here, and it is the assertion that matters for the callers.
var (
	_ Publisher          = (*Relay)(nil)
	_ ConnectivityTester = (*Relay)(nil)
	_ RelayIdentity      = (*Relay)(nil)
	_ Publisher          = unsupported{}
)

func TestAppsFromOnlyIncludesPublicApps(t *testing.T) {
	st := config.NewState()
	st.Containers["pylearn"] = &config.ContainerState{
		ID: "pylearn", Ports: []int{8330}, PublicEnabled: true,
	}
	st.Containers["sponsorenlauf"] = &config.ContainerState{
		ID: "sponsorenlauf", Ports: []int{8340}, PublicEnabled: true,
	}
	st.Containers["postgres"] = &config.ContainerState{
		ID: "postgres", Ports: []int{5432}, PublicEnabled: false,
	}
	// Publicly flagged but portless — nothing to forward to.
	st.Containers["broken"] = &config.ContainerState{ID: "broken", PublicEnabled: true}

	apps := AppsFrom(st, func(id string) int { return 8000 })

	if len(apps) != 2 {
		t.Fatalf("got %d apps, want 2: %+v", len(apps), apps)
	}
	// Sorted, so the order is asserted rather than incidental.
	if apps[0].ID != "pylearn" || apps[1].ID != "sponsorenlauf" {
		t.Errorf("unexpected apps/order: %+v", apps)
	}
	if apps[0].LocalPort != 8330 {
		t.Errorf("LocalPort = %d, want 8330", apps[0].LocalPort)
	}
	if apps[0].ContainerPort != 8000 {
		t.Errorf("ContainerPort = %d, want 8000", apps[0].ContainerPort)
	}
}

func TestAppsFromToleratesMissingContainerPort(t *testing.T) {
	st := config.NewState()
	st.Containers["pylearn"] = &config.ContainerState{
		ID: "pylearn", Ports: []int{8330}, PublicEnabled: true,
	}

	apps := AppsFrom(st, nil)

	if len(apps) != 1 || apps[0].ContainerPort != 0 {
		t.Fatalf("an app with no known container port must still be published: %+v", apps)
	}
	if AppsFrom(nil, nil) != nil {
		t.Error("AppsFrom(nil) should be nil")
	}
}

// A transport this build cannot serve — a config from a newer stackctl, or a
// hand-edited value — must fail loudly per operation while leaving stackctl
// itself running. The admin fixes the setting in the very web UI that would
// otherwise be gone.
func TestUnsupportedTransportFailsPerOperation(t *testing.T) {
	cfg := &config.Config{
		School: config.School{Slug: "phoenix"},
		Public: config.Public{Transport: "carrier-pigeon", BaseDomain: "ls.gym-phoenix.de"},
	}

	p := For(cfg)
	if p == nil {
		t.Fatal("For must never return nil")
	}
	if err := p.EnsureAuth(); err == nil {
		t.Error("EnsureAuth should report the unsupported transport")
	}
	if _, err := p.Enable(App{ID: "pylearn", LocalPort: 8330}); err == nil {
		t.Error("Enable should report the unsupported transport")
	}
	if p.Status("pylearn") != StatusError {
		t.Error("an app that cannot be published must not look healthy")
	}
	// Withdrawing something that was never published stays a no-op.
	if err := p.Disable("pylearn"); err != nil {
		t.Errorf("Disable = %v, want nil", err)
	}
}

func TestForReturnsRelayByDefault(t *testing.T) {
	cfg := &config.Config{
		School: config.School{Slug: "phoenix"},
		Public: config.Public{Transport: config.TransportRelay, BaseDomain: "phoenix.learningstack.online"},
	}

	p := For(cfg)
	if p.Kind() != KindRelay {
		t.Errorf("Kind = %q, want %q", p.Kind(), KindRelay)
	}
	if _, ok := p.(ConnectivityTester); !ok {
		t.Error("a relay must offer a transport test")
	}

	direct := For(&config.Config{
		School: config.School{Slug: "phoenix"},
		Public: config.Public{Transport: config.TransportDirect, BaseDomain: "ls.gym-phoenix.de"},
	})
	if direct.Kind() != KindDirect {
		t.Errorf("Kind = %q, want %q", direct.Kind(), KindDirect)
	}
	// A server that publishes itself dials no endpoint, so it has neither a
	// relay identity to show nor a transport handshake to test.
	if _, ok := direct.(RelayIdentity); ok {
		t.Error("direct transport must not advertise a relay identity")
	}

	// An install whose transport was never written down is a relay too —
	// that is the historical default, not an error.
	empty := For(&config.Config{School: config.School{Slug: "phoenix"}})
	if empty.Kind() != KindRelay {
		t.Errorf("empty transport → %q, want %q", empty.Kind(), KindRelay)
	}
}
