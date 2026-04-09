package envfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSections(t *testing.T) {
	raw := `# === global ===
SCHOOL_NAME=Gymnasium Phoenix
SCHOOL_SLUG=phoenix

# === postgres ===
POSTGRES_PASSWORD=hunter2

# === langflow ===
LANGFLOW_DB_PASSWORD=secret
LANGFLOW_OIDC_SECRET=abc
`
	f, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if v, ok := f.Get("SCHOOL_NAME"); !ok || v != "Gymnasium Phoenix" {
		t.Errorf("SCHOOL_NAME = %q, %v", v, ok)
	}
	if v, _ := f.Get("POSTGRES_PASSWORD"); v != "hunter2" {
		t.Errorf("POSTGRES_PASSWORD = %q", v)
	}
	secs := f.Sections()
	wantSecs := []string{"global", "postgres", "langflow"}
	if !equalSlice(secs, wantSecs) {
		t.Errorf("Sections = %v, want %v", secs, wantSecs)
	}
	langflowKeys := f.Keys("langflow")
	wantKeys := []string{"LANGFLOW_DB_PASSWORD", "LANGFLOW_OIDC_SECRET"}
	if !equalSlice(langflowKeys, wantKeys) {
		t.Errorf("langflow keys = %v, want %v", langflowKeys, wantKeys)
	}
}

func TestParseKeysWithoutSectionBecomeGlobal(t *testing.T) {
	raw := "FOO=bar\nBAZ=qux\n"
	f, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := f.Keys(GlobalSection); !equalSlice(got, []string{"FOO", "BAZ"}) {
		t.Errorf("global keys = %v", got)
	}
}

func TestParseSkipsBlankAndComments(t *testing.T) {
	raw := "\n# comment\n\nFOO=bar\n# another\n"
	f, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if v, _ := f.Get("FOO"); v != "bar" {
		t.Errorf("FOO = %q", v)
	}
}

func TestParseRejectsInvalidLines(t *testing.T) {
	bad := []string{
		"NOTANASSIGNMENT\n",
		"=value\n",
	}
	for _, raw := range bad {
		if _, err := Parse(raw); err == nil {
			t.Errorf("Parse(%q) expected error", raw)
		}
	}
}

func TestSetMovesKeyBetweenSections(t *testing.T) {
	f := New()
	f.Set("postgres", "POSTGRES_PASSWORD", "one")
	f.Set("postgres", "POSTGRES_PASSWORD", "two") // same section, update
	if v, _ := f.Get("POSTGRES_PASSWORD"); v != "two" {
		t.Errorf("update failed: %q", v)
	}
	if len(f.Keys("postgres")) != 1 {
		t.Errorf("duplicate after update: %v", f.Keys("postgres"))
	}

	f.Set("global", "POSTGRES_PASSWORD", "three") // move to global
	if !equalSlice(f.Keys("postgres"), []string{}) && f.Keys("postgres") != nil {
		t.Errorf("postgres not pruned: %v", f.Keys("postgres"))
	}
	if !containsString(f.Sections(), "postgres") == false && containsString(f.Sections(), "postgres") {
		// Section should be gone because it's empty.
		t.Errorf("empty postgres section should be removed: %v", f.Sections())
	}
	if !equalSlice(f.Keys("global"), []string{"POSTGRES_PASSWORD"}) {
		t.Errorf("global keys = %v", f.Keys("global"))
	}
}

func TestDeletePrunesSection(t *testing.T) {
	f := New()
	f.Set("postgres", "PG_PASSWORD", "x")
	f.Delete("PG_PASSWORD")
	if _, ok := f.Get("PG_PASSWORD"); ok {
		t.Error("key still present after delete")
	}
	if containsString(f.Sections(), "postgres") {
		t.Errorf("empty postgres section should be removed: %v", f.Sections())
	}
}

func TestRenderPutsGlobalFirst(t *testing.T) {
	f := New()
	// Intentionally set postgres first, global later.
	f.Set("postgres", "POSTGRES_PASSWORD", "x")
	f.Set("langflow", "LANGFLOW_DB_PASSWORD", "y")
	f.Set("global", "SCHOOL_NAME", "Phoenix")

	out := f.Render()
	gIdx := strings.Index(out, "# === global ===")
	pIdx := strings.Index(out, "# === postgres ===")
	lIdx := strings.Index(out, "# === langflow ===")
	if gIdx == -1 || pIdx == -1 || lIdx == -1 {
		t.Fatalf("missing section header:\n%s", out)
	}
	if !(gIdx < pIdx && pIdx < lIdx) {
		t.Errorf("unexpected section order:\n%s", out)
	}
	if !strings.Contains(out, "SCHOOL_NAME=Phoenix") {
		t.Errorf("value missing:\n%s", out)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")

	f := New()
	f.Set("global", "SCHOOL_NAME", "Phoenix")
	f.Set("global", "SCHOOL_SLUG", "phoenix")
	f.Set("postgres", "POSTGRES_PASSWORD", "pw")
	f.Set("langflow", "LANGFLOW_DB_PASSWORD", "db")
	f.Set("langflow", "LANGFLOW_OIDC_SECRET", "oidc")

	if err := f.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != FilePerm {
		t.Errorf("perm = %o, want %o", perm, FilePerm)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	for _, k := range f.AllKeys() {
		want, _ := f.Get(k)
		got, ok := loaded.Get(k)
		if !ok || got != want {
			t.Errorf("round-trip key %s: got=%q ok=%v, want=%q", k, got, ok, want)
		}
	}
	if !equalSlice(loaded.Sections(), []string{"global", "postgres", "langflow"}) {
		t.Errorf("loaded sections = %v", loaded.Sections())
	}
}

func TestLoadMissingReturnsEmpty(t *testing.T) {
	f, err := Load(filepath.Join(t.TempDir(), "does-not-exist.env"))
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if len(f.AllKeys()) != 0 {
		t.Errorf("expected empty file, got %v", f.AllKeys())
	}
}

// -- helpers ---------------------------------------------------------------

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsString(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}
