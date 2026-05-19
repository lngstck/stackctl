// Package llm verwaltet die llmd-Konfiguration (Provider, Modelle, Personas,
// API-Keys) und schreibt sie direkt in das Verzeichnis, das die llmd-Container
// bind-mounted als /etc/llmd liest. stackctl ist die einzige schreibende
// Instanz; llmd liest read-only und reagiert auf SIGHUP.
//
// Daten leben in /opt/learningstack/llmd/config/:
//   config.yaml          — Hauptdatei (von llmd erwartet)
//   prompts/<id>.md      — System-Prompts pro Persona
//
// Es gibt absichtlich keine separate "Quell"-Datei im stackctl-Tree: die
// in /opt/learningstack/llmd/config/ liegenden Files SIND die Quelle.
package llm

// File bildet das exakte YAML-Schema ab, das llmd erwartet. Aenderungen
// hier muessen mit lngstck/llmd:internal/config/config.go synchron bleiben.
type File struct {
	Version   string     `yaml:"version"`
	Server    Server     `yaml:"server"`
	Providers []Provider `yaml:"providers"`
	Models    []Model    `yaml:"models"`
	Personas  []Persona  `yaml:"personas"`
	APIKeys   []APIKey   `yaml:"api_keys"`
}

type Server struct {
	Listen      string   `yaml:"listen,omitempty"`
	CORSOrigins []string `yaml:"cors_origins"`
}

// Provider beschreibt einen Upstream-LLM-Anbieter. Der eigentliche API-Key
// wird NICHT hier gespeichert, sondern als Env-Variable ueber den .env-File
// von docker-compose injiziert. APIKeyEnv ist der NAME dieser Variable.
type Provider struct {
	ID        string `yaml:"id"`
	Kind      string `yaml:"kind"`
	BaseURL   string `yaml:"base_url"`
	APIKeyEnv string `yaml:"api_key_env"`
}

// Model verknuepft eine schul-lokale Model-ID mit dem Provider und der
// echten Upstream-Bezeichnung.
type Model struct {
	ID         string `yaml:"id"`
	Provider   string `yaml:"provider"`
	UpstreamID string `yaml:"upstream_id"`
}

// Persona ist das, was Clients als "Modell" sehen. PromptFile referenziert
// eine Datei unter prompts/. Params sind Default-Parameter (temperature etc).
type Persona struct {
	ID         string         `yaml:"id"`
	Model      string         `yaml:"model"`
	PromptFile string         `yaml:"prompt_file,omitempty"`
	Params     map[string]any `yaml:"params,omitempty"`
}

// APIKey ist ein bcrypt-gehashter Authentifizierungs-Token fuer Clients
// (typischerweise Open WebUI). AllowedPersonas leer = Zugriff auf alle.
type APIKey struct {
	ID              string   `yaml:"id"`
	Prefix          string   `yaml:"prefix"`
	Hash            string   `yaml:"hash"`
	AllowedPersonas []string `yaml:"allowed_personas"`
}

// CurrentVersion ist die Schema-Version, die stackctl schreibt.
const CurrentVersion = "1"
