package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/paths"
)

func TestLinkifyAdminNotes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string // substrings that must appear
		not  []string // substrings that must NOT appear
	}{
		{
			name: "plain text bleibt escaped",
			in:   "Kein Link, aber <b>Markup</b> & Sonderzeichen.",
			want: []string{"&lt;b&gt;Markup&lt;/b&gt;", "&amp;"},
			not:  []string{"<b>"},
		},
		{
			name: "URL wird zum Anker",
			in:   "Anleitung unter:\nhttps://sponsorenlauf.schule.example/admin\nDanach Token eingeben.",
			want: []string{
				`<a href="https://sponsorenlauf.schule.example/admin" target="_blank" rel="noopener">https://sponsorenlauf.schule.example/admin</a>`,
				"Danach Token eingeben.",
			},
		},
		{
			name: "Satzzeichen am Ende bleibt draussen",
			in:   "Siehe https://example.org/docs.",
			want: []string{`href="https://example.org/docs"`, "</a>."},
			not:  []string{`href="https://example.org/docs."`},
		},
		{
			name: "Injection ueber URL-Umgebung unmoeglich",
			in:   `<script>x</script> https://example.org/?a=1&b=2 <img src=x>`,
			want: []string{"&lt;script&gt;", `href="https://example.org/?a=1&amp;b=2"`, "&lt;img"},
			not:  []string{"<script>", "<img"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(linkifyAdminNotes(tc.in))
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("Ergebnis enthaelt %q nicht:\n%s", w, got)
				}
			}
			for _, n := range tc.not {
				if strings.Contains(got, n) {
					t.Errorf("Ergebnis enthaelt unerwartet %q:\n%s", n, got)
				}
			}
		})
	}

	if linkifyAdminNotes("") != "" {
		t.Error("leerer Input muss leer bleiben")
	}
}

// Ohne Katalog-Index fehlt nur das Angebot. Installierte Apps und die
// Meldung der Aktion, die hierher umgeleitet hat, muessen trotzdem erscheinen
// — vorher brach die Seite ab und zeigte nur "Katalog nicht geladen".
func TestAppsPageWithoutCatalogKeepsInstalledAppsAndMessage(t *testing.T) {
	s, st := testServerWithPublisher(t, &fakePublisher{})
	st.Containers["sponsorenlauf"] = &config.ContainerState{ID: "sponsorenlauf", Name: "Sponsorenlauf"}
	if err := s.loadTemplates(); err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}

	rec := httptest.NewRecorder()
	s.handleApps(rec, httptest.NewRequest(http.MethodGet, "/apps?msg=pylearn+gestartet", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	body := rec.Body.String()
	for _, want := range []string{
		"PyLearn",
		"Sponsorenlauf",
		"pylearn gestartet",
		"Katalog ist noch nicht geladen",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Seite enthaelt %q nicht", want)
		}
	}
	if strings.Contains(body, "Alle Apps sind bereits installiert") {
		t.Error("ohne Katalog behauptet die Seite, alles sei installiert")
	}
}

func writeDefinition(t *testing.T, id, yaml string) {
	t.Helper()
	if err := os.MkdirAll(paths.CatalogContainersDir(), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.AppDefinitionFile(id), []byte(yaml), 0o640); err != nil {
		t.Fatal(err)
	}
}

// Im Schulnetz bekommt eine neue App ihre Adresse sofort: das ist kein
// Internetzugang, und ohne Adresse funktioniert kein Login.
func TestAutoPublishInSchoolNetwork(t *testing.T) {
	fake := &fakePublisher{host: "pylearn.ls.gym-phoenix.de"}
	s, _ := testServerWithPublisher(t, fake)
	writeDefinition(t, "pylearn", "id: pylearn\nname: PyLearn\nports:\n  - host: 8330\n    container: 8000\n")

	working := s.snapState()
	if msg := s.autoPublish(working, "pylearn"); msg != "" {
		t.Fatalf("unerwartete Meldung: %q", msg)
	}
	cs := working.Containers["pylearn"]
	if !cs.PublicEnabled || cs.PublicHost != fake.host {
		t.Errorf("state = %+v, want veroeffentlicht unter %s", cs, fake.host)
	}
	if len(fake.enabled) != 1 || fake.enabled[0].ContainerPort != 8000 {
		t.Errorf("Enable-Aufrufe = %+v, want einen mit Container-Port 8000", fake.enabled)
	}
}

// Im direkten Betrieb hiesse "automatisch" ins Internet stellen — das bleibt
// eine bewusste Entscheidung. Pflicht-Dienste bekommen nie eine eigene Adresse.
func TestAutoPublishLeavesOtherCasesAlone(t *testing.T) {
	fake := &fakePublisher{host: "x"}
	s, st := testServerWithPublisher(t, fake)
	writeDefinition(t, "pylearn", "id: pylearn\nname: PyLearn\nports:\n  - host: 8330\n    container: 8000\n")
	st.Containers["postgres"] = &config.ContainerState{ID: "postgres", Ports: []int{5432}}
	writeDefinition(t, "postgres", "id: postgres\nname: PostgreSQL\nports:\n  - host: 5432\n    container: 5432\n")

	working := s.snapState()
	s.autoPublish(working, "postgres")

	s.cfg.Public.Transport = config.TransportDirect
	s.autoPublish(working, "pylearn")

	if len(fake.enabled) != 0 {
		t.Errorf("Enable aufgerufen: %+v", fake.enabled)
	}
	if working.Containers["pylearn"].PublicEnabled || working.Containers["postgres"].PublicEnabled {
		t.Error("state wurde veraendert")
	}
}

// Scheitert die Route, bleibt die App installiert und der Job sagt, wo es
// weitergeht — ohne den Zustand zu beschoenigen.
func TestAutoPublishFailureIsReportedNotRecorded(t *testing.T) {
	s, _ := testServerWithPublisher(t, &fakePublisher{enableErr: errFake})
	writeDefinition(t, "pylearn", "id: pylearn\nname: PyLearn\nports:\n  - host: 8330\n    container: 8000\n")

	working := s.snapState()
	msg := s.autoPublish(working, "pylearn")
	if msg == "" {
		t.Error("Fehlschlag ohne Meldung")
	}
	if cs := working.Containers["pylearn"]; cs.PublicEnabled || cs.PublicHost != "" {
		t.Errorf("state = %+v, want unveraendert", cs)
	}
}
