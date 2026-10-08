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
		Public: config.Public{
			Transport:  config.TransportDirect,
			BaseDomain: "ls.gym-phoenix.de",
		},
	}

	ApplySystemEnv(f, cfg, "pw123456")

	for k, want := range map[string]string{
		"SCHOOL_NAME":        "Phoenix",
		"SCHOOL_SLUG":        "phoenix",
		"SERVER_DOMAIN":      "192.168.1.10",
		"PUBLIC_BASE_DOMAIN": "ls.gym-phoenix.de",
		"DEX_AUTH_URL":       "https://auth.ls.gym-phoenix.de",
		"ADMIN_PASSWORD":     "pw123456",
	} {
		if v, ok := f.Get(k); !ok || v != want {
			t.Errorf("%s = %q,%v; want %q", k, v, ok, want)
		}
	}
}

// TestApplySystemEnvAuthURLFollowsBaseDomain pins DEX_AUTH_URL to the school's
// own domain rather than to its slug.
func TestApplySystemEnvAuthURLFollowsBaseDomain(t *testing.T) {
	for _, tc := range []struct {
		name      string
		transport string
		want      string
	}{
		{"direct", config.TransportDirect, "https://auth.ls.gym-phoenix.de"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := New()
			cfg := &config.Config{
				School: config.School{Name: "Phoenix", Slug: "phoenix"},
				Public: config.Public{
					Transport:  tc.transport,
					BaseDomain: "ls.gym-phoenix.de",
				},
			}

			ApplySystemEnv(f, cfg, "")

			if v, _ := f.Get("DEX_AUTH_URL"); v != tc.want {
				t.Errorf("DEX_AUTH_URL = %q; want %q", v, tc.want)
			}
		})
	}
}

func TestApplySystemEnvPreservesExistingPassword(t *testing.T) {
	f := New()
	f.Set(GlobalSection, "ADMIN_PASSWORD", "old")
	cfg := &config.Config{School: config.School{Name: "S", Slug: "s"}}

	ApplySystemEnv(f, cfg, "") // empty password → preserve

	if v, _ := f.Get("ADMIN_PASSWORD"); v != "old" {
		t.Errorf("ADMIN_PASSWORD = %q; want %q (preserved)", v, "old")
	}
}

func TestApplySystemEnvDefaultDomain(t *testing.T) {
	f := New()
	cfg := &config.Config{School: config.School{Name: "S", Slug: "s"}}
	ApplySystemEnv(f, cfg, "")
	if v, _ := f.Get("SERVER_DOMAIN"); v != "localhost" {
		t.Errorf("SERVER_DOMAIN = %q; want %q", v, "localhost")
	}
	// No domain chosen yet, so no issuer either — rather than one invented
	// from the slug.
	if v, _ := f.Get("DEX_AUTH_URL"); v != "" {
		t.Errorf("DEX_AUTH_URL = %q, want empty", v)
	}
}

// A school on its own domain must see that domain in the .env, because the
// catalog builds app URLs from it (see APP-GUIDE: PUBLIC_BASE_DOMAIN).
func TestApplySystemEnvCustomBaseDomain(t *testing.T) {
	f := New()
	cfg := &config.Config{
		School: config.School{Name: "Phoenix", Slug: "phoenix"},
		Public: config.Public{
			Transport:  config.TransportDirect,
			BaseDomain: "ls.gym-phoenix.de",
		},
	}

	ApplySystemEnv(f, cfg, "")

	if v, _ := f.Get("PUBLIC_BASE_DOMAIN"); v != "ls.gym-phoenix.de" {
		t.Errorf("PUBLIC_BASE_DOMAIN = %q", v)
	}
	// The slug stays what it is — it identifies the school, it is not an
	// address, and things like the Open WebUI admin login are keyed on it.
	if v, _ := f.Get("SCHOOL_SLUG"); v != "phoenix" {
		t.Errorf("SCHOOL_SLUG = %q, want it unchanged", v)
	}
	if v, _ := f.Get("DEX_AUTH_URL"); v != "https://auth.ls.gym-phoenix.de" {
		t.Errorf("DEX_AUTH_URL = %q", v)
	}
}

// Der deSEC-Token gehoert in die .env, die der Proxy liest — und nur im
// Schulnetz-Betrieb. Sonst bleibt der Schluessel leer statt alt.
func TestApplySystemEnvDNSToken(t *testing.T) {
	cfg := &config.Config{
		School: config.School{Name: "Phoenix", Slug: "phoenix"},
		Public: config.Public{
			Transport:  config.TransportLocal,
			BaseDomain: "ls.gym-phoenix.de",
			Local:      config.PublicLocal{DNSToken: "geheim"},
		},
	}
	f := New()
	ApplySystemEnv(f, cfg, "")
	if v, _ := f.Get("DESEC_TOKEN"); v != "geheim" {
		t.Errorf("DESEC_TOKEN = %q, want geheim", v)
	}

	cfg.Public.Transport = config.TransportDirect
	ApplySystemEnv(f, cfg, "")
	if v, ok := f.Get("DESEC_TOKEN"); !ok || v != "" {
		t.Errorf("direkter Betrieb: DESEC_TOKEN = %q (%v), want leer", v, ok)
	}
}
