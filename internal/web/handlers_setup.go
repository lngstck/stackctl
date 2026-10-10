package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/envfile"
	"github.com/lngstck/stackctl/internal/paths"
	"github.com/lngstck/stackctl/internal/preflight"
	"github.com/lngstck/stackctl/internal/secrets"
)

// newProber builds the prerequisite checker. It is a variable so tests can
// answer DNS, port and deSEC questions without touching the network.
var newProber = preflight.NewProber

// setupData is the template context for setup.html.tmpl.
type setupData struct {
	SchoolName   string
	SchoolSlug   string
	ServerDomain string
	ContactEmail string
	Mode         string
	BaseDomain   string
	ACMEEmail    string
	// DNSToken and ChallengeDomain configure the certificate in the
	// school-network mode.
	DNSToken        string
	ChallengeDomain string
	// SetupCode is what the admin typed or what the link from install.sh
	// carried; it is echoed back so a failed submit does not lose it.
	SetupCode string
	Error     string
}

// setupCodeHint tells the admin where the code is. It appears whenever the
// code is missing or wrong.
const setupCodeHint = "Er steht im Terminal am Ende der Installation. Neu anzeigen: sudo stackctl setup-code"

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if s.cfg.SetupState != config.SetupStateNeedsSetup {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	data := setupData{
		ServerDomain: detectLANIP(),
		// Only inside the school network is the default: nothing leaves it
		// without an explicit decision.
		Mode: preflight.ModeLocal,
		// The link install.sh prints carries the code, so nobody types it.
		SetupCode: r.URL.Query().Get("code"),
	}
	if s.setupCode == "" {
		data.Error = "Der Einrichtungscode konnte nicht angelegt werden — die Einrichtung bleibt gesperrt. Details: journalctl -u stackctl"
	}
	s.render(w, "setup.html.tmpl", data)
}

// handleSetupPreflight answers the wizard's live prerequisite checks.
//
// It is reachable without a login, like the setup form itself, and closes
// together with it: once setup is done the endpoint refuses. It resolves
// names, tries to bind two local ports and asks deSEC about the token the
// caller typed — the exposure during that window is limited to what the
// caller already knows. It takes a POST so the token stays out of URLs and
// logs.
func (s *Server) handleSetupPreflight(w http.ResponseWriter, r *http.Request) {
	if s.cfg.SetupState != config.SetupStateNeedsSetup {
		http.Error(w, "Setup ist bereits abgeschlossen", http.StatusForbidden)
		return
	}
	if !s.checkSetupCode(r) {
		http.Error(w, "Einrichtungscode fehlt oder stimmt nicht", http.StatusForbidden)
		return
	}

	in := preflight.Input{
		Mode:            r.FormValue("mode"),
		BaseDomain:      strings.TrimSpace(r.FormValue("base_domain")),
		DNSToken:        strings.TrimSpace(r.FormValue("dns_token")),
		ChallengeDomain: normalizeDomain(r.FormValue("challenge_domain")),
	}

	checks := newProber().Run(r.Context(), in)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"summary": preflight.Worst(checks),
		"checks":  checks,
	}); err != nil {
		log.Printf("web: encode preflight result: %v", err)
	}
}

func (s *Server) handleSetupPost(w http.ResponseWriter, r *http.Request) {
	if s.cfg.SetupState != config.SetupStateNeedsSetup {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Ungueltige Formulardaten", http.StatusBadRequest)
		return
	}

	schoolName := r.FormValue("school_name")
	schoolSlug := r.FormValue("school_slug")
	serverDomain := r.FormValue("server_domain")
	contactEmail := r.FormValue("contact_email")
	password := r.FormValue("password")
	passwordConfirm := r.FormValue("password_confirm")
	mode := r.FormValue("public_mode")
	baseDomain := strings.TrimSpace(strings.ToLower(r.FormValue("base_domain")))
	acmeEmail := strings.TrimSpace(r.FormValue("acme_email"))
	dnsToken := strings.TrimSpace(r.FormValue("dns_token"))
	challengeDomain := normalizeDomain(r.FormValue("challenge_domain"))

	data := setupData{
		SchoolName:      schoolName,
		SchoolSlug:      schoolSlug,
		ServerDomain:    serverDomain,
		ContactEmail:    contactEmail,
		Mode:            mode,
		BaseDomain:      baseDomain,
		ACMEEmail:       acmeEmail,
		DNSToken:        dnsToken,
		ChallengeDomain: challengeDomain,
		SetupCode:       r.FormValue("setup_code"),
	}

	// The code comes first: without it nothing else is worth checking.
	if !s.checkSetupCode(r) {
		data.Error = "Der Einrichtungscode stimmt nicht. " + setupCodeHint
		if s.limiter != nil && s.limiter.isLocked(clientIP(r)) {
			data.Error = "Zu viele Fehlversuche. Bitte warte eine Minute."
		}
		s.render(w, "setup.html.tmpl", data)
		return
	}

	// Validation.
	if schoolName == "" {
		data.Error = "Schulname ist erforderlich."
		s.render(w, "setup.html.tmpl", data)
		return
	}
	if schoolSlug == "" {
		schoolSlug = slugify(schoolName)
		data.SchoolSlug = schoolSlug
	}
	if err := config.ValidateSlug(schoolSlug); err != nil {
		data.Error = fmt.Sprintf("Slug ungueltig: %v", err)
		s.render(w, "setup.html.tmpl", data)
		return
	}
	if password == "" {
		data.Error = "Admin-Passwort ist erforderlich."
		s.render(w, "setup.html.tmpl", data)
		return
	}
	if password != passwordConfirm {
		data.Error = "Passwoerter stimmen nicht ueberein."
		s.render(w, "setup.html.tmpl", data)
		return
	}
	if len(password) < 8 {
		data.Error = "Passwort muss mindestens 8 Zeichen lang sein."
		s.render(w, "setup.html.tmpl", data)
		return
	}

	// Resolve the chosen mode into transport and base domain. This is the
	// only place the wizard cards exist as such; everything downstream sees
	// a transport and a base domain.
	transport, baseDomain, err := resolvePublicMode(mode, baseDomain)
	if err != nil {
		data.Error = err.Error()
		s.render(w, "setup.html.tmpl", data)
		return
	}
	if transport == config.TransportLocal {
		if err := validateLocalCertificate(dnsToken, challengeDomain); err != nil {
			data.Error = err.Error()
			s.render(w, "setup.html.tmpl", data)
			return
		}
	}

	// Hash password.
	hash, err := secrets.HashPassword(password)
	if err != nil {
		data.Error = "Passwort-Hash fehlgeschlagen."
		s.render(w, "setup.html.tmpl", data)
		return
	}

	// Update config. Nothing outside the school has to approve the install,
	// so setup ends in "ready" right here.
	s.cfg.School.Name = schoolName
	s.cfg.School.Slug = schoolSlug
	s.cfg.School.ServerDomain = serverDomain
	s.cfg.School.ContactEmail = contactEmail
	s.cfg.Admin.PasswordHash = hash
	s.cfg.Public.Transport = transport
	s.cfg.Public.BaseDomain = baseDomain
	// Without a contact address Let's Encrypt issues certificates but nobody
	// is told when renewal starts failing — and the failure only becomes
	// visible when the certificate expires.
	if acmeEmail == "" || transport == config.TransportLocal {
		acmeEmail = contactEmail
	}
	s.cfg.Public.ACMEEmail = acmeEmail
	if transport == config.TransportLocal {
		s.cfg.Public.Local = config.PublicLocal{DNSToken: dnsToken, ChallengeDomain: challengeDomain}
	}
	s.cfg.SetupState = config.SetupStateReady

	if err := s.cfg.Save(); err != nil {
		log.Printf("web: save config after setup: %v", err)
		data.Error = "Konfiguration konnte nicht gespeichert werden."
		s.cfg.SetupState = config.SetupStateNeedsSetup
		s.render(w, "setup.html.tmpl", data)
		return
	}

	// Seed .env with system-owned keys, including ADMIN_PASSWORD so apps
	// can reference it via ${ADMIN_PASSWORD} in their environment block.
	env, err := envfile.Load(paths.EnvFile())
	if err != nil {
		env = envfile.New()
	}
	envfile.ApplySystemEnv(env, s.cfg, password)
	if err := env.Save(paths.EnvFile()); err != nil {
		log.Printf("web: save env after setup: %v", err)
	}

	// The admin password guards everything from here on.
	if err := removeSetupCode(); err != nil {
		log.Printf("web: remove setup code: %v", err)
	}
	s.setupCode = ""

	// Publish now — otherwise the login route would only appear on the next
	// stackctl restart.
	s.bootstrapPublisher()

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// checkSetupCode validates the request's setup_code field. Misses count
// towards the same per-address limit as failed logins, which also stops the
// preflight endpoint from being used to guess.
func (s *Server) checkSetupCode(r *http.Request) bool {
	ip := clientIP(r)
	if s.limiter != nil && s.limiter.isLocked(ip) {
		return false
	}
	if s.validSetupCode(r.FormValue("setup_code")) {
		return true
	}
	if s.limiter != nil {
		s.limiter.recordFailure(ip)
	}
	return false
}

// resolvePublicMode maps a wizard card onto transport and base domain. Every
// mode needs the school's domain: login and apps live under it.
func resolvePublicMode(mode, baseDomain string) (transport, resolved string, err error) {
	switch mode {
	case preflight.ModeLocal, preflight.ModeDirect:
		if baseDomain == "" {
			return "", "", errors.New("Bitte die Domain der Schule angeben.")
		}
		if err := config.ValidateBaseDomain(baseDomain); err != nil {
			return "", "", fmt.Errorf("Domain ungültig: %s", preflight.TranslateDomainError(err))
		}
		return mode, baseDomain, nil
	case "":
		return "", "", errors.New("Bitte eine Betriebsart auswählen.")
	default:
		return "", "", fmt.Errorf("Unbekannte Betriebsart %q.", mode)
	}
}

// validateLocalCertificate checks what DNS-01 needs. Without a token the
// server never gets a certificate, and nothing works — that is worth a stop
// here, unlike DNS records, which may well be created after setup.
func validateLocalCertificate(token, challengeDomain string) error {
	if token == "" {
		return errors.New("Für den Betrieb im Schulnetz wird ein deSEC-Token gebraucht — ohne ihn gibt es kein Zertifikat.")
	}
	if challengeDomain != "" {
		if err := config.ValidateChallengeDomain(challengeDomain); err != nil {
			return fmt.Errorf("Ziel bei deSEC ungültig: %s", preflight.TranslateDomainError(err))
		}
	}
	return nil
}

// normalizeDomain trims what people paste along with a domain name: blanks,
// upper case and the trailing dot of a fully qualified name.
func normalizeDomain(s string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
}
