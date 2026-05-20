package install

import (
	"strings"
	"testing"

	"github.com/lngstck/stackctl/internal/envfile"
	"github.com/lngstck/stackctl/internal/llm"
	"github.com/lngstck/stackctl/internal/paths"
)

func TestSeedLLMConfigFirstRun(t *testing.T) {
	root := t.TempDir()
	t.Setenv(paths.EnvLearningstackDir, root)

	env := envfile.New()
	added, err := seedLLMConfig(env)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if !contains(added, LLMAPIKeyEnv) {
		t.Errorf("expected %q in newEnvKeys, got %v", LLMAPIKeyEnv, added)
	}

	// LLM_API_KEY landet als globaler Env-Wert
	v, ok := env.Get(LLMAPIKeyEnv)
	if !ok {
		t.Fatal("LLM_API_KEY not set in env")
	}
	if !strings.HasPrefix(v, "llm-") {
		t.Errorf("LLM_API_KEY format wrong: %q", v)
	}

	// Config-Datei wurde geschrieben mit den 2 Seed-Personas + 1 Key
	f, err := llm.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Personas) != 2 {
		t.Errorf("expected 2 personas, got %d", len(f.Personas))
	}
	if len(f.APIKeys) != 1 || f.APIKeys[0].ID != LLMDefaultKeyID {
		t.Errorf("expected one key with id %q, got %+v", LLMDefaultKeyID, f.APIKeys)
	}

	// Inline-Prompts an den Personas
	for _, id := range []string{"grundschul-erklaerer", "sek2-helfer"} {
		p := f.GetPersona(id)
		if p == nil {
			t.Errorf("persona %s missing", id)
			continue
		}
		if len(strings.TrimSpace(p.Prompt)) < 50 {
			t.Errorf("prompt for %s suspiciously short: %d chars", id, len(p.Prompt))
		}
		// Seed-Personas haben absichtlich noch keinen Provider — der Admin
		// haengt sie nach dem Anlegen eines Providers per UI/CLI dran.
		if p.Provider != "" {
			t.Errorf("seed persona %s should ship without provider, got %q", id, p.Provider)
		}
	}
}

func TestSeedLLMConfigIdempotent(t *testing.T) {
	root := t.TempDir()
	t.Setenv(paths.EnvLearningstackDir, root)

	env := envfile.New()
	// Erstinstall
	if _, err := seedLLMConfig(env); err != nil {
		t.Fatal(err)
	}
	firstKey, _ := env.Get(LLMAPIKeyEnv)

	// Zweiter Aufruf (Re-Install): darf NICHT neu erzeugen
	added2, err := seedLLMConfig(env)
	if err != nil {
		t.Fatal(err)
	}
	if len(added2) != 0 {
		t.Errorf("re-install should not add env keys, got %v", added2)
	}

	// Key in der Datei muss derselbe geblieben sein
	f, _ := llm.Load()
	if len(f.APIKeys) != 1 {
		t.Fatalf("expected 1 key after re-install, got %d", len(f.APIKeys))
	}

	// Klartext-Key in .env darf sich auch nicht geaendert haben
	secondKey, _ := env.Get(LLMAPIKeyEnv)
	if secondKey != firstKey {
		t.Errorf("LLM_API_KEY changed between installs: %q -> %q", firstKey, secondKey)
	}
}

func TestSeedLLMConfigReusesExistingEnvKey(t *testing.T) {
	// Recovery-Pfad: config.yaml ist weg, .env hat aber noch LLM_API_KEY.
	// Erwartung: seed re-bcryptet den bestehenden Klartext und schreibt
	// .env NICHT um — Open WebUI muss mit demselben Plaintext weiter
	// authentisieren koennen.
	root := t.TempDir()
	t.Setenv(paths.EnvLearningstackDir, root)

	// Echten Key generieren (statt was Plaintextes zu erfinden), damit
	// das Format zu HashAPIKey passt.
	plaintext, _, _, err := llm.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}

	env := envfile.New()
	env.Set(envfile.GlobalSection, LLMAPIKeyEnv, plaintext)

	added, err := seedLLMConfig(env)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if contains(added, LLMAPIKeyEnv) {
		t.Errorf("expected LLM_API_KEY NOT to be reported as new env key (was reused), got %v", added)
	}

	// .env-Plaintext unveraendert
	if got, _ := env.Get(LLMAPIKeyEnv); got != plaintext {
		t.Errorf("plaintext was rewritten: %q -> %q", plaintext, got)
	}

	// Config-Datei hat einen Key, dessen Prefix zu plaintext passt
	f, _ := llm.Load()
	if len(f.APIKeys) != 1 {
		t.Fatalf("expected 1 api key, got %d", len(f.APIKeys))
	}
	if !strings.HasPrefix(plaintext, f.APIKeys[0].Prefix+"-") {
		t.Errorf("prefix %q does not match plaintext %q", f.APIKeys[0].Prefix, plaintext)
	}
}

func TestSeedLLMConfigPreservesAdminPersonas(t *testing.T) {
	root := t.TempDir()
	t.Setenv(paths.EnvLearningstackDir, root)

	// Admin hat vor dem llmd-Install per CLI eigene Persona angelegt.
	f := &llm.File{}
	mustNoErr(t, f.AddPersona(llm.Persona{ID: "custom-persona"}))
	mustNoErr(t, llm.Save(f))

	env := envfile.New()
	if _, err := seedLLMConfig(env); err != nil {
		t.Fatal(err)
	}

	// Seed-Personas werden NICHT zusaetzlich angelegt, weil bereits
	// welche existieren. Die custom-Persona bleibt unangetastet.
	got, _ := llm.Load()
	if len(got.Personas) != 1 || got.Personas[0].ID != "custom-persona" {
		t.Errorf("admin personas were trampled: %+v", got.Personas)
	}
	// Aber der Schluessel wurde generiert (weil keiner da war).
	if len(got.APIKeys) != 1 {
		t.Errorf("expected api key to be created, got %d", len(got.APIKeys))
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
