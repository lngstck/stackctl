// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"encoding/json"
	"net/http"
)

// jobPageData is the template context for job.html.tmpl. The page renders the
// initial snapshot server-side, then a small script polls /jobs/{id}/status to
// keep the checklist, log and result in sync until the job finishes.
type jobPageData struct {
	PageData
	Job jobSnapshot
	// BootID of the process serving this page. The restart-detection script
	// compares it against /healthz to know when a *new* process is up.
	BootID string
}

func (s *Server) handleJobPage(w http.ResponseWriter, r *http.Request) {
	job, ok := s.jobs.get(r.PathValue("id"))
	if !ok {
		http.Redirect(w, r, "/apps?msg=Vorgang+nicht+gefunden&err=1", http.StatusSeeOther)
		return
	}
	s.render(w, "job.html.tmpl", jobPageData{
		PageData: s.pageData(navForJobKind(job.Kind)),
		Job:      job.snapshot(),
		BootID:   bootID,
	})
}

// navForJobKind maps a job kind to the sidebar section it belongs to, so the
// active nav entry matches the operation in progress (a backup highlights
// "Sicherung", not "Apps").
func navForJobKind(kind string) string {
	switch kind {
	case "backup", "restore":
		return "backups"
	case "selfupdate":
		return "settings"
	default: // install, update
		return "apps"
	}
}

func (s *Server) handleJobStatus(w http.ResponseWriter, r *http.Request) {
	job, ok := s.jobs.get(r.PathValue("id"))
	if !ok {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(job.snapshot())
}
