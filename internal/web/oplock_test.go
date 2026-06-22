// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lngstck/stackctl/internal/lock"
	"github.com/lngstck/stackctl/internal/paths"
)

// When the operation lock is already held (e.g. the nightly autoupdate timer is
// running), a mutating web request must not execute its handler. It redirects
// back with an error notice instead of corrupting state.
func TestWithOpLockBusyDoesNotRunHandler(t *testing.T) {
	t.Setenv(paths.EnvStackctlDir, t.TempDir())

	held, err := lock.Acquire()
	if err != nil {
		t.Fatalf("pre-acquire lock: %v", err)
	}
	defer held.Release()

	called := false
	s := &Server{}
	h := s.withOpLock(func(http.ResponseWriter, *http.Request) { called = true })

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/apps/x/install", nil))

	if called {
		t.Fatal("handler ran while lock was held; expected it to be skipped")
	}
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status: want %d, got %d", http.StatusSeeOther, rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc == "" || !contains(loc, "err=1") {
		t.Fatalf("redirect location: want busy notice with err=1, got %q", loc)
	}
}

// When the lock is free, the wrapped handler runs and the lock is released
// afterwards (a follow-up Acquire succeeds).
func TestWithOpLockFreeRunsHandlerAndReleases(t *testing.T) {
	t.Setenv(paths.EnvStackctlDir, t.TempDir())

	called := false
	s := &Server{}
	h := s.withOpLock(func(http.ResponseWriter, *http.Request) { called = true })

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/apps/x/install", nil))

	if !called {
		t.Fatal("handler did not run with a free lock")
	}
	after, err := lock.Acquire()
	if err != nil {
		t.Fatalf("lock not released after handler: %v", err)
	}
	_ = after.Release()
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
