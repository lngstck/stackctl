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

// Fehlende Pflicht-Dienste sind eine Aufgabe mit einem Knopf, nicht eine
// Karte pro Dienst.
func TestMissingInfraIssues(t *testing.T) {
	issues := missingInfraIssues(stateWith())
	if len(issues) != 1 {
		t.Fatalf("leerer State: %d Karten, want 1", len(issues))
	}
	if is := issues[0]; is.PostTo != "/system/infra" || is.ActionLabel == "" || is.Level == "danger" {
		t.Errorf("Karte = %+v", is)
	}

	got := missingInfraIssues(stateWith("postgres", "dex"))
	if len(got) != 1 || !strings.Contains(got[0].Detail, "Webzugang") || strings.Contains(got[0].Detail, "Datenbank") {
		t.Errorf("ohne Proxy: want eine Karte, die nur den Webzugang nennt, got %+v", got)
	}
	if got := missingInfraIssues(stateWith("postgres", "dex", "caddy")); len(got) != 0 {
		t.Errorf("alle installiert: want keine Karten, got %+v", got)
	}
	if ids := missingInfraIDs(stateWith("dex")); strings.Join(ids, ",") != "caddy,postgres" {
		t.Errorf("missingInfraIDs = %v, want Reihenfolge caddy,postgres", ids)
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

	data := startData{
		PageData: PageData{NavActive: "dashboard", SchoolName: "Musterschule", SchoolSlug: "musterschule", CSRFToken: "tok"},
		Sys:      sysView{},
	}
	for _, is := range missingInfraIssues(stateWith()) {
		data.Notices = append(data.Notices, noticeFor(is, nil, "tok"))
	}

	rec := httptest.NewRecorder()
	s.render(rec, "dashboard.html.tmpl", data)
	if rec.Code != 200 {
		t.Fatalf("render status = %d, want 200; body:\n%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// HasApps ist hier false (frisches System) — die Hinweise müssen TROTZDEM
	// erscheinen, zusammen mit dem "Noch keine Apps"-Hinweis.
	for _, want := range []string{
		"Noch keine Apps installiert",
		"Grundversorgung einrichten",
		`action="/system/infra"`,
		"Jetzt einrichten",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered dashboard missing %q", want)
		}
	}
}

// Rendert den Katalog mit einem nicht installierten Pflicht-Dienst — der
// Hinweis im Installations-Blatt muss erscheinen.
func TestRenderAppsWithMandatoryBadge(t *testing.T) {
	s := &Server{}
	if err := s.loadTemplates(); err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}

	postgres := catalogApp{
		ID: "postgres", Name: "PostgreSQL", Label: "Datenbank", Version: "1.3", IsMandatory: true,
		Tile: appTile{Face: faceFor("postgres", "PostgreSQL")}, Values: map[string]string{},
	}
	data := catalogData{
		PageData:  PageData{NavActive: "apps", SchoolName: "Musterschule", SchoolSlug: "musterschule", CSRFToken: "tok"},
		Shelves:   []catalogShelf{{Title: "Grundversorgung", Apps: []catalogApp{postgres}}},
		Apps:      []catalogApp{postgres},
		Available: 1,
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
