// Package config loads and saves the two authoritative YAML files for
// stackctl: config.yaml (school settings, admin hash, dex binding) and
// state.yaml (installed containers, port allocations, tunnel flags).
//
// Both files are expected under $STACKCTL_DIR/config/ and are written with
// mode 0640, owner learningstack:learningstack on a production install.
package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/lngstck/stackctl/internal/paths"
)

// ConfigVersion is the schema version of config.yaml emitted by this build.
//
// v3 replaced the implicit address model — every install lived under
// "{slug}.learningstack.online" — with an explicit public: block, so a school
// can also be reached under its own domain, either through a relay or
// directly from its own server.
const ConfigVersion = 3

// DefaultRootDomain is the operator-run root that relay-hosted schools get
// their subdomains under. It is only a default: an install may carry any
// base_domain, including one the school owns.
const DefaultRootDomain = "learningstack.online"

// Defaults for the operator-run relay endpoint.
const (
	DefaultRelaySSHHost = "sish." + DefaultRootDomain
	DefaultRelaySSHPort = 2222
)

// Transport kinds for Public.Transport.
const (
	// TransportRelay reaches this install through an SSH reverse tunnel to a
	// sish endpoint. The server may sit behind NAT. TLS terminates at the
	// relay, which therefore sees request contents in the clear.
	TransportRelay = "relay"
	// TransportDirect reaches this install on the server itself, which holds
	// public 80/443 and terminates TLS locally.
	TransportDirect = "direct"
)

// FilePerm is the on-disk permission for config.yaml and state.yaml.
// 0640 = owner rw, group r, others none (see ARCHITECTURE.md §16).
const FilePerm = 0o640

// SetupState drives the three-state setup machine from ARCHITECTURE.md §10.
type SetupState string

const (
	SetupStateNeedsSetup            SetupState = "needs_setup"
	SetupStateAwaitingRegistration  SetupState = "awaiting_registration"
	SetupStateReady                 SetupState = "ready"
)

// Config mirrors config.yaml. Field tags use snake_case to match the format
// shown in ARCHITECTURE.md §12.
type Config struct {
	Version      int               `yaml:"version"`
	SetupState   SetupState        `yaml:"setup_state"`
	School       School            `yaml:"school"`
	Catalog      Catalog           `yaml:"catalog"`
	Admin        Admin             `yaml:"admin"`
	Dex          Dex               `yaml:"dex"`
	Registration Registration      `yaml:"registration,omitempty"`
	GlobalEnv    map[string]string `yaml:"global_env,omitempty"`
	Public       Public            `yaml:"public"`
	// AutoUpdate steuert das naechtliche Auto-Update aller Apps.
	AutoUpdate AutoUpdate `yaml:"auto_update,omitempty"`
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

// Dex binds this school to a client registration on the central dex.
// Phase 1 hardcodes the upstream to "moin.schule via central dex", so no
// upstream selector is stored here. AuthURL is immutable after setup.
type Dex struct {
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	AuthURL      string `yaml:"auth_url"`
}

// Registration tracks progress of the awaiting_registration state.
type Registration struct {
	StateEnteredAt            string `yaml:"state_entered_at,omitempty"`
	PackagePath               string `yaml:"package_path,omitempty"`
	OperatorPubkeyFingerprint string `yaml:"operator_pubkey_fingerprint,omitempty"`
}

// Public describes how this install is reached from the internet. It is the
// authoritative source for every public hostname stackctl builds — see
// internal/public for the constructors that read it.
type Public struct {
	// Transport is how traffic arrives: TransportRelay or TransportDirect.
	Transport string `yaml:"transport"`
	// BaseDomain is the parent of every public hostname. Apps answer at
	// {app_id}.{base_domain}, the local Dex at auth.{base_domain}. For an
	// operator-relay install this is {slug}.learningstack.online; a school
	// with its own domain carries something like "ls.gym-phoenix.de".
	BaseDomain string `yaml:"base_domain"`
	// Relay targets the sish endpoint and is only meaningful for
	// TransportRelay. Whether that endpoint is operator-run or school-run
	// makes no difference to stackctl — it is the same SSH reverse tunnel
	// either way, and only the operator runbook differs.
	Relay PublicRelay `yaml:"relay,omitempty"`
}

// PublicRelay stores the sish target. The private key lives next to
// config.yaml as tunnel_key (see paths.TunnelKeyFile).
type PublicRelay struct {
	SSHHost string `yaml:"ssh_host"`
	SSHPort int    `yaml:"ssh_port"`
}

// RelayBaseDomain returns the base domain an operator-hosted relay install
// gets by default.
func RelayBaseDomain(slug string) string {
	return slug + "." + DefaultRootDomain
}

// Default returns a Config pre-populated with the values used for a fresh
// install. The caller fills in school identity, admin hash, and dex fields
// during setup.
func Default() *Config {
	return &Config{
		Version:    ConfigVersion,
		SetupState: SetupStateNeedsSetup,
		Catalog: Catalog{
			URL: "https://raw.githubusercontent.com/lngstck/catalog/main",
		},
		Public: Public{
			Transport: TransportRelay,
			Relay: PublicRelay{
				SSHHost: DefaultRelaySSHHost,
				SSHPort: DefaultRelaySSHPort,
			},
		},
		GlobalEnv: map[string]string{},
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
	if c.GlobalEnv == nil {
		c.GlobalEnv = map[string]string{}
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

// IsReady is true once setup has completed and the tunnel client is
// registered on System 1.
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
	case SetupStateNeedsSetup, SetupStateAwaitingRegistration, SetupStateReady:
	default:
		return fmt.Errorf("unknown setup_state %q", c.SetupState)
	}
	switch c.Public.Transport {
	case TransportRelay, TransportDirect:
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
