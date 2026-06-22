// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package lock

import (
	"errors"
	"testing"

	"github.com/lngstck/stackctl/internal/paths"
)

func TestAcquireRejectsSecondHolder(t *testing.T) {
	t.Setenv(paths.EnvStackctlDir, t.TempDir())

	first, err := Acquire()
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}

	// A second Acquire (separate file description, even in the same process)
	// must be rejected with ErrBusy — this is what blocks a double-click and
	// the nightly timer from interleaving with the web server.
	if _, err := Acquire(); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Acquire: want ErrBusy, got %v", err)
	}

	// After release the lock is free again.
	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	second, err := Acquire()
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
}

func TestReleaseNilSafe(t *testing.T) {
	var h *Handle
	if err := h.Release(); err != nil {
		t.Fatalf("nil Release: %v", err)
	}
}
