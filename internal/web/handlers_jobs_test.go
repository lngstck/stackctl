// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRenderJobPageQuotesValues guards against the regression where the job
// page emitted `var jobID = c635…;` (jsString escapes but does not quote), so a
// hex id starting with a letter was read as an undefined identifier and crashed
// the whole polling script — freezing every job page at "läuft… 0:00".
func TestRenderJobPageQuotesValues(t *testing.T) {
	s := &Server{}
	if err := s.loadTemplates(); err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}
	rec := httptest.NewRecorder()
	s.render(rec, "job.html.tmpl", jobPageData{
		PageData: PageData{NavActive: "apps", CSRFToken: "tok"},
		Job:      jobSnapshot{ID: "c635f878815ad543", Kind: "backup", Title: "Backup erstellen", BackURL: "/backups"},
	})
	if rec.Code != 200 {
		t.Fatalf("render status = %d; body:\n%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "var jobID = 'c635f878815ad543';") {
		t.Errorf("job id not emitted as a quoted JS string literal")
	}
	if strings.Contains(body, "var jobID = c635f878815ad543;") {
		t.Errorf("job id emitted unquoted — would crash the polling script")
	}
	// backURL may contain html/template's defensive \/ escaping (valid JS); just
	// assert each is opened with a quote rather than a bare token.
	for _, want := range []string{"var jobKind = '", "var backURL = '"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing quoted assignment %q", want)
		}
	}
}

func TestNavForJobKind(t *testing.T) {
	cases := map[string]string{
		"backup":     "backups",
		"restore":    "backups",
		"selfupdate": "settings",
		"install":    "apps",
		"update":     "apps",
		"":           "apps",
	}
	for kind, want := range cases {
		if got := navForJobKind(kind); got != want {
			t.Errorf("navForJobKind(%q) = %q, want %q", kind, got, want)
		}
	}
}

func TestHandleJobStatusNotFound(t *testing.T) {
	s := &Server{jobs: newJobStore()}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/jobs/nope/status", nil)
	r.SetPathValue("id", "nope")
	s.handleJobStatus(rec, r)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d", rec.Code)
	}
}

func TestHandleJobStatusReturnsSnapshot(t *testing.T) {
	s := &Server{jobs: newJobStore()}
	job := s.jobs.create("install", "open-webui", "Installiere Open WebUI", "/apps/open-webui")
	job.Step("Vorbereitung & Secrets")
	job.Log("pulling image")

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/jobs/"+job.ID+"/status", nil)
	r.SetPathValue("id", job.ID)
	s.handleJobStatus(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type: want application/json, got %q", ct)
	}
	var snap jobSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if snap.Kind != "install" || snap.Title != "Installiere Open WebUI" {
		t.Errorf("unexpected snapshot: %+v", snap)
	}
	if len(snap.Steps) != 1 || snap.Steps[0].Status != StepRunning {
		t.Errorf("steps: %+v", snap.Steps)
	}
	if len(snap.Log) != 1 || snap.Log[0] != "pulling image" {
		t.Errorf("log: %+v", snap.Log)
	}
}
