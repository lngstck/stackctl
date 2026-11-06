// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/publish"
)

func TestPublicAddr(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":           false, // SSH-Tunnel
		"::1":                 false,
		"10.1.2.3":            false,
		"172.18.0.5":          false, // Caddy im Docker-Netz
		"192.168.1.10":        false, // Schulnetz
		"100.101.102.103":     false, // Tailscale / CGNAT
		"169.254.1.1":         false,
		"fe80::1":             false,
		"fd12:3456::1":        false, // ULA
		"::ffff:192.168.1.10": false,
		"203.0.113.5":         true,
		"8.8.8.8":             true,
		"100.128.0.1":         true, // knapp außerhalb von 100.64.0.0/10
		"2a01:4f8::1":         true,
		"::ffff:203.0.113.5":  true, // IPv4 über einen Dual-Stack-Socket
	}
	for in, want := range cases {
		if got := publicAddr(netip.MustParseAddr(in)); got != want {
			t.Errorf("publicAddr(%s) = %v, want %v", in, got, want)
		}
	}
}

// guardServer is a server with all routes and templates, in the given state.
func guardServer(t *testing.T, setup config.SetupState, transport string) *Server {
	t.Helper()
	s := &Server{
		cfg: &config.Config{
			SetupState: setup,
			School:     config.School{Slug: "phoenix"},
			Public:     config.Public{Transport: transport, BaseDomain: "ls.gym-phoenix.de"},
		},
		state:      config.NewState(),
		sessions:   &sessionStore{},
		limiter:    newRateLimiter(),
		jobs:       newJobStore(),
		mux:        http.NewServeMux(),
		listenPort: 8090,
	}
	if err := s.loadTemplates(); err != nil {
		t.Fatal(err)
	}
	s.routes()
	return s
}

func guardGet(s *Server, path, remote string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = remote
	req.Host = "203.0.113.5:8090"
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

const (
	fromInternetV4 = "203.0.113.9:51000"
	fromInternetV6 = "[2a01:4f8::9]:51000"
	fromMapped     = "[::ffff:203.0.113.9]:51000"
	fromTunnel     = "127.0.0.1:51000"
	fromCaddy      = "172.18.0.5:51000"
	fromSchoolLAN  = "192.168.1.50:51000"
)

var (
	publicSources  = []string{fromInternetV4, fromInternetV6, fromMapped}
	privateSources = []string{fromTunnel, fromCaddy, fromSchoolLAN}
)

// The school network is the usual case and must stay as it was. After setup
// in the school-network mode no address, however public it looks, is turned
// away. Before setup the admin comes from the school network over the private
// address in the install.sh link, and that still gets through.
func TestInternetGuardLeavesSchoolNetworkAlone(t *testing.T) {
	ready := guardServer(t, config.SetupStateReady, config.TransportLocal)
	for _, remote := range append(publicSources, privateSources...) {
		for _, path := range []string{"/healthz", "/login", "/setup"} {
			if rec := guardGet(ready, path, remote); rec.Code == http.StatusForbidden {
				t.Errorf("Schulnetz eingerichtet: GET %s von %s = 403, want the normal page", path, remote)
			}
		}
	}

	fresh := guardServer(t, config.SetupStateNeedsSetup, "")
	for _, remote := range privateSources {
		rec := guardGet(fresh, "/setup?code=ABCD-EFGH", remote)
		if rec.Code != http.StatusOK {
			t.Errorf("vor Einrichtung: GET /setup von %s = %d, want 200", remote, rec.Code)
		}
	}
}

// The plain port answers the public internet with the explanation only:
// before setup, always, and after setup in the direct mode.
func TestInternetGuardBlocksInternet(t *testing.T) {
	servers := map[string]*Server{
		"direkter Betrieb": guardServer(t, config.SetupStateReady, config.TransportDirect),
		"vor Einrichtung":  guardServer(t, config.SetupStateNeedsSetup, ""),
	}
	for name, s := range servers {
		for _, remote := range publicSources {
			rec := guardGet(s, "/login", remote)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s: GET /login von %s = %d, want 403", name, remote, rec.Code)
				continue
			}
			body := rec.Body.String()
			if !strings.Contains(body, "ssh -L 8090:localhost:8090 login@203.0.113.5") {
				t.Errorf("%s: page lacks the tunnel command:\n%s", name, body)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
				t.Errorf("%s: Content-Type = %q", name, ct)
			}
			// The security headers still apply to the explanation.
			if rec.Header().Get("X-Frame-Options") != "DENY" {
				t.Errorf("%s: security headers missing", name)
			}
		}
		// Tunnel, VPN and Caddy (admin.<domain>) keep working.
		for _, remote := range privateSources {
			if rec := guardGet(s, "/healthz", remote); rec.Code != http.StatusOK {
				t.Errorf("%s: GET /healthz von %s = %d, want 200", name, remote, rec.Code)
			}
		}
		// Styles for the explanation page.
		if rec := guardGet(s, "/static/stackctl.css", fromInternetV4); rec.Code != http.StatusOK {
			t.Errorf("%s: static asset = %d, want 200", name, rec.Code)
		}
	}
}

// Before setup the page must not claim the server hangs on the internet — it
// may well be a school server reached over a public IPv6 address. It links
// the setup page with the same code through the tunnel.
func TestInternetGuardSetupPage(t *testing.T) {
	s := guardServer(t, config.SetupStateNeedsSetup, "")
	rec := guardGet(s, "/setup?code=ABCD-EFGH", fromInternetV6)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `href="http://localhost:8090/setup?code=ABCD-EFGH"`) {
		t.Errorf("page does not link the setup page through the tunnel:\n%s", body)
	}
	for _, want := range []string{"Einrichtung nur über einen sicheren Weg", "Einrichtungscode", "private Adresse des Servers"} {
		if !strings.Contains(body, want) {
			t.Errorf("setup page lacks %q", want)
		}
	}
	if strings.Contains(body, "hängt direkt am Internet") {
		t.Error("before setup the page must not claim the server hangs on the internet")
	}
}

// Once admin.<domain> is up, the page offers it as the encrypted way in.
func TestInternetGuardOffersAdminAddress(t *testing.T) {
	s := guardServer(t, config.SetupStateReady, config.TransportDirect)
	s.publisher = &fakePublisher{adminStatus: publish.StatusRunning}
	rec := guardGet(s, "/", fromInternetV4)
	if !strings.Contains(rec.Body.String(), `href="https://admin.ls.gym-phoenix.de"`) {
		t.Errorf("page lacks the admin address:\n%s", rec.Body.String())
	}

	s.publisher = &fakePublisher{adminStatus: publish.StatusStopped}
	rec = guardGet(s, "/", fromInternetV4)
	if strings.Contains(rec.Body.String(), "admin.ls.gym-phoenix.de") {
		t.Error("a switched-off admin address must not be offered")
	}
}

// Setup decides what follows: the school-network mode opens the plain port
// again, the direct mode keeps it closed to the internet.
func TestInternetGuardFollowsModeAfterSetup(t *testing.T) {
	for _, tc := range []struct {
		transport string
		blocked   bool
	}{
		{config.TransportLocal, false},
		{config.TransportDirect, true},
	} {
		s := guardServer(t, config.SetupStateNeedsSetup, "")
		if rec := guardGet(s, "/login", fromInternetV4); rec.Code != http.StatusForbidden {
			t.Fatalf("before setup: code = %d, want 403", rec.Code)
		}
		s.cfg.SetupState = config.SetupStateReady
		s.cfg.Public.Transport = tc.transport
		rec := guardGet(s, "/login", fromInternetV4)
		if blocked := rec.Code == http.StatusForbidden; blocked != tc.blocked {
			t.Errorf("%s after setup: code = %d, blocked = %v, want %v", tc.transport, rec.Code, blocked, tc.blocked)
		}
	}
}

// The Zugang page names the way into the plain port. In the school network
// that text stays exactly as before; in the direct mode it names the tunnel,
// because the server address on the plain port no longer answers the internet.
func TestPublicPageNamesWayIn(t *testing.T) {
	cases := []struct {
		transport string
		want      []string
		notWant   string
	}{
		{config.TransportLocal,
			[]string{"im Schulnetz immer unter <code>10.0.0.5:8090</code>", "Unter 10.0.0.5:8090 bleibt sie wie bisher erreichbar."},
			"SSH-Tunnel"},
		{config.TransportDirect,
			[]string{"immer per SSH-Tunnel unter <code>localhost:8090</code>", "Per SSH-Tunnel unter localhost:8090 bleibt sie wie bisher erreichbar."},
			"im Schulnetz immer unter"},
	}
	for _, tc := range cases {
		t.Run(tc.transport, func(t *testing.T) {
			s, _ := testServerWithPublisher(t, &fakePublisher{})
			s.cfg.Public.Transport = tc.transport
			s.cfg.School.ServerDomain = "10.0.0.5"
			s.listenPort = 8090
			if err := s.loadTemplates(); err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			s.handlePublic(rec, httptest.NewRequest(http.MethodGet, "/public", nil))
			body := rec.Body.String()
			for _, w := range tc.want {
				if !strings.Contains(body, w) {
					t.Errorf("page lacks %q", w)
				}
			}
			if strings.Contains(body, tc.notWant) {
				t.Errorf("page contains %q", tc.notWant)
			}
		})
	}
}
