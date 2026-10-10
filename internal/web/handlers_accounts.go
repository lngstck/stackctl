// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/lngstck/stackctl/internal/catalog"
	"github.com/lngstck/stackctl/internal/claims"
	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/dex"
	"github.com/lngstck/stackctl/internal/envfile"
	"github.com/lngstck/stackctl/internal/install"
	"github.com/lngstck/stackctl/internal/paths"
	"github.com/lngstck/stackctl/internal/public"
	"github.com/lngstck/stackctl/internal/secrets"
)

// accountRow is one test account as the settings page lists it.
type accountRow struct {
	ID        string
	Name      string
	RoleLabel string
	Login     string
}

// accountRows lists the test accounts for display. The password hash stays
// out of the template.
func accountRows(cfg *config.Config) []accountRow {
	rows := make([]accountRow, 0, len(cfg.Auth.TestAccounts))
	for _, a := range cfg.Auth.TestAccounts {
		rows = append(rows, accountRow{
			ID:        a.ID,
			Name:      a.Name,
			RoleLabel: claims.RoleLabel(a.Role),
			Login:     a.Login,
		})
	}
	return rows
}

// roleOption is one entry in the role select.
type roleOption struct {
	Value string
	Label string
}

func roleOptions() []roleOption {
	var out []roleOption
	for _, r := range claims.Roles() {
		out = append(out, roleOption{Value: r, Label: claims.RoleLabel(r)})
	}
	return out
}

// loginNamePattern is the part before the @. Narrow on purpose: people type it
// on school devices, often on a touch keyboard.
var loginNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,39}$`)

// handleAccountCreate adds a test account and hands it to Dex.
func (s *Server) handleAccountCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	loginName := strings.ToLower(strings.TrimSpace(r.FormValue("login_name")))
	role := r.FormValue("role")
	password := r.FormValue("password")

	base := public.BaseDomain(s.cfg)
	switch {
	case name == "":
		accountsRedirect(w, r, "Bitte einen Namen angeben.", true)
		return
	case len(name) > 100:
		accountsRedirect(w, r, "Der Name ist zu lang (höchstens 100 Zeichen).", true)
		return
	case strings.Contains(loginName, "@"):
		accountsRedirect(w, r, "Beim Anmeldenamen nur den Teil vor dem @ eingeben — der Rest kommt von der Domain.", true)
		return
	case !loginNamePattern.MatchString(loginName):
		accountsRedirect(w, r, "Anmeldename: Kleinbuchstaben, Ziffern, Punkt, Binde- und Unterstrich, höchstens 40 Zeichen.", true)
		return
	case !claims.ValidRole(role):
		accountsRedirect(w, r, "Bitte eine Rolle auswählen.", true)
		return
	case len(password) < 8:
		accountsRedirect(w, r, "Das Passwort braucht mindestens 8 Zeichen.", true)
		return
	case base == "":
		accountsRedirect(w, r, "Ohne Domain gibt es keine Anmeldung — ist die Einrichtung abgeschlossen?", true)
		return
	}

	login := loginName + "@" + base
	if s.cfg.Auth.TestAccountByLogin(login) != nil {
		accountsRedirect(w, r, "Den Anmeldenamen "+loginName+" gibt es schon.", true)
		return
	}

	hash, err := secrets.HashPassword(password)
	if err != nil {
		log.Printf("web: hash test account password: %v", err)
		accountsRedirect(w, r, "Das Passwort konnte nicht verarbeitet werden.", true)
		return
	}
	id, err := secrets.RandomHex(16)
	if err != nil {
		log.Printf("web: test account id: %v", err)
		accountsRedirect(w, r, "Das Konto konnte nicht angelegt werden.", true)
		return
	}

	s.cfg.Auth.TestAccounts = append(s.cfg.Auth.TestAccounts, config.TestAccount{
		ID:           id,
		Name:         name,
		Role:         role,
		Login:        login,
		PasswordHash: hash,
	})
	if err := s.cfg.Save(); err != nil {
		log.Printf("web: save config after adding test account: %v", err)
		s.cfg.Auth.RemoveTestAccount(id)
		accountsRedirect(w, r, "Das Konto konnte nicht gespeichert werden.", true)
		return
	}
	if msg := s.applyAccounts(); msg != "" {
		accountsRedirect(w, r, "Konto angelegt, aber: "+msg, true)
		return
	}
	accountsRedirect(w, r, fmt.Sprintf("%s kann sich jetzt als %s anmelden.", name, login), false)
}

// handleAccountDelete removes a test account. Sessions the person still holds
// in an app end when the app next refreshes its token.
func (s *Server) handleAccountDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	acc := s.cfg.Auth.TestAccountByID(id)
	if acc == nil {
		accountsRedirect(w, r, "Das Konto gibt es nicht mehr.", true)
		return
	}
	removed := *acc
	s.cfg.Auth.RemoveTestAccount(id)
	if err := s.cfg.Save(); err != nil {
		log.Printf("web: save config after removing test account: %v", err)
		s.cfg.Auth.TestAccounts = append(s.cfg.Auth.TestAccounts, removed)
		accountsRedirect(w, r, "Das Konto konnte nicht gelöscht werden.", true)
		return
	}
	if msg := s.applyAccounts(); msg != "" {
		accountsRedirect(w, r, "Konto gelöscht, aber: "+msg, true)
		return
	}
	accountsRedirect(w, r, removed.Name+" wurde gelöscht.", false)
}

// applyAccounts rewrites the Dex config so a change to the accounts takes
// effect. Without Dex installed there is nothing to tell: the accounts are
// saved and Dex picks them up when it is installed. It returns a message for
// the admin when Dex could not be updated, "" otherwise.
func (s *Server) applyAccounts() string {
	if !s.snapState().IsInstalled("dex") {
		return ""
	}
	if err := s.rewriteDexConfig(); err != nil {
		log.Printf("web: rewrite dex config: %v", err)
		return "die Anmeldung (Dex) konnte nicht neu gestartet werden. Unter Apps → Dex lässt sie sich neu starten."
	}
	return ""
}

// rewriteDexConfig regenerates Dex's configuration from the current config
// and every installed app's client, then restarts Dex.
func (s *Server) rewriteDexConfig() error {
	st := s.snapState()
	env, err := envfile.Load(paths.EnvFile())
	if err != nil {
		env = envfile.New()
	}
	var defs []*catalog.Definition
	for _, id := range st.InstalledIDs() {
		if d, err := catalog.LoadDefinition(id); err == nil {
			defs = append(defs, d)
		}
	}
	return dex.SaveConfig(s.cfg, install.ReconstructDexClients(defs, env, s.cfg))
}

// accountsRedirect sends the admin back to the accounts section with a notice.
func accountsRedirect(w http.ResponseWriter, r *http.Request, msg string, isErr bool) {
	q := url.Values{"konten": {msg}}
	if isErr {
		q.Set("konten_err", "1")
	}
	http.Redirect(w, r, "/settings?"+q.Encode()+"#konten", http.StatusSeeOther)
}
