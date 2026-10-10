package web

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lngstck/stackctl/internal/llm"
)

func TestAbbreviate(t *testing.T) {
	for name, want := range map[string]string{
		"Uptime Kuma":  "UK",
		"CryptPad":     "CP",
		"Stirling-PDF": "SP",
		"Kiwix":        "Ki",
		"Wiki.js":      "WJ",
		"x":            "X",
		"":             "Fa", // fallback id "fallback"
	} {
		if got := abbreviate(name, "fallback"); got != want {
			t.Errorf("abbreviate(%q) = %q, want %q", name, got, want)
		}
	}
}

// Ein App-Farbton darf nicht bei jedem Laden wechseln.
func TestFaceIsStable(t *testing.T) {
	a, b := faceFor("nextcloud", "Nextcloud"), faceFor("nextcloud", "Nextcloud")
	if a != b || a.Abbr != "Ne" || a.Icon != "" {
		t.Errorf("faceFor = %+v / %+v", a, b)
	}
	if f := faceFor("pylearn", "PyLearn"); !f.Own || f.Icon != "app-pylearn" {
		t.Errorf("eigene App: %+v", f)
	}
	if f := faceFor("postgres", "PostgreSQL"); f.Icon != "database" {
		t.Errorf("Grundversorgung ohne Icon: %+v", f)
	}
}

func TestShelvesFollowCategoryOrder(t *testing.T) {
	shelves := shelvesFor([]appSheet{
		{ID: "z", Name: "Zeta", Category: "tools"},
		{ID: "b", Name: "beta", Category: "education"},
		{ID: "a", Name: "Alpha", Category: "education"},
		{ID: "q", Name: "Quiz", Category: "quizzes"},
	})
	var titles []string
	for _, s := range shelves {
		titles = append(titles, s.Title)
	}
	if got := strings.Join(titles, ","); got != "Unterricht,Werkzeuge,Quizzes" {
		t.Fatalf("Regale = %s", got)
	}
	if shelves[0].Apps[0].Name != "Alpha" || shelves[0].Apps[1].Name != "beta" {
		t.Errorf("Apps im Regal nicht nach Name sortiert: %+v", shelves[0].Apps)
	}
	if !shelves[2].Last || shelves[0].Last {
		t.Error("nur das letzte Regal trägt die Kachel zum Katalog")
	}
}

func TestStartSentence(t *testing.T) {
	up := appSheet{Name: "A", Running: true}
	off := appSheet{Name: "B"}
	for _, tc := range []struct {
		needs, updates int
		apps           []appSheet
		head, summary  string
	}{
		{0, 0, nil, "Bereit für die erste App", "Noch keine Apps installiert"},
		{0, 0, []appSheet{up}, "Alles läuft", "Die App läuft"},
		{0, 2, []appSheet{up, up}, "Alles läuft", "Alle 2 Apps laufen · 2 Updates"},
		{1, 1, []appSheet{up, off}, "Eine Sache braucht dich", "1 von 2 Apps laufen · 1 Update"},
		{3, 0, []appSheet{off}, "3 Dinge brauchen dich", "0 von 1 Apps laufen"},
	} {
		head, summary := startSentence(tc.needs, tc.apps, tc.updates)
		if head != tc.head || summary != tc.summary {
			t.Errorf("startSentence(%d, %d Apps, %d) = %q / %q", tc.needs, len(tc.apps), tc.updates, head, summary)
		}
	}
}

func TestGigabytes(t *testing.T) {
	for n, want := range map[uint64]string{
		5_200_000_000:   "5,2",
		139_400_000_000: "139",
		0:               "0,0",
	} {
		if got := gigabytes(n); got != want {
			t.Errorf("gigabytes(%d) = %q, want %q", n, got, want)
		}
	}
}

// Rendert die KI-Seite mit allen drei Bereichen — fängt Feldfehler in den
// Blättern, die nur bei vorhandenen Einträgen ausgeführt werden.
func TestRenderLLMPage(t *testing.T) {
	s := &Server{}
	if err := s.loadTemplates(); err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}
	data := llmData{
		PageData:  PageData{NavActive: "llm", CSRFToken: "tok", LLMInstalled: true},
		Providers: []llm.Provider{{ID: "openai", Kind: "openai", BaseURL: "https://api.openai.com", APIKey: "sk"}},
		Personas:  []llm.Persona{{ID: "tutor", Provider: "openai", UpstreamID: "gpt", Prompt: "Hilf."}, {ID: "lokal"}},
		APIKeys:   []llm.APIKey{{ID: "open-webui", Prefix: "sk-ls", AllowedPersonas: []string{"tutor"}}},
		NewKey:    "sk-ls-neu", NewKeyID: "open-webui",
		ActiveTab: "keys",
	}
	rec := httptest.NewRecorder()
	s.render(rec, "llm.html.tmpl", data)
	if rec.Code != 200 {
		t.Fatalf("status = %d; body:\n%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-default="schluessel"`,
		`/llm/personas/tutor/update`,
		`/llm/personas/tutor/deactivate`,
		`/llm/providers/openai/key`,
		`/llm/keys/open-webui/delete`,
		`sk-ls-neu`,
		`<option value="openai" selected>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("KI-Seite ohne %q", want)
		}
	}
	if strings.Contains(body, "/llm/personas/lokal/deactivate") {
		t.Error("inaktives Modell lässt sich deaktivieren")
	}
}
