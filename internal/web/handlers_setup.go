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
	"github.com/lngstck/stackctl/internal/envfile"
	"github.com/lngstck/stackctl/internal/paths"
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
	LLMEndpoint  string
	LLMAPIKey    string
	Error        string
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if s.cfg.SetupState != config.SetupStateNeedsSetup {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	data := setupData{
		ServerDomain: detectLANIP(),
		LLMEndpoint:  "https://llm.learningstack.online/v1",
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
	llmEndpoint := r.FormValue("llm_endpoint")
	llmAPIKey := r.FormValue("llm_api_key")

	data := setupData{
		SchoolName:   schoolName,
		SchoolSlug:   schoolSlug,
		ServerDomain: serverDomain,
		ContactEmail: contactEmail,
		LLMEndpoint:  llmEndpoint,
		LLMAPIKey:    llmAPIKey,
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
	s.cfg.Dex.AuthURL = "https://auth." + schoolSlug + ".learningstack.online"
	s.cfg.GlobalEnv["LLM_ENDPOINT"] = llmEndpoint
	s.cfg.GlobalEnv["LLM_API_KEY"] = llmAPIKey

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

	// Seed .env with system-owned keys, including the admin plaintext so
	// apps with admin_password_env can reuse it during install.
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

	slug := s.cfg.School.Slug
	dexTunnel := checkDexTunnel(slug)
	oidcClient := false
	if dexTunnel {
		oidcClient = checkOIDCClient(slug, s.cfg.Dex.ClientSecret)
	}

	// If both checks pass, transition to ready.
	if dexTunnel && oidcClient {
		s.cfg.SetupState = config.SetupStateReady
		if err := s.cfg.Save(); err != nil {
			log.Printf("web: save config after registration: %v", err)
		} else {
			log.Printf("web: registration complete, state → ready")
		}
		// Bootstrap tunnels now that we're ready — otherwise the Dex tunnel
		// would only come up on next stackctl restart.
		if s.tunnelMgr != nil {
			if err := tunnel.EnsureKey(); err != nil {
				log.Printf("web: ensure tunnel key: %v", err)
			}
			if err := s.tunnelMgr.EnsureDexTunnel(); err != nil {
				log.Printf("web: ensure dex tunnel: %v", err)
			}
			s.tunnelMgr.RestoreAppTunnels()
			s.tunnelMgr.StartMonitor()
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ready"}`)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"waiting","dex_tunnel":%t,"oidc_client":%t}`, dexTunnel, oidcClient)
}

// checkDexTunnel tests whether the school's wildcard subdomain is reachable
// via HTTPS. A successful TLS handshake (any HTTP status) means DNS + cert +
// sish are all configured. Timeout is short since this runs every 30s.
func checkDexTunnel(slug string) bool {
	host := fmt.Sprintf("auth.%s.learningstack.online", slug)
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
// the client_id, it redirects to the login page (302). If not, it returns
// an error page (4xx).
func checkOIDCClient(slug, clientSecret string) bool {
	authURL := fmt.Sprintf(
		"https://auth.learningstack.online/auth?client_id=%s&redirect_uri=%s&response_type=code&scope=openid",
		slug,
		url.QueryEscape(fmt.Sprintf("https://auth.%s.learningstack.online/callback", slug)),
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
