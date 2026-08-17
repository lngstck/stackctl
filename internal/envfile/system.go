package envfile

import (
	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/public"
)

// SystemEnvKeys lists the .env keys that stackctl derives from config and
// owns in the global section. They are (re)written on every call to
// ApplySystemEnv so the .env tracks the current Config without manual edits.
var SystemEnvKeys = []string{
	"SCHOOL_NAME",
	"SCHOOL_SLUG",
	"SERVER_DOMAIN",
	"PUBLIC_BASE_DOMAIN",
	"DEX_AUTH_URL",
	"ADMIN_PASSWORD",
}

// ApplySystemEnv overwrites the system-owned keys in the global section
// using the current Config. adminPassword, if non-empty, overwrites the
// ADMIN_PASSWORD entry; pass "" to leave any existing value untouched
// (we never persist the plaintext in config.yaml, so callers that don't
// have the plaintext at hand should pass "").
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

	// PUBLIC_BASE_DOMAIN lets a container definition build its own public
	// URLs without knowing the operator's root domain, which is what the
	// pre-v3 catalog entries hardcoded.
	f.Set(GlobalSection, "PUBLIC_BASE_DOMAIN", public.BaseDomain(cfg))

	// Same source as the issuer in the generated Dex config and as the
	// redirect URIs registered with it — an app that trusts DEX_AUTH_URL and
	// a Dex that mints tokens under a different issuer would fail validation
	// on every login.
	f.Set(GlobalSection, "DEX_AUTH_URL", public.AuthURL(cfg))

	if adminPassword != "" {
		f.Set(GlobalSection, "ADMIN_PASSWORD", adminPassword)
	}
}
