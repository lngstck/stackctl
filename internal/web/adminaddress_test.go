package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/publish"
)

func stubProxyReady(t *testing.T, err error) {
	t.Helper()
	old := waitForProxy
	waitForProxy = func() error { return err }
	t.Cleanup(func() { waitForProxy = old })
}

// Im Schulnetz bekommt die Verwaltung ihre Adresse mit dem Reverse-Proxy:
// Sie bleibt im Schulnetz, das Passwort geht aber verschlüsselt durchs Netz.
func TestAutoPublishAdminWithProxy(t *testing.T) {
	stubProxyReady(t, nil)
	fake := &fakePublisher{}
	s, _ := testServerWithPublisher(t, fake)
	s.listenPort = 8090

	working := s.snapState()
	msg := s.autoPublishAdmin(working, "caddy")
	if !working.AdminPublished {
		t.Error("AdminPublished bleibt false")
	}
	if len(fake.adminPorts) != 1 || fake.adminPorts[0] != 8090 {
		t.Errorf("StartAdmin-Aufrufe = %v, want [8090]", fake.adminPorts)
	}
	if !strings.Contains(msg, "https://admin.ls.gym-phoenix.de") {
		t.Errorf("Meldung ohne Adresse: %q", msg)
	}
}

// Andere Apps, der direkte Betrieb (dort hieße das: aus dem Internet) und
// eine schon eingeschaltete Adresse bleiben unangetastet.
func TestAutoPublishAdminLeavesOtherCasesAlone(t *testing.T) {
	stubProxyReady(t, nil)
	fake := &fakePublisher{}
	s, _ := testServerWithPublisher(t, fake)

	working := s.snapState()
	if msg := s.autoPublishAdmin(working, "dex"); msg != "" || working.AdminPublished {
		t.Errorf("andere App: msg=%q published=%v", msg, working.AdminPublished)
	}

	working.AdminPublished = true
	s.autoPublishAdmin(working, "caddy")

	working.AdminPublished = false
	s.cfg.Public.Transport = config.TransportDirect
	if msg := s.autoPublishAdmin(working, "caddy"); msg != "" || working.AdminPublished {
		t.Errorf("direkter Betrieb: msg=%q published=%v", msg, working.AdminPublished)
	}
	if len(fake.adminPorts) != 0 {
		t.Errorf("StartAdmin aufgerufen: %v", fake.adminPorts)
	}
}

// Scheitert es, bleibt die Installation gelungen, der Zustand ehrlich und der
// Job sagt, wo es weitergeht.
func TestAutoPublishAdminFailureIsReported(t *testing.T) {
	for name, tc := range map[string]struct {
		ready, start error
	}{
		"Proxy nicht bereit": {ready: errors.New("reload")},
		"Route scheitert":    {start: errors.New("route")},
	} {
		t.Run(name, func(t *testing.T) {
			stubProxyReady(t, tc.ready)
			s, _ := testServerWithPublisher(t, &fakePublisher{adminErr: tc.start})
			working := s.snapState()
			msg := s.autoPublishAdmin(working, "caddy")
			if !strings.Contains(msg, "Zugang") || working.AdminPublished {
				t.Errorf("msg=%q published=%v", msg, working.AdminPublished)
			}
		})
	}
}

// Der Hinweis auf die verschlüsselte Adresse erscheint nur, wenn sie läuft und
// die Seite über den unverschlüsselten Port kam.
func TestSecureAdminURL(t *testing.T) {
	fake := &fakePublisher{adminStatus: publish.StatusRunning}
	s, _ := testServerWithPublisher(t, fake)
	req := func(host string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/login", nil)
		r.Host = host
		return r
	}

	if got := s.secureAdminURL(req("192.168.1.10:8090")); got != "https://admin.ls.gym-phoenix.de" {
		t.Errorf("über Port 8090: %q", got)
	}
	if got := s.secureAdminURL(req("admin.ls.gym-phoenix.de")); got != "" {
		t.Errorf("schon verschlüsselt: %q", got)
	}
	fake.adminStatus = publish.StatusStopped
	if got := s.secureAdminURL(req("192.168.1.10:8090")); got != "" {
		t.Errorf("Adresse aus: %q", got)
	}
	fake.adminStatus = publish.StatusRunning
	s.cfg.Public.Transport = config.TransportDirect
	if got := s.secureAdminURL(req("192.168.1.10:8090")); got != "" {
		t.Errorf("direkter Betrieb: %q", got)
	}
}

func TestLoginShowsSecureAddress(t *testing.T) {
	fake := &fakePublisher{adminStatus: publish.StatusRunning}
	s, _ := testServerWithPublisher(t, fake)
	s.cfg.SetupState = config.SetupStateReady
	if err := s.loadTemplates(); err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}
	r := httptest.NewRequest(http.MethodGet, "/login", nil)
	r.Host = "192.168.1.10:8090"
	rec := httptest.NewRecorder()
	s.handleLogin(rec, r)
	if !strings.Contains(rec.Body.String(), `href="https://admin.ls.gym-phoenix.de"`) {
		t.Errorf("Anmeldeseite ohne Hinweis:\n%s", rec.Body.String())
	}
}
