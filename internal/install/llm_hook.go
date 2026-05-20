package install

import (
	"embed"
	"fmt"
	"strings"

	"github.com/lngstck/stackctl/internal/envfile"
	"github.com/lngstck/stackctl/internal/llm"
)

// LLMAPIKeyEnv ist der globale Env-Var-Name unter dem stackctl den
// generierten Klartext-Key fuer llmd in die .env schreibt. Apps wie
// Open WebUI referenzieren ihn als ${LLM_API_KEY}.
const LLMAPIKeyEnv = "LLM_API_KEY"

// LLMDefaultKeyID ist die interne ID des automatisch generierten Schul-
// weiten API-Keys. Pro Phase 1 ein einziger Key fuer alle Apps; spaeter
// koennen daneben weitere Keys mit Personas-Restriktion existieren.
const LLMDefaultKeyID = "default"

//go:embed seed/prompts/*.md
var seedPromptsFS embed.FS

// seedPersonas listet die Personas, die beim Erstinstall von llmd angelegt
// werden. Provider/UpstreamID bleiben leer — der Admin verbindet sie via
// UI/CLI mit einem Provider, sobald er einen angelegt hat.
var seedPersonas = []struct {
	ID         string
	PromptFile string // im embed FS unter seed/prompts/
	Params     map[string]any
}{
	{
		ID:         "grundschul-erklaerer",
		PromptFile: "grundschul-erklaerer.md",
		Params:     map[string]any{"temperature": 0.4},
	},
	{
		ID:         "sek2-helfer",
		PromptFile: "sek2-helfer.md",
		Params:     map[string]any{"temperature": 0.7},
	},
}

// seedLLMConfig bereitet die llmd-Installation vor: schreibt eine initiale
// config.yaml mit Beispiel-Personas (inkl. inline Prompts) und generiert
// einen Default-API-Key, dessen Klartext als LLM_API_KEY in die globale
// Section von .env wandert.
//
// Idempotent: existiert bereits eine config.yaml mit mindestens einem
// API-Key, wird nichts ueberschrieben. Damit kann llmd jederzeit ohne
// Datenverlust neuinstalliert werden.
//
// Bei jeder neuen Schluesselgenerierung wird der neue Klartext in .env
// geschrieben — der Key-Name wird in newEnvKeys ausgegeben, damit der
// Aufrufer ihn fuer Rollback erfasst.
func seedLLMConfig(env *envfile.File) (newEnvKeys []string, err error) {
	f, err := llm.Load()
	if err != nil {
		return nil, fmt.Errorf("load llm config: %w", err)
	}

	// Idempotenz: schon konfiguriert? Nicht anfassen.
	if len(f.APIKeys) > 0 {
		return nil, nil
	}

	// Seed-Personas anlegen (nur wenn keine vorhanden — defensiv, weil
	// der Admin theoretisch Personas vor dem ersten Install per CLI
	// haette anlegen koennen).
	if len(f.Personas) == 0 {
		for _, sp := range seedPersonas {
			prompt, err := seedPromptsFS.ReadFile("seed/prompts/" + sp.PromptFile)
			if err != nil {
				return nil, fmt.Errorf("read seed prompt %s: %w", sp.PromptFile, err)
			}
			if err := f.AddPersona(llm.Persona{
				ID:     sp.ID,
				Prompt: strings.TrimSpace(string(prompt)),
				Params: sp.Params,
				// Provider + UpstreamID bewusst leer — Admin haengt nach
				// dem Anlegen eines Providers eine Persona dran.
			}); err != nil {
				return nil, fmt.Errorf("seed persona %s: %w", sp.ID, err)
			}
		}
	}

	// Schul-Default-Key besorgen.
	//
	// Zwei Pfade:
	//
	// (a) LLM_API_KEY existiert bereits in .env — typischer Recovery-Fall,
	//     z.B. wenn der Admin /opt/learningstack/llmd/config/config.yaml
	//     geloescht hat (oder ein App-Update den Seed neu triggert).
	//     Wir re-bcrypten den BESTEHENDEN Klartext, damit Open WebUI mit
	//     seinem schon-gespeicherten ${LLM_API_KEY} weiter authentisieren
	//     kann — kein .env-Update, kein open-webui-Recreate noetig.
	//
	// (b) Frischer Install — neuer Klartext + neuer Hash, Klartext in
	//     .env schreiben. Nach diesem Moment ist der Klartext nicht mehr
	//     rekonstruierbar; bei Verlust nur per `key revoke` + `key create`
	//     wieder zu beschaffen.
	if existing, ok := env.Get(LLMAPIKeyEnv); ok && existing != "" {
		prefix, hash, err := llm.HashAPIKey(existing)
		if err != nil {
			return nil, fmt.Errorf("reuse existing LLM_API_KEY: %w", err)
		}
		if err := f.AddAPIKey(llm.APIKey{ID: LLMDefaultKeyID, Prefix: prefix, Hash: hash}); err != nil {
			return nil, fmt.Errorf("add api key (reused): %w", err)
		}
		// Bewusst kein newEnvKeys-Eintrag — wir haben .env nicht veraendert,
		// also auch nichts fuer Rollback einzutragen.
	} else {
		plaintext, prefix, hash, err := llm.GenerateAPIKey()
		if err != nil {
			return nil, fmt.Errorf("generate api key: %w", err)
		}
		if err := f.AddAPIKey(llm.APIKey{
			ID:     LLMDefaultKeyID,
			Prefix: prefix,
			Hash:   hash,
			// AllowedPersonas leer = Zugriff auf alle Personas. Open WebUI
			// (und ggf. andere Apps) filtert die User-Sichtbarkeit selbst.
		}); err != nil {
			return nil, fmt.Errorf("add api key: %w", err)
		}
		env.Set(envfile.GlobalSection, LLMAPIKeyEnv, plaintext)
		newEnvKeys = append(newEnvKeys, LLMAPIKeyEnv)
	}

	if err := llm.Save(f); err != nil {
		return newEnvKeys, fmt.Errorf("save llm config: %w", err)
	}
	// Kein Reload (SIGHUP) — der Container laeuft an dieser Stelle des
	// Install-Flows noch nicht. compose up startet ihn gleich mit der
	// frisch geschriebenen Config.

	return newEnvKeys, nil
}
