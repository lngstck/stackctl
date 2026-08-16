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
			Transport:  config.TransportRelay,
			BaseDomain: "phoenix.learningstack.online",
		},
	}

	ApplySystemEnv(f, cfg, "pw123456")

	for k, want := range map[string]string{
		"SCHOOL_NAME":        "Phoenix",
		"SCHOOL_SLUG":        "phoenix",
		"SERVER_DOMAIN":      "192.168.1.10",
		"PUBLIC_BASE_DOMAIN": "phoenix.learningstack.online",
		"DEX_AUTH_URL":       "https://auth.phoenix.learningstack.online",
		"ADMIN_PASSWORD":     "pw123456",
	} {
		if v, ok := f.Get(k); !ok || v != want {
			t.Errorf("%s = %q,%v; want %q", k, v, ok, want)
		}
	}
}

// TestApplySystemEnvAuthURLFollowsBaseDomain pins DEX_AUTH_URL to the school's
// own domain rather than to its slug. The URL used to be read from a copy
// stored at setup time, which agreed with the address only as long as every
// school lived under the operator's root domain.
func TestApplySystemEnvAuthURLFollowsBaseDomain(t *testing.T) {
	for _, tc := range []struct {
		name      string
		transport string
		want      string
	}{
		{"relay", config.TransportRelay, "https://auth.ls.gym-phoenix.de"},
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
	if v, _ := f.Get("DEX_AUTH_URL"); v != "https://auth.s.learningstack.online" {
		t.Errorf("DEX_AUTH_URL = %q", v)
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
