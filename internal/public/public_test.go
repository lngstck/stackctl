package public

import (
	"testing"

	"github.com/lngstck/stackctl/internal/config"
)

func relayCfg() *config.Config {
	return &config.Config{
		School: config.School{Name: "Gymnasium Phoenix", Slug: "phoenix"},
		Public: config.Public{
			Transport:  config.TransportRelay,
			BaseDomain: "phoenix.learningstack.online",
		},
	}
}

func TestHostAndURL(t *testing.T) {
	cfg := relayCfg()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"auth host", AuthHost(cfg), "auth.phoenix.learningstack.online"},
		{"auth url", AuthURL(cfg), "https://auth.phoenix.learningstack.online"},
		{"app host", AppHost(cfg, "pylearn"), "pylearn.phoenix.learningstack.online"},
		{"app url", AppURL(cfg, "pylearn"), "https://pylearn.phoenix.learningstack.online"},
		{"empty sub is the base itself", Host(cfg, ""), "phoenix.learningstack.online"},
	}
	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

// The whole point of the package: a school on its own domain gets its
// hostnames under that domain, with no trace of the operator's root.
func TestCustomBaseDomain(t *testing.T) {
	cfg := relayCfg()
	cfg.Public.BaseDomain = "ls.gym-phoenix.de"

	if got, want := AuthURL(cfg), "https://auth.ls.gym-phoenix.de"; got != want {
		t.Errorf("AuthURL = %q, want %q", got, want)
	}
	if got, want := AppHost(cfg, "sponsorenlauf"), "sponsorenlauf.ls.gym-phoenix.de"; got != want {
		t.Errorf("AppHost = %q, want %q", got, want)
	}
}

// Transport does not influence the address — a relay tunnel and a local
// proxy publish the very same hostnames.
func TestTransportDoesNotChangeAddresses(t *testing.T) {
	relay := relayCfg()
	direct := relayCfg()
	direct.Public.Transport = config.TransportDirect

	if AuthURL(relay) != AuthURL(direct) {
		t.Errorf("auth URL differs by transport: %q vs %q", AuthURL(relay), AuthURL(direct))
	}
	if AppHost(relay, "pylearn") != AppHost(direct, "pylearn") {
		t.Error("app host differs by transport")
	}
}

// A config whose base_domain has not been written yet — mid-setup, or a v2
// file upgraded in memory but not saved — must still produce the address the
// pre-v3 build would have produced.
func TestBaseDomainFallsBackToSlug(t *testing.T) {
	cfg := &config.Config{School: config.School{Slug: "phoenix"}}

	if got, want := BaseDomain(cfg), "phoenix.learningstack.online"; got != want {
		t.Errorf("BaseDomain = %q, want %q", got, want)
	}
	if got, want := AuthURL(cfg), "https://auth.phoenix.learningstack.online"; got != want {
		t.Errorf("AuthURL = %q, want %q", got, want)
	}
}

// With neither a base domain nor a slug there is no honest answer, and a
// caller must not end up building "auth." or "https://auth." out of it.
func TestNoAddressYieldsEmptyString(t *testing.T) {
	cfg := &config.Config{}

	if got := BaseDomain(cfg); got != "" {
		t.Errorf("BaseDomain = %q, want empty", got)
	}
	if got := Host(cfg, "auth"); got != "" {
		t.Errorf("Host = %q, want empty", got)
	}
	if got := URL(cfg, "auth"); got != "" {
		t.Errorf("URL = %q, want empty", got)
	}
	if got := AuthURL(nil); got != "" {
		t.Errorf("AuthURL(nil) = %q, want empty", got)
	}
}

// The admin label is reserved: an app called "admin" would otherwise answer on
// the address of the control plane, and only once both are published.
func TestAdminAddress(t *testing.T) {
	cfg := &config.Config{
		School: config.School{Slug: "phoenix"},
		Public: config.Public{
			Transport:  config.TransportDirect,
			BaseDomain: "ls.gym-phoenix.de",
		},
	}

	if got, want := AdminHost(cfg), "admin.ls.gym-phoenix.de"; got != want {
		t.Errorf("AdminHost = %q, want %q", got, want)
	}
	if got, want := AdminURL(cfg), "https://admin.ls.gym-phoenix.de"; got != want {
		t.Errorf("AdminURL = %q, want %q", got, want)
	}
	if AdminHost(cfg) == AppHost(cfg, AuthSubdomain) {
		t.Error("admin and auth must not share a hostname")
	}
	if got := AdminHost(nil); got != "" {
		t.Errorf("AdminHost(nil) = %q, want empty", got)
	}
}
