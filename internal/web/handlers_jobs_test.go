// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
