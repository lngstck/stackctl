package web

import (
	"fmt"
	"log"
	"net/http"

	"github.com/lngstck/stackctl/internal/catalog"
	"github.com/lngstck/stackctl/internal/update"
)

// handleSystemUpdate fuehrt den stackctl-Self-Update durch. Die ehemals
// eigene /system-Seite ist in /settings (Tab "System") integriert (Issue #4),
// daher landen Erfolg/Fehler dort als Flash-Message.
func (s *Server) handleSystemUpdate(w http.ResponseWriter, r *http.Request) {
	result, err := update.Check()
	if err != nil {
		http.Redirect(w, r, "/settings?update="+fmt.Sprintf("Update-Check fehlgeschlagen: %v", err)+"&err=1", http.StatusSeeOther)
		return
	}
	if !result.UpdateAvailable {
		http.Redirect(w, r, "/settings?update=Bereits+aktuell", http.StatusSeeOther)
		return
	}

	newVersion, err := update.Apply(result.Release)
	if err != nil {
		log.Printf("web: self-update: %v", err)
		http.Redirect(w, r, "/settings?update="+fmt.Sprintf("Update fehlgeschlagen: %v", err)+"&err=1", http.StatusSeeOther)
		return
	}

	log.Printf("web: updated to %s, restarting...", newVersion)

	// Im Dev-Modus (kein systemd-Wrapper) wuerde os.Exit den Prozess tot
	// lassen — Hinweis statt Restart. Siehe PR #11 (Issue #10). In der
	// Vor-PR-#11-Welt gab RestartService einen sudo-Fehler zurueck und
	// dieser Pfad hat die "manuell neu starten"-Meldung gezeigt; das
	// bleibt funktional aequivalent, nur Dev-spezifischer formuliert.
	if s.devMode {
		http.Redirect(w, r, "/settings?update="+fmt.Sprintf("Update auf %s erfolgreich. Im Dev-Modus bitte manuell neu starten.", newVersion), http.StatusSeeOther)
		return
	}

	_ = update.RestartService()
	http.Redirect(w, r, "/settings?update="+fmt.Sprintf("Update auf %s — Neustart...", newVersion), http.StatusSeeOther)
}

// handleCatalogSync synchronisiert den App-Katalog. Wie zuvor entscheidet
// der Caller via Hidden-Field `return` ueber das Redirect-Ziel — Default ist
// /settings (vorher /system); /apps benutzt /apps mit eigenem msg-Param.
func (s *Server) handleCatalogSync(w http.ResponseWriter, r *http.Request) {
	returnTo := r.FormValue("return")
	if returnTo == "" {
		returnTo = "/settings"
	}
	// Whitelist statt freier Redirect-Akzeptanz: nur In-App-Pfade.
	if returnTo[0] != '/' || (len(returnTo) > 1 && returnTo[1] == '/') {
		returnTo = "/settings"
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
	if returnTo == "/settings" {
		msg = "ok"
	}
	http.Redirect(w, r, fmt.Sprintf("%s?%s=%s", returnTo, param, msg), http.StatusSeeOther)
}
