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

	// Im Dev-Modus laeuft stackctl ohne systemd-Wrapper — os.Exit wuerde den
	// Prozess tot lassen. Stattdessen Hinweis "manuell starten" zeigen.
	if s.devMode {
		http.Redirect(w, r, "/system?update="+fmt.Sprintf("Update auf %s erfolgreich. Im Dev-Modus bitte manuell neu starten.", newVersion), http.StatusSeeOther)
		return
	}

	// Produktion: RestartService spawnt eine Goroutine, die nach kurzer
	// Pause os.Exit(0) ruft — systemd bringt uns mit dem neuen Binary
	// wieder hoch (Restart=always). Diese Antwort sieht der Browser noch.
	_ = update.RestartService()
	http.Redirect(w, r, "/system?update="+fmt.Sprintf("Update auf %s — Neustart...", newVersion), http.StatusSeeOther)
}

func (s *Server) handleCatalogSync(w http.ResponseWriter, r *http.Request) {
	// Caller darf via ?return=... bestimmen, wo nach dem Sync gelandet wird.
	// Per Default: /system (alte URL, falls noch jemand direkt postet).
	returnTo := r.FormValue("return")
	if returnTo == "" {
		returnTo = "/system"
	}
	// Whitelist statt freier Redirect-Akzeptanz: nur In-App-Pfade.
	if returnTo[0] != '/' || (len(returnTo) > 1 && returnTo[1] == '/') {
		returnTo = "/system"
	}

	param := "sync"
	if returnTo == "/apps" {
		param = "msg"
	}

	_, err := catalog.Sync(s.cfg.Catalog.URL)
	if err != nil {
		log.Printf("web: catalog sync: %v", err)
		http.Redirect(w, r, fmt.Sprintf("%s?%s=%s&err=1", returnTo, param, "Katalog-Sync+fehlgeschlagen"), http.StatusSeeOther)
		return
	}
	msg := "Katalog+aktualisiert"
	if returnTo == "/system" {
		msg = "ok"
	}
	http.Redirect(w, r, fmt.Sprintf("%s?%s=%s", returnTo, param, msg), http.StatusSeeOther)
}
