package web

import (
	"fmt"
	"log"
	"net/http"

	"github.com/lngstck/stackctl/internal/catalog"
	"github.com/lngstck/stackctl/internal/update"
)

// systemData is the template context for system.html.tmpl.
type systemData struct {
	PageData
	CurrentVersion  string
	LatestVersion   string
	UpdateAvailable bool
	CheckError      string
	UpdateResult    string
	UpdateError     string
	SyncResult      string
	SyncError       string
}

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	data := systemData{
		PageData:       s.pageData("system"),
		CurrentVersion: update.CurrentVersion(),
	}

	// Check for updates (non-blocking — errors are shown in the UI).
	result, err := update.Check()
	if err != nil {
		data.CheckError = err.Error()
	} else {
		data.LatestVersion = result.LatestVersion
		data.UpdateAvailable = result.UpdateAvailable
	}

	// Flash messages from POST actions.
	if msg := r.URL.Query().Get("sync"); msg != "" {
		if msg == "ok" {
			data.SyncResult = "Katalog erfolgreich synchronisiert."
		} else {
			data.SyncError = msg
		}
	}
	if msg := r.URL.Query().Get("update"); msg != "" {
		if r.URL.Query().Get("err") == "1" {
			data.UpdateError = msg
		} else {
			data.UpdateResult = msg
		}
	}

	s.render(w, "system.html.tmpl", data)
}

func (s *Server) handleSystemUpdate(w http.ResponseWriter, r *http.Request) {
	result, err := update.Check()
	if err != nil {
		http.Redirect(w, r, "/system?update="+fmt.Sprintf("Update-Check fehlgeschlagen: %v", err)+"&err=1", http.StatusSeeOther)
		return
	}
	if !result.UpdateAvailable {
		http.Redirect(w, r, "/system?update=Bereits+aktuell", http.StatusSeeOther)
		return
	}

	newVersion, err := update.Apply(result.Release)
	if err != nil {
		log.Printf("web: self-update: %v", err)
		http.Redirect(w, r, "/system?update="+fmt.Sprintf("Update fehlgeschlagen: %v", err)+"&err=1", http.StatusSeeOther)
		return
	}

	log.Printf("web: updated to %s, restarting...", newVersion)

	// Try systemd restart. If it fails (dev mode, no systemd), just redirect.
	if err := update.RestartService(); err != nil {
		http.Redirect(w, r, "/system?update="+fmt.Sprintf("Update auf %s erfolgreich. Bitte manuell neu starten.", newVersion), http.StatusSeeOther)
		return
	}

	// If systemctl restart works, this process will be killed.
	// The browser will briefly see an error, then the new version serves.
	http.Redirect(w, r, "/system?update="+fmt.Sprintf("Update auf %s — Neustart...", newVersion), http.StatusSeeOther)
}

func (s *Server) handleCatalogSync(w http.ResponseWriter, r *http.Request) {
	_, err := catalog.Sync(s.cfg.Catalog.URL)
	if err != nil {
		log.Printf("web: catalog sync: %v", err)
		http.Redirect(w, r, "/system?sync="+fmt.Sprintf("Fehler: %v", err), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/system?sync=ok", http.StatusSeeOther)
}
