// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/paths"
	"github.com/lngstck/stackctl/internal/secrets"
)

// accountsTestServer has no Dex installed, so account changes stop at
// config.yaml and never reach for Docker.
func accountsTestServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv(paths.EnvStackctlDir, t.TempDir())
	t.Setenv(paths.EnvLearningstackDir, t.TempDir())
	return &Server{
		cfg: &config.Config{
			Version:    config.ConfigVersion,
			SetupState: config.SetupStateReady,
			School:     config.School{Name: "Phoenix", Slug: "phoenix"},
			Public: config.Public{
				Transport:  config.TransportLocal,
				BaseDomain: "sl.gym-phoenix.de",
			},
		},
		state:    config.NewState(),
		sessions: &sessionStore{},
	}
}

func postAccountForm(h http.HandlerFunc, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func validAccountForm() url.Values {
	return url.Values{
		"name":       {"Frau Müller"},
		"login_name": {" Mueller "},
		"role":       {"lehrkraft"},
		"password":   {"geheim1234"},
	}
}

// noticeOf reads the account notice the handler put into its redirect.
func noticeOf(t *testing.T, rec *httptest.ResponseRecorder) (msg string, isErr bool) {
	t.Helper()
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want redirect", rec.Code)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("bad redirect: %v", err)
	}
	if loc.Path != "/settings" || loc.Fragment != "konten" {
		t.Errorf("redirect = %q, want /settings#konten", loc)
	}
	return loc.Query().Get("konten"), loc.Query().Get("konten_err") == "1"
}

// A new account is saved with a hashed password and a login under the
// school's domain — and it survives a reload of config.yaml.
func TestAccountCreate(t *testing.T) {
	s := accountsTestServer(t)

	rec := postAccountForm(s.handleAccountCreate, "/settings/accounts", validAccountForm())
	if msg, isErr := noticeOf(t, rec); isErr {
		t.Fatalf("create failed: %s", msg)
	}

	loaded, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if n := len(loaded.Auth.TestAccounts); n != 1 {
		t.Fatalf("saved accounts = %d, want 1", n)
	}
	a := loaded.Auth.TestAccounts[0]
	if a.Login != "mueller@sl.gym-phoenix.de" {
		t.Errorf("login = %q, want the trimmed, lower-cased name under the base domain", a.Login)
	}
	if a.Role != "lehrkraft" || a.Name != "Frau Müller" || a.ID == "" {
		t.Errorf("account = %+v", a)
	}
	if a.PasswordHash == "geheim1234" || !secrets.VerifyPassword(a.PasswordHash, "geheim1234") {
		t.Error("password must be stored as a hash that verifies")
	}
}

// Every rejected form leaves the config untouched and says why.
func TestAccountCreateRejects(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
		value string
	}{
		{"kein Name", "name", "  "},
		{"ganze Adresse statt Name", "login_name", "mueller@sl.gym-phoenix.de"},
		{"Leerzeichen im Anmeldenamen", "login_name", "frau mueller"},
		{"leerer Anmeldename", "login_name", ""},
		{"unbekannte Rolle", "role", "admin"},
		{"Anbieterwert statt Rolle", "role", "Lehr"},
		{"kurzes Passwort", "password", "kurz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := accountsTestServer(t)
			form := validAccountForm()
			form.Set(tc.field, tc.value)

			msg, isErr := noticeOf(t, postAccountForm(s.handleAccountCreate, "/settings/accounts", form))
			if !isErr || msg == "" {
				t.Errorf("want an error notice, got %q (err=%v)", msg, isErr)
			}
			if n := len(s.cfg.Auth.TestAccounts); n != 0 {
				t.Errorf("accounts = %d, want none", n)
			}
		})
	}
}

// Two people cannot share a login: Dex looks accounts up by it.
func TestAccountCreateRejectsDuplicateLogin(t *testing.T) {
	s := accountsTestServer(t)
	postAccountForm(s.handleAccountCreate, "/settings/accounts", validAccountForm())

	again := validAccountForm()
	again.Set("name", "Herr Müller")
	again.Set("login_name", "MUELLER")
	if _, isErr := noticeOf(t, postAccountForm(s.handleAccountCreate, "/settings/accounts", again)); !isErr {
		t.Error("duplicate login should be rejected")
	}
	if n := len(s.cfg.Auth.TestAccounts); n != 1 {
		t.Errorf("accounts = %d, want 1", n)
	}
}

func TestAccountDelete(t *testing.T) {
	s := accountsTestServer(t)
	postAccountForm(s.handleAccountCreate, "/settings/accounts", validAccountForm())
	id := s.cfg.Auth.TestAccounts[0].ID

	req := httptest.NewRequest(http.MethodPost, "/settings/accounts/"+id+"/delete", nil)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	s.handleAccountDelete(rec, req)
	if msg, isErr := noticeOf(t, rec); isErr {
		t.Fatalf("delete failed: %s", msg)
	}

	loaded, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if n := len(loaded.Auth.TestAccounts); n != 0 {
		t.Errorf("accounts after delete = %d, want 0", n)
	}
}

// The dashboard tells the admin when Dex runs but nobody can sign in — and
// stops once there is an account.
func TestDashboardFlagsMissingAccounts(t *testing.T) {
	s := accountsTestServer(t)
	s.state.Containers["dex"] = &config.ContainerState{ID: "dex", Name: "Dex"}

	has := func() bool {
		for _, is := range accountIssues(s.state, s.cfg) {
			if strings.Contains(is.Title, "niemand anmelden") {
				return true
			}
		}
		return false
	}
	if !has() {
		t.Error("want a notice while there are no accounts")
	}
	// Directly, not through the handler: with Dex installed the handler
	// would restart its container.
	s.cfg.Auth.TestAccounts = append(s.cfg.Auth.TestAccounts, config.TestAccount{ID: "x", Role: "schueler"})
	if has() {
		t.Error("notice should disappear once an account exists")
	}
}

// The settings page lists accounts with their login and a delete button, and
// never shows the password hash.
func TestSettingsRendersAccounts(t *testing.T) {
	s := accountsTestServer(t)
	if err := s.loadTemplates(); err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}
	postAccountForm(s.handleAccountCreate, "/settings/accounts", validAccountForm())
	acc := s.cfg.Auth.TestAccounts[0]

	data := s.settingsData("", "")
	data.AccountsNotice = "Frau Müller kann sich jetzt anmelden."
	rec := httptest.NewRecorder()
	s.render(rec, "settings.html.tmpl", data)

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, body)
	}
	for _, want := range []string{acc.Login, "Lehrkraft", "/settings/accounts/" + acc.ID + "/delete", "@sl.gym-phoenix.de", "kann sich jetzt anmelden"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, acc.PasswordHash) {
		t.Error("page must not contain the password hash")
	}
}
