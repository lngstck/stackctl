package public

import (
	"testing"

	"github.com/lngstck/stackctl/internal/config"
)

func schoolCfg() *config.Config {
	return &config.Config{
		School: config.School{Name: "Gymnasium Phoenix", Slug: "phoenix"},
		Public: config.Public{
			Transport:  config.TransportDirect,
			BaseDomain: "ls.gym-phoenix.de",
		},
	}
}

func TestHostAndURL(t *testing.T) {
	cfg := schoolCfg()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"auth host", AuthHost(cfg), "auth.ls.gym-phoenix.de"},
		{"auth url", AuthURL(cfg), "https://auth.ls.gym-phoenix.de"},
		{"app host", AppHost(cfg, "pylearn"), "pylearn.ls.gym-phoenix.de"},
		{"app url", AppURL(cfg, "pylearn"), "https://pylearn.ls.gym-phoenix.de"},
		{"empty sub is the base itself", Host(cfg, ""), "ls.gym-phoenix.de"},
	}
	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

// The slug is the school's short name, not part of its address. Before setup
// has chosen a domain there is no honest answer, and a caller must not end up
// building "auth." or "https://auth." out of it.
func TestNoAddressYieldsEmptyString(t *testing.T) {
	cfg := &config.Config{School: config.School{Slug: "phoenix"}}

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
	cfg := schoolCfg()

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
