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
}

func (s *Server) handleJobPage(w http.ResponseWriter, r *http.Request) {
	job, ok := s.jobs.get(r.PathValue("id"))
	if !ok {
		http.Redirect(w, r, "/apps?msg=Vorgang+nicht+gefunden&err=1", http.StatusSeeOther)
		return
	}
	s.render(w, "job.html.tmpl", jobPageData{
		PageData: s.pageData("apps"),
		Job:      job.snapshot(),
	})
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
