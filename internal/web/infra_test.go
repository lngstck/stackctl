// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/lock"
	"github.com/lngstck/stackctl/internal/publish"
)

// Der Knopf auf Start installiert genau das Fehlende, in fester Reihenfolge,
// und führt zum Fortschritt. Fehlt nichts, geht es zurück nach Start.
func TestInfraInstallButton(t *testing.T) {
	s, st := testServerWithPublisher(t, &fakePublisher{})
	s.jobs = newJobStore()
	ran := make(chan []string, 1)
	s.infraRunner = func(h *lock.Handle, job *Job, ids []string) {
		defer h.Release()
		job.finish(true, "")
		ran <- ids
	}
	st.Containers["dex"] = &config.ContainerState{ID: "dex"}

	post := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.handleInfraInstall(rec, httptest.NewRequest(http.MethodPost, "/system/infra", nil))
		return rec
	}

	rec := post()
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/jobs/") {
		t.Fatalf("Location = %q, want /jobs/…", loc)
	}
	if ids := <-ran; strings.Join(ids, ",") != "postgres,caddy" {
		t.Errorf("installiert %v, want postgres,caddy", ids)
	}

	st.Containers["postgres"] = &config.ContainerState{ID: "postgres"}
	st.Containers["caddy"] = &config.ContainerState{ID: "caddy"}
	if loc := post().Header().Get("Location"); loc != "/" {
		t.Errorf("nichts fehlt: Location = %q, want /", loc)
	}
}

// Solange Anmeldung oder Webzugang fehlen, ist „Anmeldung nicht erreichbar“
// nur die Folge davon. Start zeigt dann allein die Einrichtung.
func TestStartShowsOnlyInfraWhileMissing(t *testing.T) {
	fake := &fakePublisher{authStatus: publish.StatusStopped}
	s, st := testServerWithPublisher(t, fake)
	s.jobs = newJobStore()
	delete(st.Containers, "pylearn")
	titles := func() string {
		var t []string
		for _, n := range s.buildStart(httptest.NewRequest(http.MethodGet, "/", nil)).Notices {
			t = append(t, n.Title)
		}
		return strings.Join(t, " | ")
	}

	if got := titles(); got != "Grundversorgung einrichten" {
		t.Errorf("frisch: Hinweise = %q", got)
	}

	for _, id := range mandatoryAppIDs() {
		st.Containers[id] = &config.ContainerState{ID: id}
	}
	saved := appRunning
	appRunning = func(string) bool { return true }
	defer func() { appRunning = saved }()
	if got := titles(); !strings.Contains(got, "Anmeldung nicht erreichbar") || strings.Contains(got, "Grundversorgung") {
		t.Errorf("alles installiert, Login aus: Hinweise = %q", got)
	}
}
