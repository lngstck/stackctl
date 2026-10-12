// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/lngstck/stackctl/internal/catalog"
	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/dex"
	"github.com/lngstck/stackctl/internal/envfile"
	"github.com/lngstck/stackctl/internal/install"
	"github.com/lngstck/stackctl/internal/lock"
	"github.com/lngstck/stackctl/internal/public"
)

// The basic services — database, login, proxy — are one task for the admin,
// not three: every school needs all of them, they take no settings, and no
// app works before they run. stackctl therefore installs them in a single
// job, "Grundversorgung einrichten", right after setup and from one button
// on the start screen whenever one of them is missing.

// infraJobKind marks that job, so the start screen can tell it is running.
const infraJobKind = "infra"

// missingInfraIDs lists the basic services not yet installed, in install
// order (mandatoryAppIDs).
func missingInfraIDs(st *config.State) []string {
	var missing []string
	for _, id := range mandatoryAppIDs() {
		if _, installed := st.Containers[id]; !installed {
			missing = append(missing, id)
		}
	}
	return missing
}

// infraJobRunning reports whether a "Grundversorgung einrichten" job is
// still at work. The start screen then shows its progress instead of asking
// for the same thing again.
func (s *Server) infraJobRunning() bool {
	if s.jobs == nil {
		return false
	}
	for _, a := range s.jobs.recent(maxRetainedJobs) {
		if !a.Done && a.Kind == infraJobKind {
			return true
		}
	}
	return false
}

// handleInfraInstall starts the job from the start screen.
func (s *Server) handleInfraInstall(w http.ResponseWriter, r *http.Request) {
	h, ok := s.tryLock(w, r)
	if !ok {
		return
	}
	job := s.startInfraJob(h)
	if job == nil {
		h.Release()
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/jobs/"+job.ID, http.StatusSeeOther)
}

// startInfraJob starts installing whatever basic service is missing and
// hands h to the job, which releases it. Nothing missing, no job: it returns
// nil and the caller keeps h.
func (s *Server) startInfraJob(h *lock.Handle) *Job {
	missing := missingInfraIDs(s.snapState())
	if len(missing) == 0 || s.jobs == nil {
		return nil
	}
	job := s.jobs.create(infraJobKind, "", "Grundversorgung einrichten", "/")
	run := s.infraRunner
	if run == nil {
		run = s.runInfraJob
	}
	go run(h, job, missing)
	return job
}

// runInfraJob installs the given services one after the other. Each one is a
// step of the job; what the install does inside goes to the log. It stops at
// the first failure: what is installed by then stays, and the start screen
// offers the rest again.
func (s *Server) runInfraJob(h *lock.Handle, job *Job, ids []string) {
	defer h.Release()
	var messages []string
	for _, id := range ids {
		job.Step(infraShortNames[id] + " installieren")
		def, err := catalog.GetOrFetch(s.cfg.Catalog.URL, id)
		if err != nil {
			job.finish(false, fmt.Sprintf("%s: Beschreibung im Katalog nicht abrufbar: %v", infraShortNames[id], err))
			return
		}
		inner := logOnly{job}
		result, notes, errMsg := s.applyAppOp(job, "install", id, func(working *config.State, env *envfile.File, allDefs []*catalog.Definition, dexClients []dex.Client) (*install.Result, []dex.Client, error) {
			return install.Install(def, s.cfg, working, env, dexClients, allDefs, map[string]string{}, inner)
		})
		if result == nil || !result.Success {
			if errMsg == "" {
				errMsg = "unbekannter Fehler"
			}
			job.finish(false, infraShortNames[id]+": "+errMsg)
			return
		}
		// The catalog's messages (ports, bootstrap user) are for the record,
		// not for the admin; stackctl's own notes (the admin address) are.
		for _, m := range result.Messages {
			job.Log(m)
		}
		messages = append(messages, notes...)
	}
	job.setResult(nil, append(infraSummary(s.cfg), messages...))
	job.finish(true, "")
}

// infraSummary is what the admin reads when the basic services are in.
func infraSummary(cfg *config.Config) []string {
	lines := []string{"Datenbank, Anmeldung und Webzugang laufen. Jetzt lassen sich Apps aus dem Katalog installieren."}
	if cfg.Public.Transport == config.TransportLocal {
		lines = append(lines, fmt.Sprintf("Das Zertifikat für *.%s holt der Webzugang jetzt über deSEC. Das dauert meist 5 bis 10 Minuten, bis dahin melden Browser einen Zertifikatsfehler.", public.BaseDomain(cfg)))
	} else {
		lines = append(lines, "Die Zertifikate holt der Webzugang beim ersten Aufruf einer Adresse.")
	}
	return lines
}

// logOnly turns an install's steps into log lines, so one job can carry
// several installs with one step each.
type logOnly struct{ job *Job }

func (l logOnly) Step(name string) { l.job.Log("· " + name) }
func (l logOnly) Log(line string)  { l.job.Log(line) }

// startInfraAfterSetup begins the job right when setup is done. A busy lock
// (an unlikely nightly update) just means the start screen offers it.
func (s *Server) startInfraAfterSetup() *Job {
	h, err := lock.Acquire()
	if err != nil {
		if !errors.Is(err, lock.ErrBusy) {
			log.Printf("web: acquire op lock after setup: %v", err)
		}
		return nil
	}
	job := s.startInfraJob(h)
	if job == nil {
		h.Release()
	}
	return job
}
