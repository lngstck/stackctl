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

	// Prompts auf der Platte
	for _, id := range []string{"grundschul-erklaerer", "sek2-helfer"} {
		content, err := llm.LoadPrompt(id)
		if err != nil {
			t.Errorf("load prompt %s: %v", id, err)
		}
		if len(content) < 50 {
			t.Errorf("prompt %s suspiciously short: %d chars", id, len(content))
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
