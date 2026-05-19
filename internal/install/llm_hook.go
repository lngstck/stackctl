package install

import (
	"embed"
	"fmt"

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
// werden. Modell-Zuweisung bleibt leer — der Admin muss erst Provider/
// Modelle anlegen und dann zuweisen (`stackctl llm persona set-model`).
var seedPersonas = []struct {
	ID         string
	PromptFile string
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
// config.yaml mit Beispiel-Personas und generiert einen Default-API-Key,
// dessen Klartext als LLM_API_KEY in die globale Section von .env wandert.
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
			if err := f.AddPersona(llm.Persona{
				ID:         sp.ID,
				PromptFile: sp.PromptFile,
				Params:     sp.Params,
			}); err != nil {
				return nil, fmt.Errorf("seed persona %s: %w", sp.ID, err)
			}
			content, err := seedPromptsFS.ReadFile("seed/prompts/" + sp.PromptFile)
			if err != nil {
				return nil, fmt.Errorf("read seed prompt %s: %w", sp.PromptFile, err)
			}
			if err := llm.SavePrompt(sp.ID, string(content)); err != nil {
				return nil, fmt.Errorf("write seed prompt %s: %w", sp.ID, err)
			}
		}
	}

	// Schul-Default-Key generieren. Klartext landet in .env, Hash in
	// llm.yaml. Der Klartext ist nach diesem Moment nicht mehr
	// rekonstruierbar — bei Verlust per `stackctl llm key revoke default`
	// + `key create` neu erzeugen.
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

	// Klartext in die globale Section schreiben, damit alle Apps ihn
	// als ${LLM_API_KEY} referenzieren koennen.
	env.Set(envfile.GlobalSection, LLMAPIKeyEnv, plaintext)
	newEnvKeys = append(newEnvKeys, LLMAPIKeyEnv)

	if err := llm.Save(f); err != nil {
		return newEnvKeys, fmt.Errorf("save llm config: %w", err)
	}
	// Kein Reload (SIGHUP) — der Container laeuft an dieser Stelle des
	// Install-Flows noch nicht. compose up startet ihn gleich mit der
	// frisch geschriebenen Config.

	return newEnvKeys, nil
}
