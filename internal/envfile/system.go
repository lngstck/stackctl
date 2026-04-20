package envfile

import "github.com/lngstck/stackctl/internal/config"

// SystemEnvKeys lists the .env keys that stackctl derives from config and
// owns in the global section. They are (re)written on every call to
// ApplySystemEnv so the .env tracks the current Config without manual edits.
var SystemEnvKeys = []string{
	"SCHOOL_NAME",
	"SCHOOL_SLUG",
	"SERVER_DOMAIN",
	"DEX_AUTH_URL",
	"STACKCTL_ADMIN_PASSWORD",
}

// ApplySystemEnv overwrites the system-owned keys in the global section
// using the current Config. adminPassword, if non-empty, overwrites the
// STACKCTL_ADMIN_PASSWORD entry; pass "" to leave any existing value
// untouched (we never persist the plaintext in config.yaml, so callers
// that don't have the plaintext at hand should pass "").
func ApplySystemEnv(f *File, cfg *config.Config, adminPassword string) {
	if f == nil || cfg == nil {
		return
	}
	f.Set(GlobalSection, "SCHOOL_NAME", cfg.School.Name)
	f.Set(GlobalSection, "SCHOOL_SLUG", cfg.School.Slug)

	domain := cfg.School.ServerDomain
	if domain == "" {
		domain = "localhost"
	}
	f.Set(GlobalSection, "SERVER_DOMAIN", domain)

	authURL := cfg.Dex.AuthURL
	if authURL == "" && cfg.School.Slug != "" {
		authURL = "https://auth." + cfg.School.Slug + ".learningstack.online"
	}
	f.Set(GlobalSection, "DEX_AUTH_URL", authURL)

	if adminPassword != "" {
		f.Set(GlobalSection, "STACKCTL_ADMIN_PASSWORD", adminPassword)
	}
}
