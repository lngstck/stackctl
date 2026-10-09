// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/desec"
)

// No web test may reach the real deSEC. Tests that want an API install a
// fake through fakeDeSECAPI; everything else lands on a closed port.
func init() {
	newDeSEC = func(token string) *desec.Client {
		c := desec.New(token)
		c.BaseURL = "http://127.0.0.1:1/unreachable"
		return c
	}
}

// deSECAPI records the wildcard writes stackctl makes. Zones are the domains
// the token can see; reject makes every request fail as a bad token would.
type deSECAPI struct {
	mu     sync.Mutex
	zones  []string
	reject bool
	writes []string // "zone subname type value"
}

func fakeDeSECAPI(t *testing.T, api *deSECAPI) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.mu.Lock()
		defer api.mu.Unlock()
		if api.reject {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		switch {
		case r.Method == http.MethodGet && path == "/domains/":
			var out []map[string]string
			for _, z := range api.zones {
				out = append(out, map[string]string{"name": z})
			}
			json.NewEncoder(w).Encode(out)
		case r.Method == http.MethodGet && strings.HasSuffix(path, "/rrsets/"):
			w.Write([]byte("[]"))
		case r.Method == http.MethodGet:
			w.Write([]byte(`{"minimum_ttl": 3600}`))
		case r.Method == http.MethodPut:
			zone := strings.TrimSuffix(strings.TrimPrefix(path, "/domains/"), "/rrsets/")
			var body []struct {
				Subname string   `json:"subname"`
				Type    string   `json:"type"`
				Records []string `json:"records"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			for _, rr := range body {
				api.writes = append(api.writes, strings.Join([]string{zone, rr.Subname, rr.Type, strings.Join(rr.Records, ",")}, " "))
			}
			w.Write([]byte("[]"))
		}
	}))
	t.Cleanup(srv.Close)

	orig := newDeSEC
	newDeSEC = func(token string) *desec.Client {
		c := desec.New(token)
		c.BaseURL = srv.URL + "/api/v1"
		return c
	}
	t.Cleanup(func() { newDeSEC = orig })
}

// handedOverSetupForm is the standard school-network setup: the domain sits
// at deSEC, no delegation target.
func handedOverSetupForm() map[string][]string {
	form := localSetupForm()
	form.Set("base_domain", "test1.learningstack.online")
	form.Set("challenge_domain", "")
	form.Set("server_domain", "192.168.178.20")
	return form
}

// Setup ends with the wildcard pointing at this server — the first app the
// admin installs needs it.
func TestSetupSetsWildcardAtDeSEC(t *testing.T) {
	api := &deSECAPI{zones: []string{"test1.learningstack.online"}}
	fakeDeSECAPI(t, api)

	s, _ := testServerWithPublisher(t, &fakePublisher{})
	s.cfg = config.Default()
	if err := s.loadTemplates(); err != nil {
		t.Fatal(err)
	}
	if rec := postSetup(t, s, handedOverSetupForm()); rec.Code != http.StatusSeeOther {
		t.Fatalf("setup failed: %d\n%s", rec.Code, rec.Body.String())
	}

	want := "test1.learningstack.online * A 192.168.178.20"
	if len(api.writes) != 1 || api.writes[0] != want {
		t.Errorf("writes = %v, want [%s]", api.writes, want)
	}
	if msg := s.dnsSync.get(); msg != "" {
		t.Errorf("sync error = %q", msg)
	}
}

// deSEC refusing does not undo setup: the dashboard says what is missing.
func TestSetupSurvivesDeSECFailure(t *testing.T) {
	fakeDeSECAPI(t, &deSECAPI{reject: true})

	s, _ := testServerWithPublisher(t, &fakePublisher{})
	s.cfg = config.Default()
	if err := s.loadTemplates(); err != nil {
		t.Fatal(err)
	}
	if rec := postSetup(t, s, handedOverSetupForm()); rec.Code != http.StatusSeeOther {
		t.Fatalf("setup should complete despite deSEC: %d", rec.Code)
	}
	issues := s.dnsSyncIssues()
	if len(issues) != 1 || !strings.Contains(issues[0].Detail, "Token") {
		t.Errorf("dashboard issues = %+v, want one naming the token", issues)
	}
}

// With a delegation target the school keeps its records at its own
// provider; stackctl must not write DNS anywhere.
func TestNoWildcardWritesWithDelegationTarget(t *testing.T) {
	api := &deSECAPI{zones: []string{"gym-phoenix.dedyn.io"}}
	fakeDeSECAPI(t, api)

	s, _ := testServerWithPublisher(t, &fakePublisher{})
	s.cfg = config.Default()
	if err := s.loadTemplates(); err != nil {
		t.Fatal(err)
	}
	postSetup(t, s, localSetupForm())
	if len(api.writes) != 0 {
		t.Errorf("writes = %v, want none", api.writes)
	}
}

// A hostname instead of an address cannot become an A record; the admin is
// told where to fix it.
func TestWildcardNeedsAnAddress(t *testing.T) {
	api := &deSECAPI{zones: []string{"test1.learningstack.online"}}
	fakeDeSECAPI(t, api)

	s, _ := testServerWithPublisher(t, &fakePublisher{})
	s.cfg.Public.Local.DNSToken = "tok"
	s.cfg.Public.BaseDomain = "test1.learningstack.online"
	s.cfg.School.ServerDomain = "schulserver.local"

	if err := s.syncWildcard(t.Context()); err == nil || !strings.Contains(err.Error(), "keine IP-Adresse") {
		t.Errorf("err = %v", err)
	}
	if len(api.writes) != 0 {
		t.Errorf("writes = %v, want none", api.writes)
	}
}
