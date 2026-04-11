package web

import (
	"net/http"
)

// Stub handlers for pages that will be fully implemented in later steps.
// They render real templates but with minimal/placeholder data.

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	s.render(w, "settings.html.tmpl", s.pageData("settings"))
}

func (s *Server) handleSettingsPost(w http.ResponseWriter, r *http.Request) {
	// TODO: implement settings save
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
