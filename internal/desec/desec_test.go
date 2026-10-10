// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package desec

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeDeSEC answers the few endpoints EnsureWildcard uses, from a map of
// zone → subname/type → records, and counts writes.
type fakeDeSEC struct {
	mu     sync.Mutex
	token  string
	zones  map[string]map[string][]string // zone → "subname type" → records
	writes []map[string]any
}

func (f *fakeDeSEC) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "Token "+f.token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		switch {
		case r.Method == http.MethodGet && path == "/domains/":
			var out []map[string]string
			for z := range f.zones {
				out = append(out, map[string]string{"name": z})
			}
			json.NewEncoder(w).Encode(out)
		case r.Method == http.MethodGet && strings.HasSuffix(path, "/rrsets/"):
			zone := strings.TrimSuffix(strings.TrimPrefix(path, "/domains/"), "/rrsets/")
			sub := r.URL.Query().Get("subname")
			var out []map[string]any
			for key, recs := range f.zones[zone] {
				s, typ, _ := strings.Cut(key, " ")
				if s == sub {
					out = append(out, map[string]any{"subname": s, "type": typ, "records": recs})
				}
			}
			json.NewEncoder(w).Encode(out)
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/domains/"):
			json.NewEncoder(w).Encode(map[string]any{"minimum_ttl": 3600})
		case r.Method == http.MethodPut && strings.HasSuffix(path, "/rrsets/"):
			zone := strings.TrimSuffix(strings.TrimPrefix(path, "/domains/"), "/rrsets/")
			var body []map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("bad PUT body: %v", err)
			}
			for _, rr := range body {
				rr["zone"] = zone
				f.writes = append(f.writes, rr)
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("[]"))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func newFake(t *testing.T, zones map[string]map[string][]string) (*fakeDeSEC, *Client) {
	t.Helper()
	f := &fakeDeSEC{token: "tok", zones: zones}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	c := New("tok")
	c.BaseURL = srv.URL + "/api/v1"
	return f, c
}

// A fresh zone gets the wildcard, with deSEC's minimum TTL.
func TestEnsureWildcardCreates(t *testing.T) {
	f, c := newFake(t, map[string]map[string][]string{"test1.learningstack.online": {}})

	changed, err := c.EnsureWildcard(context.Background(), "test1.learningstack.online", net.ParseIP("192.168.178.20"))
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v, want a write", changed, err)
	}
	if len(f.writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(f.writes))
	}
	w := f.writes[0]
	if w["subname"] != "*" || w["type"] != "A" || w["zone"] != "test1.learningstack.online" || w["ttl"].(float64) != 3600 {
		t.Errorf("write = %v", w)
	}
	if recs := w["records"].([]any); len(recs) != 1 || recs[0] != "192.168.178.20" {
		t.Errorf("records = %v", recs)
	}
}

// Called on every start: a record that is already right is left alone, so
// deSEC does not re-sign the zone each time.
func TestEnsureWildcardSkipsWhenCurrent(t *testing.T) {
	f, c := newFake(t, map[string]map[string][]string{
		"test1.learningstack.online": {"* A": {"192.168.178.20"}},
	})
	changed, err := c.EnsureWildcard(context.Background(), "test1.learningstack.online", net.ParseIP("192.168.178.20"))
	if err != nil || changed || len(f.writes) != 0 {
		t.Errorf("changed=%v err=%v writes=%d, want no write", changed, err, len(f.writes))
	}
}

// When the school handed over a parent domain, the record lands in that
// zone with the right subname.
func TestEnsureWildcardInParentZone(t *testing.T) {
	f, c := newFake(t, map[string]map[string][]string{
		"schule.de":        {},
		"andere-schule.de": {},
	})
	if _, err := c.EnsureWildcard(context.Background(), "sl.schule.de", net.ParseIP("10.0.0.5")); err != nil {
		t.Fatal(err)
	}
	if w := f.writes[0]; w["zone"] != "schule.de" || w["subname"] != "*.sl" {
		t.Errorf("write = %v, want *.sl in schule.de", w)
	}
}

func TestEnsureWildcardErrors(t *testing.T) {
	_, c := newFake(t, map[string]map[string][]string{"andere.de": {}})
	if _, err := c.EnsureWildcard(context.Background(), "sl.schule.de", net.ParseIP("10.0.0.5")); !errors.Is(err, ErrNoZone) {
		t.Errorf("err = %v, want ErrNoZone", err)
	}

	c.Token = "falsch"
	if _, err := c.EnsureWildcard(context.Background(), "sl.schule.de", net.ParseIP("10.0.0.5")); !errors.Is(err, ErrTokenRejected) {
		t.Errorf("err = %v, want ErrTokenRejected", err)
	}
}

func TestEnsureWildcardIPv6(t *testing.T) {
	f, c := newFake(t, map[string]map[string][]string{"sl.schule.de": {}})
	if _, err := c.EnsureWildcard(context.Background(), "sl.schule.de", net.ParseIP("fd00::5")); err != nil {
		t.Fatal(err)
	}
	if f.writes[0]["type"] != "AAAA" {
		t.Errorf("type = %v, want AAAA", f.writes[0]["type"])
	}
}

func TestZoneForPicksClosest(t *testing.T) {
	zones := []string{"de", "schule.de", "sl.schule.de", "xschule.de"}
	if z, _ := ZoneFor(zones, "sl.schule.de"); z != "sl.schule.de" {
		t.Errorf("zone = %q", z)
	}
	if z, _ := ZoneFor(zones, "a.b.schule.de"); z != "schule.de" {
		t.Errorf("zone = %q", z)
	}
	if _, ok := ZoneFor([]string{"xschule.de"}, "schule.de"); ok {
		t.Error("a suffix that is not a parent must not match")
	}
}
