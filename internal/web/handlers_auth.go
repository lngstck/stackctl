package web

import (
	"net/http"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/secrets"
)

// loginData is the template context for login.html.tmpl.
type loginData struct {
	Error string
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.cfg.SetupState != config.SetupStateReady {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}

	// Already logged in?
	if s.sessions.valid(getSessionToken(r)) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	s.render(w, "login.html.tmpl", loginData{})
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	if s.cfg.SetupState != config.SetupStateReady {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}

	ip := clientIP(r)
	if s.limiter.isLocked(ip) {
		s.render(w, "login.html.tmpl", loginData{
			Error: "Zu viele Fehlversuche. Bitte warte eine Minute.",
		})
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Ungueltige Formulardaten", http.StatusBadRequest)
		return
	}

	password := r.FormValue("password")
	if !secrets.VerifyPassword(s.cfg.Admin.PasswordHash, password) {
		s.limiter.recordFailure(ip)
		s.render(w, "login.html.tmpl", loginData{
			Error: "Falsches Passwort.",
		})
		return
	}

	s.limiter.recordSuccess(ip)
	token := s.sessions.create()
	setSessionCookie(w, token)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.sessions.destroy()
	clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
