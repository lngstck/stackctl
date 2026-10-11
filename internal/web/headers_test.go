// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/secrets"
)

func TestSecurityHeaders(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	for k, want := range map[string]string{
		"X-Frame-Options":        "DENY",
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "same-origin",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Errorf("CSP = %q", rec.Header().Get("Content-Security-Policy"))
	}
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS über http: %q", got)
	}

	// Über Caddy, also https: HSTS dazu.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Strict-Transport-Security"); !strings.HasPrefix(got, "max-age=") {
		t.Errorf("HSTS über https = %q", got)
	}
}

func TestSessionCookieSecureOnlyOverHTTPS(t *testing.T) {
	for proto, want := range map[string]bool{"": false, "http": false, "https": true} {
		req := httptest.NewRequest(http.MethodPost, "/login", nil)
		if proto != "" {
			req.Header.Set("X-Forwarded-Proto", proto)
		}
		rec := httptest.NewRecorder()
		setSessionCookie(rec, req, "tok")
		c := rec.Result().Cookies()
		if len(c) != 1 || c[0].Secure != want || !c[0].HttpOnly {
			t.Errorf("proto %q: Cookie %+v, Secure soll %v sein", proto, c, want)
		}
	}
}

// Wer nur eine Sitzung hat, ändert das Admin-Passwort nicht ohne das alte.
func TestPasswordChangeNeedsCurrentPassword(t *testing.T) {
	s, _ := testServerWithPublisher(t, &fakePublisher{})
	s.cfg.SetupState = config.SetupStateReady
	s.cfg.School.Name = "Phoenix"
	hash, err := secrets.HashPassword("altes-passwort")
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.Admin.PasswordHash = hash
	if err := s.loadTemplates(); err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}
	change := func(current string) string {
		form := url.Values{
			"school_name":      {"Phoenix"},
			"current_password": {current},
			"password":         {"neues-passwort"},
			"password_confirm": {"neues-passwort"},
		}
		req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		s.handleSettingsPost(rec, req)
		return rec.Body.String()
	}

	for _, wrong := range []string{"", "falsch"} {
		body := change(wrong)
		if !strings.Contains(body, "aktuelle Passwort stimmt nicht") {
			t.Errorf("aktuelles Passwort %q: keine Fehlermeldung", wrong)
		}
		if !secrets.VerifyPassword(s.cfg.Admin.PasswordHash, "altes-passwort") {
			t.Fatalf("aktuelles Passwort %q: Passwort wurde trotzdem geändert", wrong)
		}
	}

	change("altes-passwort")
	if !secrets.VerifyPassword(s.cfg.Admin.PasswordHash, "neues-passwort") {
		t.Error("mit richtigem aktuellen Passwort nicht geändert")
	}
}
