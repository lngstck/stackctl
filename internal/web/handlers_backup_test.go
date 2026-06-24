// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRenderBackupsPage exercises template parsing AND execution of
// backups.html.tmpl with representative data — the other web tests build a
// bare Server and never render, so a bad field reference would otherwise only
// surface at runtime.
func TestRenderBackupsPage(t *testing.T) {
	s := &Server{}
	if err := s.loadTemplates(); err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}

	data := backupsData{
		PageData: PageData{NavActive: "backups", SchoolName: "Musterschule", SchoolSlug: "musterschule", CSRFToken: "tok"},
		Backups: []backupRow{{
			File:       "backup-musterschule-20260624-143000.tar.gz.age",
			CreatedAt:  "24.06.2026, 14:30",
			Size:       "1.2 GB",
			AppCount:   3,
			Encrypted:  true,
			Version:    "v0.7.0",
			IncludesDB: true,
		}},
	}

	rec := httptest.NewRecorder()
	s.render(rec, "backups.html.tmpl", data)

	if rec.Code != 200 {
		t.Fatalf("render status = %d, want 200; body:\n%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Backup erstellen",
		"backup-musterschule-20260624-143000.tar.gz.age",
		"/download",
		"tok",
		"/restore",        // restore form action
		"confirm_slug",    // slug confirmation field
		"musterschule",    // promoted SchoolSlug from embedded PageData
		"name=\"passphrase\"", // shown because the backup is encrypted
		"/backups/upload", // upload form
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
}
