// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/paths"
	"github.com/lngstck/stackctl/internal/publish"
)

// fakePublisher records what it was asked to do and never touches state —
// which is the property under test: the publisher publishes, the handler
// records.
type fakePublisher struct {
	enabled   []publish.App
	disabled  []string
	enableErr error
	host      string
}

func (f *fakePublisher) Kind() string       { return publish.KindRelay }
func (f *fakePublisher) EnsureAuth() error  { return nil }
func (f *fakePublisher) AuthStatus() string { return publish.StatusRunning }
func (f *fakePublisher) StartAuth() error   { return nil }
func (f *fakePublisher) StopAuth() error    { return nil }
func (f *fakePublisher) Enable(app publish.App) (string, error) {
	if f.enableErr != nil {
		return "", f.enableErr
	}
	f.enabled = append(f.enabled, app)
	return f.host, nil
}
func (f *fakePublisher) Disable(appID string) error {
	f.disabled = append(f.disabled, appID)
	return nil
}
func (f *fakePublisher) Restore([]publish.App) {}
func (f *fakePublisher) Status(string) string  { return publish.StatusRunning }
func (f *fakePublisher) StartMonitor()         {}
func (f *fakePublisher) Shutdown()             {}

func testServerWithPublisher(t *testing.T, p publish.Publisher) (*Server, *config.State) {
	t.Helper()
	t.Setenv(paths.EnvStackctlDir, t.TempDir())

	st := config.NewState()
	st.Containers["pylearn"] = &config.ContainerState{
		ID: "pylearn", Name: "PyLearn", Ports: []int{8330},
	}
	return &Server{
		cfg: &config.Config{
			School: config.School{Slug: "phoenix"},
			Public: config.Public{
				Transport:  config.TransportRelay,
				BaseDomain: "phoenix.learningstack.online",
			},
		},
		state:     st,
		publisher: p,
	}, st
}

func postTo(handler http.HandlerFunc, path, appID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.SetPathValue("id", appID)
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

// Enabling publishes the app and writes the host the publisher reported —
// the publisher itself must not have written anything.
func TestAppTunnelEnableRecordsPublisherResult(t *testing.T) {
	fake := &fakePublisher{host: "pylearn.phoenix.learningstack.online"}
	s, _ := testServerWithPublisher(t, fake)

	rec := postTo(s.handleAppTunnelEnable, "/apps/pylearn/tunnel/enable", "pylearn")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	if len(fake.enabled) != 1 {
		t.Fatalf("publisher.Enable calls = %d, want 1", len(fake.enabled))
	}
	if got := fake.enabled[0]; got.ID != "pylearn" || got.LocalPort != 8330 {
		t.Errorf("published %+v, want pylearn on 8330", got)
	}

	cs := s.snapState().Containers["pylearn"]
	if !cs.PublicEnabled {
		t.Error("state should record the app as public")
	}
	if cs.PublicHost != fake.host {
		t.Errorf("PublicHost = %q, want %q — the host must come from the publisher, not be rebuilt", cs.PublicHost, fake.host)
	}
}

// If publishing fails, nothing may be recorded: a state that claims an app is
// public while it is not is worse than a visible failure.
func TestAppTunnelEnableLeavesStateAloneOnFailure(t *testing.T) {
	fake := &fakePublisher{enableErr: errFake}
	s, _ := testServerWithPublisher(t, fake)

	postTo(s.handleAppTunnelEnable, "/apps/pylearn/tunnel/enable", "pylearn")

	if cs := s.snapState().Containers["pylearn"]; cs.PublicEnabled || cs.PublicHost != "" {
		t.Errorf("failed publish must not touch state: %+v", cs)
	}
}

func TestAppTunnelDisableClearsState(t *testing.T) {
	fake := &fakePublisher{}
	s, _ := testServerWithPublisher(t, fake)
	working := s.snapState()
	working.Containers["pylearn"].PublicEnabled = true
	working.Containers["pylearn"].PublicHost = "pylearn.phoenix.learningstack.online"
	if err := s.commitState(working); err != nil {
		t.Fatalf("seed commit: %v", err)
	}

	postTo(s.handleAppTunnelDisable, "/apps/pylearn/tunnel/disable", "pylearn")

	if len(fake.disabled) != 1 || fake.disabled[0] != "pylearn" {
		t.Errorf("publisher.Disable calls = %v", fake.disabled)
	}
	cs := s.snapState().Containers["pylearn"]
	if cs.PublicEnabled || cs.PublicHost != "" {
		t.Errorf("state should be cleared: %+v", cs)
	}
}

// A publisher that cannot test its transport must not offer the button.
func TestTunnelTestRejectsPublisherWithoutTransportTest(t *testing.T) {
	s, _ := testServerWithPublisher(t, &fakePublisher{})

	rec := postTo(s.handleTunnelTest, "/tunnel/test", "")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc == "/tunnel?test=ok" {
		t.Error("a publisher without a transport test must not report success")
	}
}

type fakeErr string

func (e fakeErr) Error() string { return string(e) }

const errFake = fakeErr("publish failed")
