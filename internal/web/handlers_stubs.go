package web

import (
	"fmt"
	"log"
	"net/http"

	"github.com/lngstck/stackctl/internal/catalog"
)

// Stub handlers for pages that will be fully implemented in later steps.
// They render real templates but with minimal/placeholder data.

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	s.render(w, "settings.html.tmpl", s.pageData("settings"))
}

func (s *Server) handleSettingsPost(w http.ResponseWriter, r *http.Request) {
	// TODO: Schritt 8
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	s.render(w, "system.html.tmpl", s.pageData("system"))
}

func (s *Server) handleCatalogSync(w http.ResponseWriter, r *http.Request) {
	_, err := catalog.Sync(s.cfg.Catalog.URL)
	if err != nil {
		log.Printf("web: catalog sync: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `{"ok":false,"error":%q}`, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"ok":true}`)
}
