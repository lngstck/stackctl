package catalog

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/lngstck/stackctl/internal/paths"
)

const testCatalogYAML = `version: "1.0"
apps:
  - id: postgres
    name: PostgreSQL
    category: infrastructure
    description: Zentrale Datenbank
  - id: dex
    name: Dex
    category: infrastructure
    description: OIDC-Provider
  - id: langflow
    name: Langflow
    category: education
    description: Visueller KI-Flow Builder
global_env_schema:
  - key: LLM_ENDPOINT
    description: LLM-Endpunkt
    required: false
    default: https://llm.learningstack.online/v1
`

const testAppDefYAML = `id: langflow
name: Langflow
version: "1.1.0"
description: Visueller KI-Flow Builder
category: education
image:
  name: registry.learningstack.online/langflow
  tag: "1.1.0"
ports:
  - host: 8320
    container: 7860
    description: Web-UI
volumes:
  - host: /opt/learningstack/langflow/data
    container: /app/data
environment:
  - key: LANGFLOW_DATABASE_URL
    value: "postgresql://langflow:${LANGFLOW_DB_PASSWORD}@ls-postgres:5432/langflow"
depends_on:
  - postgres
  - dex
oidc:
  client_id: langflow
  redirect_path: /oauth/callback
secrets:
  - key: LANGFLOW_DB_PASSWORD
    generate: password
  - key: LANGFLOW_OIDC_SECRET
    generate: secret
prompts:
  - key: LANGFLOW_EXTRA_SETTING
    question: Extra-Setting?
    required: false
    default: "none"
    hint: Optionale Einstellung
post_install:
  messages:
    - "Langflow läuft unter Port 8320"
  secrets_to_show:
    - key: LANGFLOW_DB_PASSWORD
      label: Datenbank-Passwort
`

// testStubYAML returns a minimal-but-valid Definition YAML for catalog
// entries the test doesn't care about individually but Sync still tries to
// fetch.
func testStubYAML(id, name, image, tag string) string {
	return "id: " + id + "\n" +
		"name: " + name + "\n" +
		"version: \"1.0\"\n" +
		"description: stub\n" +
		"category: infrastructure\n" +
		"image:\n  name: " + image + "\n  tag: \"" + tag + "\"\n"
}

func withTempDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(paths.EnvStackctlDir, dir)
}

func startTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/catalog.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testCatalogYAML))
	})
	mux.HandleFunc("/containers/langflow.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testAppDefYAML))
	})
	// Stubs fuer die anderen im Index gelisteten Apps — Sync laedt seit
	// dem Definition-Refetch jede einzeln nach.
	mux.HandleFunc("/containers/postgres.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testStubYAML("postgres", "PostgreSQL", "postgres", "18")))
	})
	mux.HandleFunc("/containers/dex.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testStubYAML("dex", "Dex", "ghcr.io/dexidp/dex", "v2.45.1")))
	})
	mux.HandleFunc("/containers/missing.yaml", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestSyncAndLoadIndex(t *testing.T) {
	withTempDir(t)
	srv := startTestServer(t)

	ok, err := Sync(srv.URL)
	if !ok || err != nil {
		t.Fatalf("Sync: ok=%v err=%v", ok, err)
	}

	// File should exist.
	if _, err := os.Stat(paths.CatalogIndexFile()); err != nil {
		t.Fatalf("catalog.yaml not cached: %v", err)
	}

	idx, err := LoadIndex()
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if len(idx.Apps) != 3 {
		t.Errorf("apps count = %d, want 3", len(idx.Apps))
	}
	if idx.Apps[0].ID != "postgres" {
		t.Errorf("first app = %q", idx.Apps[0].ID)
	}
	if len(idx.GlobalEnvSchema) != 1 || idx.GlobalEnvSchema[0].Key != "LLM_ENDPOINT" {
		t.Errorf("global_env_schema = %+v", idx.GlobalEnvSchema)
	}
}

func TestListAppsSorted(t *testing.T) {
	withTempDir(t)
	srv := startTestServer(t)
	Sync(srv.URL)

	apps, err := ListApps()
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	// education before infrastructure
	if apps[0].Category != "education" {
		t.Errorf("first category = %q, want education", apps[0].Category)
	}
}

func TestFetchAndLoadDefinition(t *testing.T) {
	withTempDir(t)
	srv := startTestServer(t)

	def, err := FetchDefinition(srv.URL, "langflow")
	if err != nil {
		t.Fatalf("FetchDefinition: %v", err)
	}
	if def.ID != "langflow" {
		t.Errorf("ID = %q", def.ID)
	}
	if def.Image.FullImage() != "registry.learningstack.online/langflow:1.1.0" {
		t.Errorf("image = %q", def.Image.FullImage())
	}
	if def.OIDC == nil || def.OIDC.ClientID != "langflow" {
		t.Errorf("OIDC = %+v", def.OIDC)
	}
	if len(def.Secrets) != 2 {
		t.Errorf("secrets count = %d", len(def.Secrets))
	}
	if len(def.Prompts) != 1 || def.Prompts[0].Key != "LANGFLOW_EXTRA_SETTING" {
		t.Errorf("prompts = %+v", def.Prompts)
	}

	// Reload from cache.
	cached, err := LoadDefinition("langflow")
	if err != nil {
		t.Fatalf("LoadDefinition: %v", err)
	}
	if cached.ID != def.ID || cached.Version != def.Version {
		t.Error("cached definition mismatch")
	}
}

func TestGetOrFetchCacheMiss(t *testing.T) {
	withTempDir(t)
	srv := startTestServer(t)

	def, err := GetOrFetch(srv.URL, "langflow")
	if err != nil {
		t.Fatalf("GetOrFetch: %v", err)
	}
	if def.ID != "langflow" {
		t.Errorf("ID = %q", def.ID)
	}
}

func TestFetchDefinitionNotFound(t *testing.T) {
	withTempDir(t)
	srv := startTestServer(t)

	_, err := FetchDefinition(srv.URL, "missing")
	if err == nil {
		t.Error("expected error for missing definition")
	}
}

func TestValidateOK(t *testing.T) {
	withTempDir(t)
	srv := startTestServer(t)
	def, _ := FetchDefinition(srv.URL, "langflow")

	problems := Validate(def)
	if len(problems) > 0 {
		t.Errorf("unexpected problems: %v", problems)
	}
}

func TestValidateBad(t *testing.T) {
	def := &Definition{} // all empty
	problems := Validate(def)
	if len(problems) == 0 {
		t.Error("empty definition should have problems")
	}
}

func TestMissingDependencies(t *testing.T) {
	def := &Definition{}
	def.DependsOn = []string{"postgres", "dex"}

	installed := map[string]bool{"postgres": true}
	missing := MissingDependencies(def, installed)
	if len(missing) != 1 || missing[0] != "dex" {
		t.Errorf("missing = %v", missing)
	}

	installed["dex"] = true
	missing = MissingDependencies(def, installed)
	if len(missing) != 0 {
		t.Errorf("should be empty: %v", missing)
	}
}

func TestHasUpdate(t *testing.T) {
	if !HasUpdate("1.0.0", "1.1.0") {
		t.Error("should detect update")
	}
	if HasUpdate("1.1.0", "1.1.0") {
		t.Error("same version is not an update")
	}
	if HasUpdate("", "1.1.0") {
		t.Error("empty installed should not trigger update")
	}
}

func TestToCompose(t *testing.T) {
	withTempDir(t)
	srv := startTestServer(t)
	def, _ := FetchDefinition(srv.URL, "langflow")

	cd := def.ToCompose()
	if cd.ID != "langflow" {
		t.Errorf("compose ID = %q", cd.ID)
	}
	if cd.Image.FullImage() != "registry.learningstack.online/langflow:1.1.0" {
		t.Errorf("compose image = %q", cd.Image.FullImage())
	}
}
