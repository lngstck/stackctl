// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lngstck/stackctl/internal/config"
)

func stateWith(ids ...string) *config.State {
	st := &config.State{Containers: map[string]*config.ContainerState{}}
	for _, id := range ids {
		st.Containers[id] = &config.ContainerState{ID: id}
	}
	return st
}

// Frisches System: alle Pflicht-Dienste fehlen → je eine danger-Karte in
// Installations-Reihenfolge, mit Direktlink aufs Install-Formular.
func TestMissingInfraIssues(t *testing.T) {
	issues := missingInfraIssues(stateWith())
	want := []string{"/apps/postgres/install", "/apps/dex/install", "/apps/caddy/install"}
	if len(issues) != len(want) {
		t.Fatalf("issues auf leerem State: want %d, got %d", len(want), len(issues))
	}
	for i, action := range want {
		if issues[i].Action != action {
			t.Errorf("Karte %d: Action = %q, want %q", i, issues[i].Action, action)
		}
	}
	for _, is := range issues {
		if is.Level != "danger" {
			t.Errorf("Level: want danger, got %q (%s)", is.Level, is.Title)
		}
		if is.ActionLabel == "" || is.Detail == "" {
			t.Errorf("Karte %q ohne Detail/ActionLabel", is.Title)
		}
	}

	if got := missingInfraIssues(stateWith("postgres", "dex")); len(got) != 1 || got[0].Action != "/apps/caddy/install" {
		t.Errorf("ohne Proxy: want genau die caddy-Karte, got %+v", got)
	}
	if got := missingInfraIssues(stateWith("postgres", "dex", "caddy")); len(got) != 0 {
		t.Errorf("alle installiert: want keine Karten, got %+v", got)
	}
}

// Der Reverse-Proxy ist der einzige Weg zu Login und Apps — ohne ihn ist
// keine Adresse erreichbar. Er ist deshalb Pflicht wie Datenbank und Dex.
func TestMandatoryServices(t *testing.T) {
	for _, id := range []string{"postgres", "dex", "caddy"} {
		if !isMandatoryApp(id) {
			t.Errorf("%s muss Pflicht sein", id)
		}
	}
	if isMandatoryApp("pylearn") {
		t.Error("eine normale App ist kein Pflicht-Dienst")
	}
}

// Nicht installierte Pflicht-Dienste werden in den App-Listen nach vorn
// gezogen; sonst bleibt die Katalog-Reihenfolge stabil erhalten.
func TestPinMandatoryFirst(t *testing.T) {
	entries := []appListEntry{
		{ID: "open-webui"},
		{ID: "pylearn"},
		{ID: "postgres", IsMandatory: true},
		{ID: "dex", IsMandatory: true},
	}
	pinMandatoryFirst(entries)
	got := []string{entries[0].ID, entries[1].ID, entries[2].ID, entries[3].ID}
	want := []string{"postgres", "dex", "open-webui", "pylearn"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Reihenfolge: want %v, got %v", want, got)
		}
	}

	// Installierte Pflicht-Dienste werden NICHT mehr gepinnt.
	entries = []appListEntry{
		{ID: "open-webui"},
		{ID: "postgres", IsMandatory: true, IsInstalled: true},
	}
	pinMandatoryFirst(entries)
	if entries[0].ID != "open-webui" {
		t.Fatalf("installierter Pflicht-Dienst darf nicht gepinnt werden, got %q zuerst", entries[0].ID)
	}
}

// Rendert das Dashboard mit den Karten für fehlende Pflicht-Dienste — fängt
// Template-Feldfehler, die reine Logik-Tests nicht sehen.
func TestRenderDashboardWithMissingInfra(t *testing.T) {
	s := &Server{}
	if err := s.loadTemplates(); err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}

	data := dashboardData{
		PageData: PageData{NavActive: "dashboard", SchoolName: "Musterschule", SchoolSlug: "musterschule", CSRFToken: "tok"},
		Issues:   missingInfraIssues(stateWith()),
		Sys:      sysView{},
	}

	rec := httptest.NewRecorder()
	s.render(rec, "dashboard.html.tmpl", data)
	if rec.Code != 200 {
		t.Fatalf("render status = %d, want 200; body:\n%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// HasApps ist hier false (frisches System) — die Karten müssen TROTZDEM
	// erscheinen, zusammen mit dem "Noch keine Apps"-Hinweis.
	for _, want := range []string{
		"Noch keine Apps installiert",
		"noch nicht installiert",
		"/apps/postgres/install",
		"/apps/dex/install",
		"Jetzt installieren",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered dashboard missing %q", want)
		}
	}
}

// Rendert die Apps-Übersicht mit einem nicht installierten Pflicht-Dienst —
// der Hinweis-Badge muss erscheinen.
func TestRenderAppsWithMandatoryBadge(t *testing.T) {
	s := &Server{}
	if err := s.loadTemplates(); err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}

	data := appsData{
		PageData: PageData{NavActive: "apps", SchoolName: "Musterschule", SchoolSlug: "musterschule", CSRFToken: "tok"},
		All: []appListEntry{
			{ID: "postgres", Name: "PostgreSQL", IsMandatory: true},
			{ID: "pylearn", Name: "PyLearn"},
		},
		Available: []appListEntry{
			{ID: "postgres", Name: "PostgreSQL", IsMandatory: true},
		},
	}

	rec := httptest.NewRecorder()
	s.render(rec, "apps.html.tmpl", data)
	if rec.Code != 200 {
		t.Fatalf("render status = %d, want 200; body:\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Pflicht-Dienst — zuerst installieren") {
		t.Error("rendered apps page missing mandatory badge")
	}
}

// Die Karten "laeuft nicht" kommen aus einer Map. Ohne Sortierung tauschten
// sie bei jedem Laden die Plaetze — wer ein Problem gerade angesehen hat,
// fand es beim naechsten Laden woanders.
func TestDashboardIssuesKeepTheirOrder(t *testing.T) {
	s, st := testServerWithPublisher(t, &fakePublisher{})
	for _, id := range []string{"zeta", "alpha", "mitte", "beta", "omega"} {
		st.Containers[id] = &config.ContainerState{ID: id, Name: id}
	}
	s.jobs = newJobStore()
	if err := s.loadTemplates(); err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}

	order := func() []int {
		rec := httptest.NewRecorder()
		s.handleDashboard(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		body := rec.Body.String()
		var pos []int
		for _, name := range []string{"alpha", "beta", "mitte", "omega", "PyLearn", "zeta"} {
			pos = append(pos, strings.Index(body, name+" läuft nicht"))
		}
		return pos
	}

	first := order()
	for i := 1; i < len(first); i++ {
		if first[i-1] < 0 || first[i] < first[i-1] {
			t.Fatalf("Karten nicht alphabetisch nach ID: Positionen %v", first)
		}
	}
	for i := 0; i < 5; i++ {
		if got := order(); !equalInts(got, first) {
			t.Fatalf("Reihenfolge wechselt zwischen zwei Aufrufen: %v vs. %v", first, got)
		}
	}
}

func equalInts(a, b []int) bool {
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
