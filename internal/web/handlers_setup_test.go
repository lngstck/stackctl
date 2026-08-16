// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/preflight"
)

// Die drei Karten des Assistenten sind eine Oberflaechen-Gruppierung; im
// Config landen nur zwei Achsen. Dieser Test haelt die Abbildung fest — sie
// entscheidet ueber die Dex-Issuer-URL und jede Redirect-URI.
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
			name: "Betreiber-Relay leitet die Domain aus dem Slug ab",
			mode: preflight.ModeRelayOperator, baseDomain: "",
			wantTransport: config.TransportRelay, wantDomain: "phoenix.learningstack.online",
		},
		{
			// Wer erst eine Domain tippt und dann auf die erste Karte
			// zurueckwechselt, soll nicht heimlich die getippte behalten.
			name: "Betreiber-Relay ignoriert eine eingetippte Domain",
			mode: preflight.ModeRelayOperator, baseDomain: "ls.gym-phoenix.de",
			wantTransport: config.TransportRelay, wantDomain: "phoenix.learningstack.online",
		},
		{
			name: "eigene Domain am Relay bleibt Relay",
			mode: preflight.ModeRelayOwn, baseDomain: "ls.gym-phoenix.de",
			wantTransport: config.TransportRelay, wantDomain: "ls.gym-phoenix.de",
		},
		{
			name: "direkter Betrieb",
			mode: preflight.ModeDirect, baseDomain: "ls.gym-phoenix.de",
			wantTransport: config.TransportDirect, wantDomain: "ls.gym-phoenix.de",
		},
		{
			name: "eigene Domain ohne Domain",
			mode: preflight.ModeRelayOwn, baseDomain: "", wantErr: true,
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
			name: "unbekannte Betriebsart",
			mode: "brieftaube", baseDomain: "ls.gym-phoenix.de", wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport, domain, err := resolvePublicMode(tt.mode, tt.baseDomain, "phoenix")
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

// Der Endpunkt gehoert zum Setup-Formular und muss mit ihm schliessen —
// sonst bleibt eine Sonde offen, die jeder ohne Login ausloesen kann.
func TestSetupPreflightClosesWithSetup(t *testing.T) {
	s := &Server{cfg: &config.Config{SetupState: config.SetupStateReady}}

	rec := httptest.NewRecorder()
	s.handleSetupPreflight(rec, httptest.NewRequest("GET", "/setup/preflight?mode=relay_operator", nil))

	if rec.Code != 403 {
		t.Errorf("status = %d, want 403 nach abgeschlossenem Setup", rec.Code)
	}
}

func TestSetupPreflightReturnsChecks(t *testing.T) {
	s := &Server{cfg: &config.Config{SetupState: config.SetupStateNeedsSetup}}

	rec := httptest.NewRecorder()
	s.handleSetupPreflight(rec, httptest.NewRequest("GET", "/setup/preflight?mode=relay_operator", nil))

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
	if got.Summary != preflight.StatusOK {
		t.Errorf("summary = %q, want ok — beim Betreiber-Relay ist nichts vorzubereiten", got.Summary)
	}
	if len(got.Checks) == 0 {
		t.Error("keine Karten geliefert")
	}
}

// Eine ungueltige Domain darf nicht ins Netz gehen, sondern muss sofort als
// Fehler zurueckkommen.
func TestSetupPreflightRejectsInvalidDomain(t *testing.T) {
	s := &Server{cfg: &config.Config{SetupState: config.SetupStateNeedsSetup}}

	rec := httptest.NewRecorder()
	s.handleSetupPreflight(rec, httptest.NewRequest("GET", "/setup/preflight?mode=direct&base_domain=*.x.de", nil))

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

	for _, mode := range []string{preflight.ModeRelayOperator, preflight.ModeRelayOwn, preflight.ModeDirect} {
		t.Run(mode, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.render(rec, "setup.html.tmpl", setupData{
				Mode:       mode,
				BaseDomain: "ls.gym-phoenix.de",
				RootDomain: config.DefaultRootDomain,
			})
			if rec.Code != 200 {
				t.Fatalf("status = %d, want 200; body:\n%s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			for _, want := range []string{
				`name="public_mode"`,
				`value="relay_operator"`,
				`value="relay_own"`,
				`value="direct"`,
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
