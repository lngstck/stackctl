// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// postForm builds a urlencoded POST request carrying the given form values.
func postForm(values map[string]string) *http.Request {
	var b strings.Builder
	first := true
	for k, v := range values {
		if !first {
			b.WriteByte('&')
		}
		first = false
		b.WriteString(k + "=" + v)
	}
	r := httptest.NewRequest(http.MethodPost, "/apps/x/remove", strings.NewReader(b.String()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func TestRequireCSRFRejectsMissingAndWrongToken(t *testing.T) {
	s := &Server{sessions: &sessionStore{}}
	s.sessions.create() // establishes a session with a CSRF token
	good := s.sessions.csrfToken()

	ran := false
	h := s.requireCSRF(func(http.ResponseWriter, *http.Request) { ran = true })

	cases := []struct {
		name  string
		token string
		want  int
	}{
		{"missing", "", http.StatusForbidden},
		{"wrong", "deadbeef", http.StatusForbidden},
		{"valid", good, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ran = false
			rec := httptest.NewRecorder()
			form := map[string]string{}
			if tc.token != "" {
				form["csrf_token"] = tc.token
			}
			h(rec, postForm(form))
			if tc.want == http.StatusOK {
				if !ran {
					t.Fatal("handler did not run for valid token")
				}
			} else {
				if ran {
					t.Fatal("handler ran despite invalid CSRF token")
				}
				if rec.Code != tc.want {
					t.Fatalf("status: want %d, got %d", tc.want, rec.Code)
				}
			}
		})
	}
}

// No session means no valid CSRF token — every POST is rejected.
func TestRequireCSRFRejectsWithoutSession(t *testing.T) {
	s := &Server{sessions: &sessionStore{}}
	h := s.requireCSRF(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler ran without a session")
	})
	rec := httptest.NewRecorder()
	h(rec, postForm(map[string]string{"csrf_token": "anything"}))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: want 403, got %d", rec.Code)
	}
}

// All embedded templates must parse with the csrfField helper registered —
// guards against a typo'd {{csrfField}} call slipping into a template.
func TestTemplatesParseWithCSRFHelper(t *testing.T) {
	s := &Server{}
	if err := s.loadTemplates(); err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}
	for _, name := range []string{
		"apps.html.tmpl", "app_detail.html.tmpl", "dashboard.html.tmpl",
		"settings.html.tmpl", "llm.html.tmpl", "public.html.tmpl",
	} {
		if _, ok := s.pages[name]; !ok {
			t.Errorf("template %q not loaded", name)
		}
	}
}
