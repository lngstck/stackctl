// Package config loads and saves the two authoritative YAML files for
// stackctl: config.yaml (school settings, admin hash, address model) and
// state.yaml (installed containers, port allocations, publication flags).
//
// Both files are expected under $STACKCTL_DIR/config/ and are written with
// mode 0640, owner learningstack:learningstack on a production install.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lngstck/stackctl/internal/paths"
)

// ConfigVersion is the schema version of config.yaml emitted by this build.
//
// v4 dropped everything that tied an install to an operator: the relay
// transport, the registration package and the client at a central Dex. A
// school runs on its own, and its address lives in the public: block alone.
// There is no upgrade path from earlier versions — no install predates v4.
const ConfigVersion = 4

// Transport kinds for Public.Transport. In both, a local reverse proxy holds
// 80/443, terminates TLS and routes by hostname; they differ in who can reach
// it and therefore in how the certificate is obtained.
const (
	// TransportLocal serves the school network only, the default. The
	// hostnames resolve to the server's private address, so no certificate
	// authority can ever reach it: one wildcard certificate comes over
	// DNS-01 instead (see PublicLocal). Access from outside is the school's
	// own business — a VPN or tunnel of its choosing — and nothing here
	// stands in its way.
	TransportLocal = "local"
	// TransportDirect serves the internet from the server itself, which
	// holds public 80/443 and gets one certificate per hostname over HTTP-01.
	TransportDirect = "direct"
)

// FilePerm is the on-disk permission for config.yaml and state.yaml.
// 0640 = owner rw, group r, others none (see ARCHITECTURE.md §16).
const FilePerm = 0o640

// SetupState is either "not set up yet" or "ready". There is no state in
// between: nothing outside the school has to approve an install.
type SetupState string

const (
	SetupStateNeedsSetup SetupState = "needs_setup"
	SetupStateReady      SetupState = "ready"
)

// Config mirrors config.yaml. Field tags use snake_case to match the format
// shown in ARCHITECTURE.md §12.
type Config struct {
	Version    int        `yaml:"version"`
	SetupState SetupState `yaml:"setup_state"`
	School     School     `yaml:"school"`
	Catalog    Catalog    `yaml:"catalog"`
	Admin      Admin      `yaml:"admin"`
	Public     Public     `yaml:"public"`
	// Auth beschreibt, womit sich Lehrkraefte und Schueler:innen in den Apps
	// anmelden.
	Auth Auth `yaml:"auth,omitempty"`
	// AutoUpdate steuert das naechtliche Auto-Update aller Apps.
	AutoUpdate AutoUpdate `yaml:"auto_update,omitempty"`
}

// Auth holds the sign-in for teachers and students, as opposed to Admin,
// which is stackctl's own login.
type Auth struct {
	// TestAccounts live in Dex's own password database. They stand in for the
	// school's sign-in service until one is connected — for trying things
	// out, not for teaching.
	TestAccounts []TestAccount `yaml:"test_accounts,omitempty"`
}

// TestAccount is one person in Dex's password database.
type TestAccount struct {
	// ID becomes the person's stable subject. It is random, not derived from
	// the login, so a deleted account's data is never inherited by the next
	// person given the same login.
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
	// Role is one of the claims contract's roles (internal/claims).
	Role string `yaml:"role"`
	// Login is what the person types into Dex's login form. Dex asks for an
	// e-mail address and puts this value into the email claim, so it has
	// that shape: {name}@{base_domain}.
	Login        string `yaml:"login"`
	PasswordHash string `yaml:"password_hash"`
}

// TestAccountByID returns the account with the given ID, or nil.
func (a *Auth) TestAccountByID(id string) *TestAccount {
	for i := range a.TestAccounts {
		if a.TestAccounts[i].ID == id {
			return &a.TestAccounts[i]
		}
	}
	return nil
}

// TestAccountByLogin returns the account with the given login, or nil. Logins
// compare case-insensitively, as Dex does.
func (a *Auth) TestAccountByLogin(login string) *TestAccount {
	for i := range a.TestAccounts {
		if strings.EqualFold(a.TestAccounts[i].Login, login) {
			return &a.TestAccounts[i]
		}
	}
	return nil
}

// RemoveTestAccount deletes the account with the given ID and reports whether
// there was one.
func (a *Auth) RemoveTestAccount(id string) bool {
	for i := range a.TestAccounts {
		if a.TestAccounts[i].ID == id {
			a.TestAccounts = append(a.TestAccounts[:i], a.TestAccounts[i+1:]...)
			return true
		}
	}
	return false
}

// AutoUpdate konfiguriert das naechtliche Auto-Update.
// Default ist deaktiviert; der Admin schaltet es in den Einstellungen ein.
// Auch im aktivierten Zustand werden Apps mit Breaking-Flag oder
// AutoUpdateDisabled uebersprungen.
type AutoUpdate struct {
	Enabled bool `yaml:"enabled,omitempty"`
}

// School holds user-entered identity fields for a school install.
type School struct {
	Name         string `yaml:"name"`
	Slug         string `yaml:"slug"`
	ServerDomain string `yaml:"server_domain"`
	ContactEmail string `yaml:"contact_email,omitempty"`
}

// Catalog points stackctl at its source of container definitions.
type Catalog struct {
	URL string `yaml:"url"`
}

// Admin stores the single-admin credentials. Only the hash is persisted.
type Admin struct {
	PasswordHash string `yaml:"password_hash"`
}

// Public describes the addresses this install answers on. It is the
// authoritative source for every hostname stackctl builds — see
// internal/public for the constructors that read it.
//
// The OIDC issuer of the local Dex is deliberately not stored anywhere: it
// follows from BaseDomain and is read through public.AuthURL. It has to match
// character for character between browser, containers and every redirect URI,
// and one source cannot disagree with itself.
type Public struct {
	// Transport is how traffic arrives: TransportLocal or TransportDirect.
	Transport string `yaml:"transport"`
	// BaseDomain is the parent of every hostname, chosen by the school at
	// setup. Apps answer at {app_id}.{base_domain}, the local Dex at
	// auth.{base_domain}, e.g. "ls.gym-phoenix.de".
	BaseDomain string `yaml:"base_domain"`
	// ACMEEmail is the contact address Let's Encrypt uses for expiry
	// warnings. Optional — certificates are issued without one, but then
	// nobody gets told when renewal has been failing.
	ACMEEmail string `yaml:"acme_email,omitempty"`
	// ACMECA overrides the ACME directory URL. Its purpose is the Let's
	// Encrypt staging endpoint: real certificates are rate-limited to five
	// duplicates per week, which a few rounds of debugging burn through.
	ACMECA string `yaml:"acme_ca,omitempty"`
	// Local configures the certificate for TransportLocal.
	Local PublicLocal `yaml:"local,omitempty"`
}

// PublicLocal holds what DNS-01 needs. The school's DNS provider rarely has
// an API, so the challenge is delegated: _acme-challenge.{base_domain} is a
// CNAME into a zone at deSEC, which has one, and the token below may write
// there.
type PublicLocal struct {
	// DNSToken is the deSEC API token. It is a secret: the proxy gets it
	// through .env (DESEC_TOKEN), never through the Caddyfile, which is
	// world-readable.
	DNSToken string `yaml:"dns_token,omitempty"`
	// ChallengeDomain is the CNAME target of _acme-challenge.{base_domain},
	// e.g. "_acme-challenge.gym-phoenix.dedyn.io". Empty means no
	// delegation: the base domain's own zone lives at deSEC.
	ChallengeDomain string `yaml:"challenge_domain,omitempty"`
}

// ChallengeName is the record the certificate authority looks up for the
// wildcard certificate, and the one the school delegates with a CNAME.
func ChallengeName(baseDomain string) string {
	return "_acme-challenge." + baseDomain
}

// Default returns a Config pre-populated with the values used for a fresh
// install. The caller fills in school identity, admin hash and address during
// setup.
func Default() *Config {
	return &Config{
		Version:    ConfigVersion,
		SetupState: SetupStateNeedsSetup,
		Catalog: Catalog{
			URL: "https://raw.githubusercontent.com/lngstck/catalog/main",
		},
		Public: Public{
			Transport: TransportLocal,
		},
	}
}

// Load reads config.yaml from disk. Returns (nil, os.ErrNotExist) if the
// file has not been created yet — callers distinguish a fresh install from
// a real error that way.
func Load() (*Config, error) {
	data, err := os.ReadFile(paths.ConfigFile())
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", paths.ConfigFile(), err)
	}
	if c.Version == 0 {
		c.Version = ConfigVersion
	}
	if c.SetupState == "" {
		c.SetupState = SetupStateNeedsSetup
	}
	return &c, nil
}

// Save writes config.yaml atomically with 0640 permissions. The parent
// directory is created with 0750 if missing.
func (c *Config) Save() error {
	if c == nil {
		return errors.New("config.Save: nil receiver")
	}
	if c.Version == 0 {
		c.Version = ConfigVersion
	}
	if err := paths.EnsureDir(paths.ConfigDir(), 0o750); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	// Prepend a do-not-edit banner so a human peeking at the file knows
	// the source of truth is stackctl.
	const banner = "# Generated by stackctl – edit via the admin web UI or stackctl CLI.\n"
	out := append([]byte(banner), data...)
	return paths.AtomicWrite(paths.ConfigFile(), out, FilePerm)
}

// IsReady is true once setup has completed.
func (c *Config) IsReady() bool {
	return c != nil && c.SetupState == SetupStateReady
}

// Validate performs a minimal structural check used by Save callers in
// later steps. It does NOT check the admin hash format or dex URL — those
// invariants live in their owning packages.
func (c *Config) Validate() error {
	if c == nil {
		return errors.New("nil config")
	}
	if c.Version != ConfigVersion {
		return fmt.Errorf("unsupported config version %d (want %d)", c.Version, ConfigVersion)
	}
	switch c.SetupState {
	case SetupStateNeedsSetup, SetupStateReady:
	default:
		return fmt.Errorf("unknown setup_state %q", c.SetupState)
	}
	switch c.Public.Transport {
	case TransportLocal, TransportDirect:
	case "":
		return errors.New("public.transport must be set")
	default:
		return fmt.Errorf("unknown public.transport %q", c.Public.Transport)
	}
	if c.Public.BaseDomain != "" {
		if err := ValidateBaseDomain(c.Public.BaseDomain); err != nil {
			return fmt.Errorf("public.base_domain: %w", err)
		}
	}
	if c.SetupState != SetupStateNeedsSetup {
		if err := ValidateSlug(c.School.Slug); err != nil {
			return fmt.Errorf("school.slug: %w", err)
		}
		if c.School.Name == "" {
			return errors.New("school.name must be set after setup")
		}
		if c.Public.BaseDomain == "" {
			return errors.New("public.base_domain must be set after setup")
		}
	}
	return nil
}
