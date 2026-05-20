// Package llm verwaltet die llmd-Konfiguration (Provider, Personas, API-Keys)
// und schreibt sie direkt in das Verzeichnis, das die llmd-Container
// bind-mounted als /etc/llmd liest. stackctl ist die einzige schreibende
// Instanz; llmd liest read-only und reagiert auf SIGHUP.
//
// Daten leben in /opt/learningstack/llmd/config/config.yaml. Schema v2:
// Provider tragen ihren API-Key direkt im Feld api_key, Personas zeigen
// direkt auf (provider, upstream_id) — kein separater models[]-Block und
// kein prompt_file mehr. System-Prompts sitzen inline am Persona-Objekt.
//
// Es gibt absichtlich keine separate "Quell"-Datei im stackctl-Tree: die
// in /opt/learningstack/llmd/config/ liegende config.yaml IST die Quelle.
package llm

// File bildet das exakte YAML-Schema ab, das llmd erwartet. Aenderungen
// hier muessen mit lngstck/llmd:internal/config/config.go synchron bleiben.
type File struct {
	Version   string     `yaml:"version"`
	Server    Server     `yaml:"server"`
	Providers []Provider `yaml:"providers"`
	Personas  []Persona  `yaml:"personas"`
	APIKeys   []APIKey   `yaml:"api_keys"`
}

type Server struct {
	Listen      string   `yaml:"listen,omitempty"`
	CORSOrigins []string `yaml:"cors_origins"`
}

// Provider beschreibt einen Upstream-LLM-Anbieter inkl. API-Key.
// APIKey wird im Klartext gespeichert; die Datei liegt mit 0o644 unter
// /opt/learningstack/llmd/config/ — gleicher Blast-Radius wie .env auf
// einem Single-Tenant-Schulserver (siehe llmd ARCHITECTURE.md §3).
type Provider struct {
	ID      string `yaml:"id"`
	Kind    string `yaml:"kind"`
	BaseURL string `yaml:"base_url"`
	APIKey  string `yaml:"api_key,omitempty"`
}

// Persona ist das, was Clients als "Modell" sehen. Verweist direkt auf
// einen Provider + Upstream-Modellnamen. Prompt ist inline; leer = kein
// System-Prompt (Passthrough).
type Persona struct {
	ID         string         `yaml:"id"`
	Provider   string         `yaml:"provider,omitempty"`
	UpstreamID string         `yaml:"upstream_id,omitempty"`
	Prompt     string         `yaml:"prompt,omitempty"`
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
const CurrentVersion = "2"
