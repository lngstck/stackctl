// Package catalog handles downloading, caching, and loading the app catalog
// and individual container definitions from the catalog server (typically
// raw.githubusercontent.com/lngstck/catalog or catalog.learningstack.online).
//
// The catalog index (catalog.yaml) is a lightweight list of available apps.
// Each app has a full definition YAML cached under config/catalog/containers/.
package catalog

import (
	"github.com/lngstck/stackctl/internal/compose"
)

// Index represents the top-level catalog.yaml downloaded from the catalog
// server. It enumerates available apps and declares global environment
// variables that apps may reference.
type Index struct {
	Version        string           `yaml:"version"`
	Apps           []AppSummary     `yaml:"apps"`
	GlobalEnvSchema []GlobalEnvSpec `yaml:"global_env_schema,omitempty"`
}

// AppSummary is one entry in the catalog index — enough to display in the
// apps page without downloading the full definition.
type AppSummary struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	Category    string `yaml:"category"`
	Description string `yaml:"description"`
}

// GlobalEnvSpec declares a global environment variable that apps may
// reference (e.g. LLM_ENDPOINT). The admin fills these in during setup or
// settings; stackctl writes them into the global section of .env.
type GlobalEnvSpec struct {
	Key         string `yaml:"key"`
	Description string `yaml:"description"`
	Required    bool   `yaml:"required,omitempty"`
	Default     string `yaml:"default,omitempty"`
}

// Definition is the full container definition YAML for a single app. It
// embeds compose.AppDefinition (the fields compose needs to generate
// docker-compose.yml) and adds catalog/install-specific fields.
type Definition struct {
	compose.AppDefinition `yaml:",inline"`

	// Display / catalog metadata.
	Description string `yaml:"description"`
	Category    string `yaml:"category"`

	// Secrets to auto-generate on install.
	Secrets []SecretSpec `yaml:"secrets,omitempty"`

	// Global env vars this app uses (with defaults).
	GlobalEnv []GlobalEnvRef `yaml:"global_env,omitempty"`

	// Interactive prompts shown before install.
	Prompts []Prompt `yaml:"prompts,omitempty"`

	// OIDC client registration for Dex.
	OIDC *OIDCSpec `yaml:"oidc,omitempty"`

	// If set, stackctl injects STACKCTL_ADMIN_PASSWORD under this key.
	AdminPasswordEnv string `yaml:"admin_password_env,omitempty"`

	// Binaries to download before container start.
	Binaries []BinarySpec `yaml:"binaries,omitempty"`

	// Post-install scripts and messages.
	Scripts     *Scripts     `yaml:"scripts,omitempty"`
	PostInstall *PostInstall `yaml:"post_install,omitempty"`

	// Documentation links for the app detail page.
	Links *Links `yaml:"links,omitempty"`
}

// SecretSpec tells stackctl to auto-generate a secret and store it in .env.
type SecretSpec struct {
	Key      string `yaml:"key"`               // e.g. "LANGFLOW_OIDC_SECRET"
	Generate string `yaml:"generate,omitempty"` // "secret" (default) | "password" | "api_key"
	Prefix   string `yaml:"prefix,omitempty"`   // only for api_key, e.g. "sk-lf"
}

// GlobalEnvRef declares that this app needs a global env var. If the var
// is already set in .env, it's reused; otherwise the default is applied.
type GlobalEnvRef struct {
	Key         string `yaml:"key"`
	Description string `yaml:"description,omitempty"`
	Required    bool   `yaml:"required,omitempty"`
	Default     string `yaml:"default,omitempty"`
}

// Prompt is an interactive question shown to the admin before install.
type Prompt struct {
	Key      string   `yaml:"key"`
	Question string   `yaml:"question"`
	Required bool     `yaml:"required,omitempty"`
	Default  string   `yaml:"default,omitempty"`
	Validate string   `yaml:"validate,omitempty"` // "email" | "int" | "url" | "" (any string)
	Options  []string `yaml:"options,omitempty"`   // multi-choice
	Hint     string   `yaml:"hint,omitempty"`
}

// OIDCSpec declares that this app is an OIDC client registered with Dex.
type OIDCSpec struct {
	ClientID     string `yaml:"client_id"`
	RedirectPath string `yaml:"redirect_path"` // e.g. "/oauth/oidc/callback"
}

// BinarySpec tells stackctl to download a file before starting the app.
type BinarySpec struct {
	Source      string `yaml:"source"`
	Destination string `yaml:"destination"`
	Mode        string `yaml:"mode,omitempty"` // e.g. "0755"
}

// Scripts holds post-install script definitions.
type Scripts struct {
	PostInstall []ScriptStep `yaml:"post_install,omitempty"`
}

// ScriptStep is one post-install command.
type ScriptStep struct {
	Type      string `yaml:"type"`                // "docker-exec" | "host"
	Container string `yaml:"container,omitempty"` // for docker-exec
	Wait      string `yaml:"wait,omitempty"`      // "healthy" | "started" | "30" (seconds)
	Command   string `yaml:"command"`
}

// SecretToShow identifies an env key to display after install, with an
// optional human-readable label.
type SecretToShow struct {
	Key   string `yaml:"key"`
	Label string `yaml:"label,omitempty"`
}

// PostInstall holds messages and secrets to display after a successful install.
type PostInstall struct {
	Messages      []string       `yaml:"messages,omitempty"`
	SecretsToShow []SecretToShow `yaml:"secrets_to_show,omitempty"`
}

// Links are documentation pointers for the app detail page.
type Links struct {
	Homepage string `yaml:"homepage,omitempty"`
	Docs     string `yaml:"docs,omitempty"`
}

// ToCompose returns the embedded compose-level definition, suitable for
// passing to compose.BuildServiceBlock or compose.Regenerate.
func (d *Definition) ToCompose() *compose.AppDefinition {
	return &d.AppDefinition
}
