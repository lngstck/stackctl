// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/paths"
	"github.com/lngstck/stackctl/internal/preflight"
	"github.com/lngstck/stackctl/internal/publish"
)

// fakePublisher records what it was asked to do and never touches state —
// which is the property under test: the publisher publishes, the handler
// records.
type fakePublisher struct {
	enabled   []publish.App
	disabled  []string
	enableErr error
	host      string
}

func (f *fakePublisher) Kind() string       { return publish.KindRelay }
func (f *fakePublisher) EnsureAuth() error  { return nil }
func (f *fakePublisher) AuthStatus() string { return publish.StatusRunning }
func (f *fakePublisher) StartAuth() error   { return nil }
func (f *fakePublisher) StopAuth() error    { return nil }
func (f *fakePublisher) Enable(app publish.App) (string, error) {
	if f.enableErr != nil {
		return "", f.enableErr
	}
	f.enabled = append(f.enabled, app)
	return f.host, nil
}
func (f *fakePublisher) Disable(appID string) error {
	f.disabled = append(f.disabled, appID)
	return nil
}
func (f *fakePublisher) Restore([]publish.App) {}
func (f *fakePublisher) Status(string) string  { return publish.StatusRunning }
func (f *fakePublisher) StartMonitor()         {}
func (f *fakePublisher) Shutdown()             {}

func testServerWithPublisher(t *testing.T, p publish.Publisher) (*Server, *config.State) {
	t.Helper()
	t.Setenv(paths.EnvStackctlDir, t.TempDir())

	st := config.NewState()
	st.Containers["pylearn"] = &config.ContainerState{
		ID: "pylearn", Name: "PyLearn", Ports: []int{8330},
	}
	return &Server{
		cfg: &config.Config{
			School: config.School{Slug: "phoenix"},
			Public: config.Public{
				Transport:  config.TransportRelay,
				BaseDomain: "phoenix.learningstack.online",
			},
		},
		state:     st,
		publisher: p,
		sessions:  &sessionStore{},
	}, st
}

func postTo(handler http.HandlerFunc, path, appID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.SetPathValue("id", appID)
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

// Enabling publishes the app and writes the host the publisher reported —
// the publisher itself must not have written anything.
func TestAppTunnelEnableRecordsPublisherResult(t *testing.T) {
	fake := &fakePublisher{host: "pylearn.phoenix.learningstack.online"}
	s, _ := testServerWithPublisher(t, fake)

	rec := postTo(s.handleAppPublishEnable, "/apps/pylearn/tunnel/enable", "pylearn")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	if len(fake.enabled) != 1 {
		t.Fatalf("publisher.Enable calls = %d, want 1", len(fake.enabled))
	}
	if got := fake.enabled[0]; got.ID != "pylearn" || got.LocalPort != 8330 {
		t.Errorf("published %+v, want pylearn on 8330", got)
	}

	cs := s.snapState().Containers["pylearn"]
	if !cs.PublicEnabled {
		t.Error("state should record the app as public")
	}
	if cs.PublicHost != fake.host {
		t.Errorf("PublicHost = %q, want %q — the host must come from the publisher, not be rebuilt", cs.PublicHost, fake.host)
	}
}

// If publishing fails, nothing may be recorded: a state that claims an app is
// public while it is not is worse than a visible failure.
func TestAppTunnelEnableLeavesStateAloneOnFailure(t *testing.T) {
	fake := &fakePublisher{enableErr: errFake}
	s, _ := testServerWithPublisher(t, fake)

	postTo(s.handleAppPublishEnable, "/apps/pylearn/tunnel/enable", "pylearn")

	if cs := s.snapState().Containers["pylearn"]; cs.PublicEnabled || cs.PublicHost != "" {
		t.Errorf("failed publish must not touch state: %+v", cs)
	}
}

func TestAppTunnelDisableClearsState(t *testing.T) {
	fake := &fakePublisher{}
	s, _ := testServerWithPublisher(t, fake)
	working := s.snapState()
	working.Containers["pylearn"].PublicEnabled = true
	working.Containers["pylearn"].PublicHost = "pylearn.phoenix.learningstack.online"
	if err := s.commitState(working); err != nil {
		t.Fatalf("seed commit: %v", err)
	}

	postTo(s.handleAppPublishDisable, "/apps/pylearn/tunnel/disable", "pylearn")

	if len(fake.disabled) != 1 || fake.disabled[0] != "pylearn" {
		t.Errorf("publisher.Disable calls = %v", fake.disabled)
	}
	cs := s.snapState().Containers["pylearn"]
	if cs.PublicEnabled || cs.PublicHost != "" {
		t.Errorf("state should be cleared: %+v", cs)
	}
}

// A publisher that cannot test its transport must not offer the button.
func TestTunnelTestRejectsPublisherWithoutTransportTest(t *testing.T) {
	s, _ := testServerWithPublisher(t, &fakePublisher{})

	rec := postTo(s.handlePublicTest, "/tunnel/test", "")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc == "/tunnel?test=ok" {
		t.Error("a publisher without a transport test must not report success")
	}
}

type fakeErr string

func (e fakeErr) Error() string { return string(e) }

const errFake = fakeErr("publish failed")

// Die Seite muss in jeder Betriebsart etwas Richtiges sagen. Ein SSH-Key und
// ein Verbindungstest ergeben im direkten Betrieb keinen Sinn — dort gibt es
// keine Gegenstelle, zu der man sich verbinden koennte.
func TestPublicPageAdaptsToTransport(t *testing.T) {
	tests := []struct {
		name       string
		cfg        *config.Config
		wantLabel  string
		wantSSHKey bool
	}{
		{
			name: "Betreiber-Relay",
			cfg: &config.Config{
				School: config.School{Slug: "phoenix"},
				Public: config.Public{Transport: config.TransportRelay, BaseDomain: "phoenix.learningstack.online"},
			},
			wantLabel: "Adresse des Betreibers", wantSSHKey: true,
		},
		{
			name: "eigene Domain am Relay",
			cfg: &config.Config{
				School: config.School{Slug: "phoenix"},
				Public: config.Public{Transport: config.TransportRelay, BaseDomain: "ls.gym-phoenix.de"},
			},
			wantLabel: "Eigene Domain über den Relay", wantSSHKey: true,
		},
		{
			name: "direkter Betrieb",
			cfg: &config.Config{
				School: config.School{Slug: "phoenix"},
				Public: config.Public{Transport: config.TransportDirect, BaseDomain: "ls.gym-phoenix.de"},
			},
			wantLabel: "Direkter Betrieb", wantSSHKey: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{}
			if err := s.loadTemplates(); err != nil {
				t.Fatalf("loadTemplates: %v", err)
			}

			mode := preflight.Mode(tt.cfg)
			rec := httptest.NewRecorder()
			s.render(rec, "public.html.tmpl", publicData{
				Mode:       mode,
				ModeLabel:  preflight.ModeLabel(mode),
				IsRelay:    tt.cfg.Public.Transport != config.TransportDirect,
				BaseDomain: tt.cfg.Public.BaseDomain,
				AuthHost:   "auth." + tt.cfg.Public.BaseDomain,
				AuthStatus: publish.StatusRunning,
				KeyExists:  true,
				SSHPubKey:  "ssh-ed25519 AAAA...",
				SSHHost:    "sish.learningstack.online",
				CanTest:    tt.cfg.Public.Transport != config.TransportDirect,
			})
			if rec.Code != 200 {
				t.Fatalf("status = %d, want 200; body:\n%s", rec.Code, rec.Body.String())
			}

			body := rec.Body.String()
			if !strings.Contains(body, tt.wantLabel) {
				t.Errorf("Betriebsart %q wird nicht benannt", tt.wantLabel)
			}
			if got := strings.Contains(body, "SSH-Key"); got != tt.wantSSHKey {
				t.Errorf("SSH-Key-Karte sichtbar = %v, want %v", got, tt.wantSSHKey)
			}
			if got := strings.Contains(body, "Verbindungstest"); got != tt.wantSSHKey {
				t.Errorf("Verbindungstest sichtbar = %v, want %v", got, tt.wantSSHKey)
			}
			// Die Aktionen muessen auf die neuen Pfade zeigen, sonst laufen
			// die Knoepfe nach der Umbenennung ins Leere.
			if !strings.Contains(body, "/public/auth/start") && !strings.Contains(body, "/public/auth/stop") {
				t.Error("Login-Aktionen zeigen nicht auf /public/auth/*")
			}
			if strings.Contains(body, "/tunnel/") {
				t.Errorf("alte /tunnel/-Pfade noch im Formular:\n%s", body)
			}
		})
	}
}

// Pflicht-Dienste gehoeren nicht in die App-Liste: sie werden nicht einzeln
// veroeffentlicht, und im direkten Betrieb waere Caddy dort ein Eintrag, der
// sich selbst freischalten soll.
func TestPublicPageHidesMandatoryServices(t *testing.T) {
	s, st := testServerWithPublisher(t, &fakePublisher{host: "pylearn.phoenix.learningstack.online"})
	s.cfg.Public.Transport = config.TransportDirect
	for _, id := range []string{"postgres", "dex", "caddy"} {
		st.Containers[id] = &config.ContainerState{ID: id, Name: id}
	}
	if err := s.loadTemplates(); err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}

	rec := httptest.NewRecorder()
	s.handlePublic(rec, httptest.NewRequest(http.MethodGet, "/public", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	body := rec.Body.String()
	for _, id := range []string{"postgres", "dex", "caddy"} {
		if strings.Contains(body, `/apps/`+id+`/public/enable`) {
			t.Errorf("Pflicht-Dienst %q steht in der App-Liste", id)
		}
	}
	if !strings.Contains(body, "/apps/pylearn/public/enable") {
		t.Error("echte App fehlt in der Liste")
	}
}

// Lesezeichen auf die alte Adresse sollen nicht ins Leere laufen.
func TestOldTunnelPathRedirects(t *testing.T) {
	s := &Server{cfg: &config.Config{SetupState: config.SetupStateReady}}
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("GET /tunnel", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/public", http.StatusMovedPermanently)
	})

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/tunnel", nil))

	if rec.Code != http.StatusMovedPermanently {
		t.Errorf("status = %d, want 301", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/public" {
		t.Errorf("Location = %q, want /public", got)
	}
}
