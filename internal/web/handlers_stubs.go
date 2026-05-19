package web

import (
	"log"
	"net/http"

	"github.com/lngstck/stackctl/internal/envfile"
	"github.com/lngstck/stackctl/internal/paths"
	"github.com/lngstck/stackctl/internal/secrets"
)

// settingsData is the template context for settings.html.tmpl.
type settingsData struct {
	PageData
	SchoolName   string
	SchoolSlug   string
	ServerDomain string
	ContactEmail string
	LLMEndpoint  string
	LLMAPIKey    string
	DexAuthURL   string
	AutoUpdate   bool
	Error        string
	Message      string
}

func (s *Server) settingsData(msg, errMsg string) settingsData {
	return settingsData{
		PageData:     s.pageData("settings"),
		SchoolName:   s.cfg.School.Name,
		SchoolSlug:   s.cfg.School.Slug,
		ServerDomain: s.cfg.School.ServerDomain,
		ContactEmail: s.cfg.School.ContactEmail,
		LLMEndpoint:  s.cfg.GlobalEnv["LLM_ENDPOINT"],
		LLMAPIKey:    s.cfg.GlobalEnv["LLM_API_KEY"],
		DexAuthURL:   s.cfg.Dex.AuthURL,
		AutoUpdate:   s.cfg.AutoUpdate.Enabled,
		Message:      msg,
		Error:        errMsg,
	}
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	s.render(w, "settings.html.tmpl", s.settingsData("", ""))
}

func (s *Server) handleSettingsPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.render(w, "settings.html.tmpl", s.settingsData("", "Ungueltige Formulardaten."))
		return
	}

	schoolName := r.FormValue("school_name")
	serverDomain := r.FormValue("server_domain")
	contactEmail := r.FormValue("contact_email")
	llmEndpoint := r.FormValue("llm_endpoint")
	llmAPIKey := r.FormValue("llm_api_key")
	password := r.FormValue("password")
	passwordConfirm := r.FormValue("password_confirm")

	if schoolName == "" {
		s.render(w, "settings.html.tmpl", s.settingsData("", "Schulname ist erforderlich."))
		return
	}

	var newPassword string
	if password != "" || passwordConfirm != "" {
		if password != passwordConfirm {
			s.render(w, "settings.html.tmpl", s.settingsData("", "Passwoerter stimmen nicht ueberein."))
			return
		}
		if len(password) < 8 {
			s.render(w, "settings.html.tmpl", s.settingsData("", "Passwort muss mindestens 8 Zeichen lang sein."))
			return
		}
		hash, err := secrets.HashPassword(password)
		if err != nil {
			s.render(w, "settings.html.tmpl", s.settingsData("", "Passwort-Hash fehlgeschlagen."))
			return
		}
		s.cfg.Admin.PasswordHash = hash
		newPassword = password
	}

	s.cfg.School.Name = schoolName
	s.cfg.School.ServerDomain = serverDomain
	s.cfg.School.ContactEmail = contactEmail
	if s.cfg.GlobalEnv == nil {
		s.cfg.GlobalEnv = map[string]string{}
	}
	s.cfg.GlobalEnv["LLM_ENDPOINT"] = llmEndpoint
	s.cfg.GlobalEnv["LLM_API_KEY"] = llmAPIKey
	s.cfg.AutoUpdate.Enabled = r.FormValue("auto_update") == "on"

	if err := s.cfg.Save(); err != nil {
		log.Printf("web: save config from settings: %v", err)
		s.render(w, "settings.html.tmpl", s.settingsData("", "Konfiguration konnte nicht gespeichert werden."))
		return
	}

	// Propagate to .env.
	env, err := envfile.Load(paths.EnvFile())
	if err != nil {
		env = envfile.New()
	}
	envfile.ApplySystemEnv(env, s.cfg, newPassword)
	if llmEndpoint != "" {
		env.Set(envfile.GlobalSection, "LLM_ENDPOINT", llmEndpoint)
	}
	if llmAPIKey != "" {
		env.Set(envfile.GlobalSection, "LLM_API_KEY", llmAPIKey)
	}
	if err := env.Save(paths.EnvFile()); err != nil {
		log.Printf("web: save env from settings: %v", err)
	}

	// If the password changed, invalidate existing sessions so the new hash
	// has to be used. (Other admins logged in elsewhere get booted; this is
	// the right behavior for a single-admin tool.)
	if newPassword != "" {
		s.sessions.destroy()
	}

	s.render(w, "settings.html.tmpl", s.settingsData("Einstellungen gespeichert.", ""))
}
