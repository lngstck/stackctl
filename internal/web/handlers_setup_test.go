// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/paths"
	"github.com/lngstck/stackctl/internal/preflight"
)

// Die Karten des Assistenten sind eine Oberflaechen-Gruppierung. Dieser Test
// haelt die Abbildung fest — sie entscheidet ueber die Dex-Issuer-URL und jede
// Redirect-URI.
func TestResolvePublicMode(t *testing.T) {
	tests := []struct {
		name          string
		mode          string
		baseDomain    string
		wantTransport string
		wantDomain    string
		wantErr       bool
	}{
		{
			name: "nur im Schulnetz",
			mode: preflight.ModeLocal, baseDomain: "ls.gym-phoenix.de",
			wantTransport: config.TransportLocal, wantDomain: "ls.gym-phoenix.de",
		},
		{
			name: "nur im Schulnetz ohne Domain",
			mode: preflight.ModeLocal, baseDomain: "", wantErr: true,
		},
		{
			name: "direkter Betrieb",
			mode: preflight.ModeDirect, baseDomain: "ls.gym-phoenix.de",
			wantTransport: config.TransportDirect, wantDomain: "ls.gym-phoenix.de",
		},
		{
			name: "direkter Betrieb ohne Domain",
			mode: preflight.ModeDirect, baseDomain: "", wantErr: true,
		},
		{
			name: "eingefuegter Wildcard-Eintrag",
			mode: preflight.ModeDirect, baseDomain: "*.ls.gym-phoenix.de", wantErr: true,
		},
		{
			name: "eingefuegte URL",
			mode: preflight.ModeDirect, baseDomain: "https://ls.gym-phoenix.de", wantErr: true,
		},
		{
			name: "keine Betriebsart gewaehlt",
			mode: "", baseDomain: "ls.gym-phoenix.de", wantErr: true,
		},
		{
			// Ein altes Formular oder ein Lesezeichen darf kein Relay mehr
			// anlegen.
			name: "Relay gibt es nicht mehr",
			mode: "relay_operator", baseDomain: "", wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport, domain, err := resolvePublicMode(tt.mode, tt.baseDomain)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got transport=%q domain=%q", transport, domain)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if transport != tt.wantTransport {
				t.Errorf("transport = %q, want %q", transport, tt.wantTransport)
			}
			if domain != tt.wantDomain {
				t.Errorf("base domain = %q, want %q", domain, tt.wantDomain)
			}
		})
	}
}

// Nach dem Setup ist die Installation sofort bereit: kein Paket an einen
// Betreiber, kein Warten auf eine Freischaltung, kein SSH-Schluessel.
func TestSetupGoesStraightToReady(t *testing.T) {
	s, _ := testServerWithPublisher(t, &fakePublisher{})
	s.cfg = config.Default()
	if err := s.loadTemplates(); err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}

	form := url.Values{
		"setup_code":       {testSetupCode},
		"school_name":      {"Gymnasium Phoenix"},
		"school_slug":      {"phoenix"},
		"server_domain":    {"192.168.1.10"},
		"contact_email":    {"it@gym-phoenix.de"},
		"public_mode":      {preflight.ModeDirect},
		"base_domain":      {"ls.gym-phoenix.de"},
		"password":         {"geheim-genug"},
		"password_confirm": {"geheim-genug"},
	}
	req := httptest.NewRequest(http.MethodPost, "/setup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.handleSetupPost(rec, req)

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("status = %d, Location = %q; want 303 → /login\n%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}

	saved, err := config.Load()
	if err != nil {
		t.Fatalf("config not saved: %v", err)
	}
	if saved.SetupState != config.SetupStateReady {
		t.Errorf("SetupState = %q, want ready", saved.SetupState)
	}
	if saved.Public.BaseDomain != "ls.gym-phoenix.de" || saved.Public.Transport != config.TransportDirect {
		t.Errorf("public = %+v", saved.Public)
	}
	if err := saved.Validate(); err != nil {
		t.Errorf("gespeicherte Config ist ungueltig: %v", err)
	}

	entries, err := os.ReadDir(paths.ConfigDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "registration-") || strings.HasPrefix(e.Name(), "tunnel_key") {
			t.Errorf("Setup hat %s angelegt", e.Name())
		}
	}
}

func postSetup(t *testing.T, s *Server, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/setup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.handleSetupPost(rec, req)
	return rec
}

func localSetupForm() url.Values {
	return url.Values{
		"setup_code":       {testSetupCode},
		"school_name":      {"Gymnasium Phoenix"},
		"school_slug":      {"phoenix"},
		"server_domain":    {"192.168.1.10"},
		"contact_email":    {"it@gym-phoenix.de"},
		"public_mode":      {preflight.ModeLocal},
		"base_domain":      {"ls.gym-phoenix.de"},
		"dns_token":        {" geheim "},
		"challenge_domain": {"_ACME-challenge.gym-phoenix.dedyn.io."},
		"password":         {"geheim-genug"},
		"password_confirm": {"geheim-genug"},
	}
}

// Der Standard: nur im Schulnetz, Zertifikat per DNS-01. Der Token landet in
// der Config und in der .env des Proxys, die Delegation wird normalisiert.
func TestSetupLocalStoresCertificateSettings(t *testing.T) {
	s, _ := testServerWithPublisher(t, &fakePublisher{})
	s.cfg = config.Default()
	if err := s.loadTemplates(); err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}

	rec := postSetup(t, s, localSetupForm())
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d; body:\n%s", rec.Code, rec.Body.String())
	}

	saved, err := config.Load()
	if err != nil {
		t.Fatalf("config not saved: %v", err)
	}
	if saved.Public.Transport != config.TransportLocal {
		t.Errorf("Transport = %q, want local", saved.Public.Transport)
	}
	want := config.PublicLocal{DNSToken: "geheim", ChallengeDomain: "_acme-challenge.gym-phoenix.dedyn.io"}
	if saved.Public.Local != want {
		t.Errorf("Local = %+v, want %+v", saved.Public.Local, want)
	}
	if saved.Public.ACMEEmail != "it@gym-phoenix.de" {
		t.Errorf("ACMEEmail = %q, want die Kontakt-Adresse", saved.Public.ACMEEmail)
	}

	env, err := os.ReadFile(paths.EnvFile())
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	if !strings.Contains(string(env), "DESEC_TOKEN=geheim") {
		t.Errorf(".env ohne DESEC_TOKEN:\n%s", env)
	}
}

// Ohne Token kein Zertifikat, ohne Zertifikat kein Login: das Setup haelt an,
// statt eine Installation anzulegen, die nicht funktionieren kann.
func TestSetupLocalRequiresToken(t *testing.T) {
	s, _ := testServerWithPublisher(t, &fakePublisher{})
	s.cfg = config.Default()
	if err := s.loadTemplates(); err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}

	form := localSetupForm()
	form.Set("dns_token", "")
	rec := postSetup(t, s, form)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "deSEC-Token") {
		t.Errorf("status = %d, want das Formular mit Fehler zum Token", rec.Code)
	}
	if s.cfg.SetupState != config.SetupStateNeedsSetup {
		t.Errorf("SetupState = %q, want needs_setup", s.cfg.SetupState)
	}
}

// Der Endpunkt gehoert zum Setup-Formular und muss mit ihm schliessen —
// sonst bleibt eine Sonde offen, die jeder ohne Login ausloesen kann.
func TestSetupPreflightClosesWithSetup(t *testing.T) {
	s := &Server{cfg: &config.Config{SetupState: config.SetupStateReady}}

	rec := httptest.NewRecorder()
	s.handleSetupPreflight(rec, httptest.NewRequest("GET", "/setup/preflight?mode=direct&base_domain=*.x.de", nil))

	if rec.Code != 403 {
		t.Errorf("status = %d, want 403 nach abgeschlossenem Setup", rec.Code)
	}
}

// offlineProber answers every question with "nothing there", so handler
// tests never touch DNS, privileged ports or deSEC.
func offlineProber(t *testing.T) {
	t.Helper()
	orig := newProber
	newProber = func() *preflight.Prober {
		p := preflight.NewProber()
		p.Resolver = noDNS{}
		p.PublicResolver = nil
		p.PortFree = func(int) (bool, error) { return true, nil }
		p.CNAME = nil
		p.DNSZones = func(context.Context, string) ([]string, error) { return nil, nil }
		return p
	}
	t.Cleanup(func() { newProber = orig })
}

type noDNS struct{}

func (noDNS) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return nil, errors.New("no such host")
}

func TestSetupPreflightReturnsChecks(t *testing.T) {
	offlineProber(t)
	s := &Server{cfg: &config.Config{SetupState: config.SetupStateNeedsSetup}, setupCode: testSetupCode}

	rec := httptest.NewRecorder()
	s.handleSetupPreflight(rec, httptest.NewRequest("GET", "/setup/preflight?setup_code="+testSetupCode+"&mode=direct&base_domain=ls.gym-phoenix.de", nil))

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got struct {
		Summary string            `json:"summary"`
		Checks  []preflight.Check `json:"checks"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Checks) == 0 {
		t.Error("keine Karten geliefert")
	}
}

// Eine ungueltige Domain darf nicht ins Netz gehen, sondern muss sofort als
// Fehler zurueckkommen.
func TestSetupPreflightRejectsInvalidDomain(t *testing.T) {
	s := &Server{cfg: &config.Config{SetupState: config.SetupStateNeedsSetup}, setupCode: testSetupCode}

	rec := httptest.NewRecorder()
	s.handleSetupPreflight(rec, httptest.NewRequest("GET", "/setup/preflight?setup_code="+testSetupCode+"&mode=direct&base_domain=*.x.de", nil))

	var got struct {
		Summary string `json:"summary"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Summary != preflight.StatusFail {
		t.Errorf("summary = %q, want fail", got.Summary)
	}
}

// Rendert das Setup-Formular — faengt Template-Feldfehler, die die reinen
// Logik-Tests nicht sehen.
func TestRenderSetupTemplate(t *testing.T) {
	s := &Server{}
	if err := s.loadTemplates(); err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}

	for _, mode := range []string{preflight.ModeLocal, preflight.ModeDirect} {
		t.Run(mode, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.render(rec, "setup.html.tmpl", setupData{
				Mode:       mode,
				BaseDomain: "ls.gym-phoenix.de",
			})
			if rec.Code != 200 {
				t.Fatalf("status = %d, want 200; body:\n%s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			for _, want := range []string{
				`name="public_mode"`,
				`value="local"`,
				`value="direct"`,
				`name="dns_token"`,
				`name="challenge_domain"`,
				`name="base_domain"`,
				`id="check-btn"`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("Formular ohne %q", want)
				}
			}
			// Genau eine Karte ist vorausgewählt — zwei checked-Radios wären
			// ein stiller Zustandsfehler im Formular. Gezählt wird das
			// Attribut am Tag-Ende, nicht das Wort: das JS liest ebenfalls
			// el.checked.
			if got := strings.Count(body, "checked>"); got != 1 {
				t.Errorf("vorausgewählte Karten = %d, want 1", got)
			}
		})
	}
}

// Ein leeres Token-Feld in den Einstellungen behaelt den gespeicherten Token —
// er wird nie an den Browser zurueckgegeben. Eine geaenderte Delegation geht
// sofort an den Proxy.
func TestSettingsKeepTokenAndRefreshProxy(t *testing.T) {
	fake := &fakePublisher{}
	s, _ := testServerWithPublisher(t, fake)
	s.cfg.SetupState = config.SetupStateReady
	s.cfg.School.Name = "Phoenix"
	s.cfg.Public.Local = config.PublicLocal{DNSToken: "alt", ChallengeDomain: "_acme-challenge.alt.dedyn.io"}
	if err := s.loadTemplates(); err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}

	form := url.Values{
		"school_name":      {"Phoenix"},
		"dns_token":        {""},
		"challenge_domain": {"_acme-challenge.neu.dedyn.io"},
	}
	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.handleSettingsPost(rec, req)

	if got := s.cfg.Public.Local; got.DNSToken != "alt" || got.ChallengeDomain != "_acme-challenge.neu.dedyn.io" {
		t.Errorf("Local = %+v", got)
	}
	if fake.refreshed != 1 {
		t.Errorf("Refresh-Aufrufe = %d, want 1", fake.refreshed)
	}
	if strings.Contains(rec.Body.String(), "alt\"") {
		t.Error("gespeicherter Token steht im Formular")
	}
}
