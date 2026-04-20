package envfile

import (
	"testing"

	"github.com/lngstck/stackctl/internal/config"
)

func TestApplySystemEnv(t *testing.T) {
	f := New()
	cfg := &config.Config{
		School: config.School{
			Name:         "Phoenix",
			Slug:         "phoenix",
			ServerDomain: "192.168.1.10",
		},
		Dex: config.Dex{
			AuthURL: "https://auth.phoenix.learningstack.online",
		},
	}

	ApplySystemEnv(f, cfg, "pw123456")

	for k, want := range map[string]string{
		"SCHOOL_NAME":             "Phoenix",
		"SCHOOL_SLUG":             "phoenix",
		"SERVER_DOMAIN":           "192.168.1.10",
		"DEX_AUTH_URL":            "https://auth.phoenix.learningstack.online",
		"STACKCTL_ADMIN_PASSWORD": "pw123456",
	} {
		if v, ok := f.Get(k); !ok || v != want {
			t.Errorf("%s = %q,%v; want %q", k, v, ok, want)
		}
	}
}

func TestApplySystemEnvPreservesExistingPassword(t *testing.T) {
	f := New()
	f.Set(GlobalSection, "STACKCTL_ADMIN_PASSWORD", "old")
	cfg := &config.Config{School: config.School{Name: "S", Slug: "s"}}

	ApplySystemEnv(f, cfg, "") // empty password → preserve

	if v, _ := f.Get("STACKCTL_ADMIN_PASSWORD"); v != "old" {
		t.Errorf("STACKCTL_ADMIN_PASSWORD = %q; want %q (preserved)", v, "old")
	}
}

func TestApplySystemEnvDefaultDomain(t *testing.T) {
	f := New()
	cfg := &config.Config{School: config.School{Name: "S", Slug: "s"}}
	ApplySystemEnv(f, cfg, "")
	if v, _ := f.Get("SERVER_DOMAIN"); v != "localhost" {
		t.Errorf("SERVER_DOMAIN = %q; want %q", v, "localhost")
	}
	if v, _ := f.Get("DEX_AUTH_URL"); v != "https://auth.s.learningstack.online" {
		t.Errorf("DEX_AUTH_URL = %q", v)
	}
}
