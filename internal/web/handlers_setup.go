package web

import (
	"fmt"
	"log"
	"net/http"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/secrets"
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

	// TODO: Generate SSH tunnel key.
	// TODO: Build age-encrypted registration package.

	// Transition to awaiting_registration.
	s.cfg.SetupState = config.SetupStateAwaitingRegistration
	if err := s.cfg.Save(); err != nil {
		log.Printf("web: save config after setup: %v", err)
		data.Error = "Konfiguration konnte nicht gespeichert werden."
		s.cfg.SetupState = config.SetupStateNeedsSetup
		s.render(w, "setup.html.tmpl", data)
		return
	}

	http.Redirect(w, r, "/setup/register", http.StatusSeeOther)
}

// registerData is the template context for register.html.tmpl.
type registerData struct {
	SchoolSlug   string
	ContactEmail string
	DevMode      bool
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if s.cfg.SetupState != config.SetupStateAwaitingRegistration {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	data := registerData{
		SchoolSlug:   s.cfg.School.Slug,
		ContactEmail: s.cfg.School.ContactEmail,
		DevMode:      s.devMode,
	}
	s.render(w, "register.html.tmpl", data)
}

func (s *Server) handleRegisterDownload(w http.ResponseWriter, r *http.Request) {
	if s.cfg.SetupState != config.SetupStateAwaitingRegistration {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	// TODO: Serve the age-encrypted registration package.
	http.Error(w, "Registrierungspaket nicht implementiert", http.StatusNotImplemented)
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

	// TODO: Poll central Dex + sish tunnel to check registration status.
	// For now, always return "waiting".
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"status":"waiting","dex_tunnel":false,"oidc_client":false}`)
}
