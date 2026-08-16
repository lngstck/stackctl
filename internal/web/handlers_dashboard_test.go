// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
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

// relayCfg / directCfg unterscheiden nur die Betriebsart — daran haengt,
// welche Dienste Pflicht sind.
func relayCfg() *config.Config {
	return &config.Config{Public: config.Public{Transport: config.TransportRelay}}
}

func directCfg() *config.Config {
	return &config.Config{Public: config.Public{Transport: config.TransportDirect}}
}

// Frisch freigeschaltetes System: beide Pflicht-Dienste fehlen → zwei
// danger-Karten, postgres zuerst (Installations-Reihenfolge), mit Direktlink
// aufs Install-Formular.
func TestMissingInfraIssues(t *testing.T) {
	cfg := relayCfg()
	issues := missingInfraIssues(cfg, stateWith())
	if len(issues) != 2 {
		t.Fatalf("issues auf leerem State: want 2, got %d", len(issues))
	}
	if issues[0].Action != "/apps/postgres/install" {
		t.Errorf("erste Karte muss postgres sein, Action = %q", issues[0].Action)
	}
	if issues[1].Action != "/apps/dex/install" {
		t.Errorf("zweite Karte muss dex sein, Action = %q", issues[1].Action)
	}
	for _, is := range issues {
		if is.Level != "danger" {
			t.Errorf("Level: want danger, got %q (%s)", is.Level, is.Title)
		}
		if is.ActionLabel == "" || is.Detail == "" {
			t.Errorf("Karte %q ohne Detail/ActionLabel", is.Title)
		}
	}

	if got := missingInfraIssues(cfg, stateWith("postgres")); len(got) != 1 || got[0].Action != "/apps/dex/install" {
		t.Errorf("nur postgres installiert: want genau die dex-Karte, got %+v", got)
	}
	if got := missingInfraIssues(cfg, stateWith("postgres", "dex")); len(got) != 0 {
		t.Errorf("beide installiert: want keine Karten, got %+v", got)
	}
}

// Im direkten Betrieb ist der Reverse-Proxy der oeffentliche Zugang: fehlt er,
// ist keine Adresse erreichbar — auch der Login nicht. Ueber einen Relay-
// Tunnel erledigt das die Gegenstelle, dort waere die Karte ein Fehlalarm.
func TestMandatoryServicesDependOnTransport(t *testing.T) {
	if got := missingInfraIssues(directCfg(), stateWith("postgres", "dex")); len(got) != 1 ||
		got[0].Action != "/apps/caddy/install" {
		t.Errorf("direkter Betrieb ohne Proxy: want caddy-Karte, got %+v", got)
	}
	if got := missingInfraIssues(relayCfg(), stateWith("postgres", "dex")); len(got) != 0 {
		t.Errorf("Relay-Betrieb: Caddy ist kein Pflicht-Dienst, got %+v", got)
	}

	if !isMandatoryApp(directCfg(), "caddy") {
		t.Error("caddy muss im direkten Betrieb Pflicht sein")
	}
	if isMandatoryApp(relayCfg(), "caddy") {
		t.Error("caddy darf im Relay-Betrieb nicht als Pflicht markiert sein")
	}
	// Datenbank und Login sind in jeder Betriebsart Pflicht.
	for _, cfg := range []*config.Config{relayCfg(), directCfg()} {
		for _, id := range []string{"postgres", "dex"} {
			if !isMandatoryApp(cfg, id) {
				t.Errorf("%s muss immer Pflicht sein (transport=%s)", id, cfg.Public.Transport)
			}
		}
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
		Issues:   missingInfraIssues(relayCfg(), stateWith()),
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
