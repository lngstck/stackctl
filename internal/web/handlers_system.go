package web

import (
	"fmt"
	"log"
	"net/http"

	"github.com/lngstck/stackctl/internal/catalog"
	"github.com/lngstck/stackctl/internal/lock"
	"github.com/lngstck/stackctl/internal/update"
)

// handleSystemUpdate fuehrt den stackctl-Self-Update als asynchronen Job durch,
// sodass die Settings-Seite auf eine Live-Verlaufsanzeige (/jobs/{id}) umleitet
// statt den Request minutenlang zu blocken (Issue #1). Der Job haelt den
// Op-Lock (Binary-Replace vs. App-Install); in Produktion endet er mit einem
// Self-Exit, den systemd auffaengt.
func (s *Server) handleSystemUpdate(w http.ResponseWriter, r *http.Request) {
	h, ok := s.tryLock(w, r)
	if !ok {
		return
	}
	job := s.jobs.create("selfupdate", "", "stackctl aktualisieren", "/settings")
	go s.runSelfUpdateJob(h, job)
	http.Redirect(w, r, "/jobs/"+job.ID, http.StatusSeeOther)
}

// runSelfUpdateJob is the worker for a stackctl self-update. In production a
// successful update schedules a process exit (systemd restarts with the new
// binary); the job page detects the restart and waits on /healthz.
func (s *Server) runSelfUpdateJob(h *lock.Handle, job *Job) {
	defer h.Release()

	job.Step("Nach Updates suchen")
	result, err := update.Check()
	if err != nil {
		job.finish(false, fmt.Sprintf("Update-Check fehlgeschlagen: %v", err))
		return
	}
	if !result.UpdateAvailable {
		job.setResult(nil, []string{"stackctl ist bereits aktuell — kein Update noetig."})
		job.finish(true, "")
		return
	}

	job.Step("Herunterladen & Pruefsumme verifizieren")
	newVersion, err := update.Apply(result.Release)
	if err != nil {
		log.Printf("web: self-update: %v", err)
		job.finish(false, fmt.Sprintf("Update fehlgeschlagen: %v", err))
		return
	}
	log.Printf("web: updated to %s", newVersion)

	if s.devMode {
		// Im Dev-Modus laeuft stackctl ohne systemd-Wrapper — kein os.Exit.
		job.setResult(nil, []string{fmt.Sprintf("Update auf %s erfolgreich. Im Dev-Modus bitte manuell neu starten.", newVersion)})
		job.setSelfUpdate(newVersion, false)
		job.finish(true, "")
		return
	}

	job.Step("Neustart")
	job.setSelfUpdate(newVersion, true)
	job.finish(true, "")
	// RestartService schedules os.Exit(0); systemd brings stackctl back up with
	// the new binary. The job page switches to /healthz polling on the
	// restarting flag (or when polls start failing).
	_ = update.RestartService()
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
