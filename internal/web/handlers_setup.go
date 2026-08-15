package web

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/dex"
	"github.com/lngstck/stackctl/internal/envfile"
	"github.com/lngstck/stackctl/internal/paths"
	"github.com/lngstck/stackctl/internal/public"
	"github.com/lngstck/stackctl/internal/registration"
	"github.com/lngstck/stackctl/internal/secrets"
	"github.com/lngstck/stackctl/internal/tunnel"
)

// setupData is the template context for setup.html.tmpl.
type setupData struct {
	SchoolName   string
	SchoolSlug   string
	ServerDomain string
	ContactEmail string
	Error        string
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if s.cfg.SetupState != config.SetupStateNeedsSetup {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	data := setupData{
		ServerDomain: detectLANIP(),
	}
	s.render(w, "setup.html.tmpl", data)
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

	data := setupData{
		SchoolName:   schoolName,
		SchoolSlug:   schoolSlug,
		ServerDomain: serverDomain,
		ContactEmail: contactEmail,
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

	// Hash password.
	hash, err := secrets.HashPassword(password)
	if err != nil {
		data.Error = "Passwort-Hash fehlgeschlagen."
		s.render(w, "setup.html.tmpl", data)
		return
	}

	// Update config.
	s.cfg.School.Name = schoolName
	s.cfg.School.Slug = schoolSlug
	s.cfg.School.ServerDomain = serverDomain
	s.cfg.School.ContactEmail = contactEmail
	s.cfg.Admin.PasswordHash = hash
	s.cfg.Dex.ClientID = schoolSlug
	// Phase 1 of the public-address rework: setup still hands out an
	// operator-relay address. Picking a transport and a domain is the wizard
	// step that follows; from here on everything downstream reads
	// cfg.Public rather than assembling the address itself.
	s.cfg.Public.Transport = config.TransportRelay
	s.cfg.Public.BaseDomain = config.RelayBaseDomain(schoolSlug)
	if s.cfg.Public.Relay.SSHHost == "" {
		s.cfg.Public.Relay.SSHHost = config.DefaultRelaySSHHost
	}
	if s.cfg.Public.Relay.SSHPort == 0 {
		s.cfg.Public.Relay.SSHPort = config.DefaultRelaySSHPort
	}
	s.cfg.Dex.AuthURL = public.AuthURL(s.cfg)

	// Generate Dex client secret.
	dexSecret, err := secrets.RandomHex(20)
	if err != nil {
		data.Error = "Dex-Secret konnte nicht erzeugt werden."
		s.render(w, "setup.html.tmpl", data)
		return
	}
	s.cfg.Dex.ClientSecret = dexSecret

	// Generate SSH tunnel key (idempotent — keeps existing key).
	if err := tunnel.EnsureKey(); err != nil {
		log.Printf("web: generate tunnel key: %v", err)
		data.Error = "SSH-Key konnte nicht erzeugt werden: " + err.Error()
		s.render(w, "setup.html.tmpl", data)
		return
	}

	sshPubKey, err := tunnel.PublicKey()
	if err != nil {
		data.Error = "SSH-Public-Key konnte nicht gelesen werden."
		s.render(w, "setup.html.tmpl", data)
		return
	}

	// Build age-encrypted registration package.
	payload := registration.Payload{
		Slug:            schoolSlug,
		SchoolName:      schoolName,
		ContactEmail:    contactEmail,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
		ServerDomain:    serverDomain,
		SSHPublicKey:    sshPubKey,
		DexClientID:     schoolSlug,
		DexClientSecret: dexSecret,
		DexRedirectURI:  public.AuthURL(s.cfg) + "/callback",
	}
	pkgPath, err := registration.BuildAndEncrypt(payload)
	if err != nil {
		log.Printf("web: build registration package: %v", err)
		data.Error = "Registrierungspaket konnte nicht erstellt werden: " + err.Error()
		s.render(w, "setup.html.tmpl", data)
		return
	}

	// Record registration metadata.
	s.cfg.Registration.StateEnteredAt = payload.CreatedAt
	s.cfg.Registration.PackagePath = filepath.Base(pkgPath)

	// Transition to awaiting_registration.
	s.cfg.SetupState = config.SetupStateAwaitingRegistration
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

	http.Redirect(w, r, "/setup/register", http.StatusSeeOther)
}

// registerData is the template context for register.html.tmpl.
type registerData struct {
	SchoolSlug   string
	SchoolName   string
	ContactEmail string
	AgeBlock     string
	DevMode      bool
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if s.cfg.SetupState != config.SetupStateAwaitingRegistration {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// Read the age block for display in the mailto body.
	var ageBlock string
	pkgFile := paths.RegistrationPackageFile(s.cfg.School.Slug)
	if data, err := os.ReadFile(pkgFile); err == nil {
		ageBlock = strings.TrimSpace(string(data))
	}

	data := registerData{
		SchoolSlug:   s.cfg.School.Slug,
		SchoolName:   s.cfg.School.Name,
		ContactEmail: s.cfg.School.ContactEmail,
		AgeBlock:     ageBlock,
		DevMode:      s.devMode,
	}
	s.render(w, "register.html.tmpl", data)
}

func (s *Server) handleRegisterDownload(w http.ResponseWriter, r *http.Request) {
	if s.cfg.SetupState != config.SetupStateAwaitingRegistration {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	slug := s.cfg.School.Slug
	pkgFile := paths.RegistrationPackageFile(slug)
	data, err := os.ReadFile(pkgFile)
	if err != nil {
		log.Printf("web: read registration package: %v", err)
		http.Error(w, "Registrierungspaket nicht gefunden. Bitte Setup erneut durchfuehren.", http.StatusInternalServerError)
		return
	}

	filename := fmt.Sprintf("registration-%s.age", slug)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Write(data)
}

func (s *Server) handleRegisterSkip(w http.ResponseWriter, r *http.Request) {
	if !s.devMode {
		http.Error(w, "Nur im Dev-Modus verfuegbar", http.StatusForbidden)
		return
	}
	if s.cfg.SetupState != config.SetupStateAwaitingRegistration {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.cfg.SetupState = config.SetupStateReady
	if err := s.cfg.Save(); err != nil {
		log.Printf("web: skip registration save: %v", err)
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	if s.cfg.SetupState != config.SetupStateAwaitingRegistration {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ready"}`)
		return
	}

	authHost := public.AuthHost(s.cfg)
	redirectURI := public.AuthURL(s.cfg) + "/callback"
	dexTunnel := checkPublicHost(authHost)
	oidcClient := false
	if dexTunnel {
		oidcClient = checkOIDCClient(s.cfg.Dex.ClientID, redirectURI)
	}

	// If both checks pass, transition to ready.
	if dexTunnel && oidcClient {
		s.cfg.SetupState = config.SetupStateReady
		if err := s.cfg.Save(); err != nil {
			log.Printf("web: save config after registration: %v", err)
		} else {
			log.Printf("web: registration complete, state → ready")
		}
		// Publish now that we're ready — otherwise the login would only
		// become reachable on the next stackctl restart.
		s.bootstrapPublisher()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ready"}`)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"waiting","dex_tunnel":%t,"oidc_client":%t}`, dexTunnel, oidcClient)
}

// checkPublicHost tests whether a public hostname of this install is
// reachable via HTTPS. A successful TLS handshake (any HTTP status) means DNS
// and certificate are in place and something is answering. Timeout is short
// since this runs every 30s.
func checkPublicHost(host string) bool {
	if host == "" {
		return false
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // don't follow redirects
		},
	}
	resp, err := client.Get("https://" + host + "/")
	if err != nil {
		return false
	}
	resp.Body.Close()
	// Any valid TLS response means the infrastructure is ready.
	return true
}

// checkOIDCClient tests whether the school is registered as a client in the
// central Dex by making an authorization request. If the central Dex knows
// the client_id *and* the redirect URI the operator registered for it, it
// redirects to the login page (302). If either is unknown, it returns an
// error page (4xx) — which is exactly what makes this a useful probe once
// the address is no longer derivable from the slug.
func checkOIDCClient(clientID, redirectURI string) bool {
	authURL := fmt.Sprintf(
		"%s/auth?client_id=%s&redirect_uri=%s&response_type=code&scope=openid",
		dex.CentralDexIssuer,
		url.QueryEscape(clientID),
		url.QueryEscape(redirectURI),
	)
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(authURL)
	if err != nil {
		return false
	}
	resp.Body.Close()
	// Dex redirects to the login page (302) if the client_id is valid.
	return resp.StatusCode == http.StatusFound
}
