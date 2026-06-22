// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

// Package lock provides a cross-process advisory lock that serialises
// infrastructure-mutating operations.
//
// Two writers can race on stackctl's state: the long-running web server (an
// admin clicking Install/Update/Remove) and the nightly `stackctl autoupdate`
// command, which runs as its own process from a systemd timer. Both rewrite
// the same files — state.yaml, the compose .env and docker-compose.yml. A
// single atomic write per file (paths.AtomicWrite) keeps each file consistent,
// but does not protect a whole read-modify-write install flow from
// interleaving with another. This lock closes that gap.
//
// It uses a non-blocking flock(2) on a single lock file. flock associates the
// lock with the open file description, and independent descriptors conflict
// even within the same process — so the same lock also rejects a second
// concurrent operation triggered by a double-click in the web UI, without an
// extra in-process mutex. Contention is surfaced as ErrBusy; callers decide
// whether to tell the admin "try again in a moment" or simply skip (the timer).
package lock

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/lngstck/stackctl/internal/paths"
)

// ErrBusy is returned by Acquire when another holder (the autoupdate timer, or
// a concurrent web request) currently owns the operation lock.
var ErrBusy = errors.New("operation lock held by another process")

// Handle is an acquired operation lock. Release it with Release when the
// mutating operation finishes.
type Handle struct {
	f *os.File
}

// File returns the path of the advisory lock file. It lives next to the
// binary under STACKCTL_DIR (honours the dev override) and is never read or
// written — only flock'd.
func File() string {
	return filepath.Join(paths.StackctlDir(), "op.lock")
}

// Acquire takes a non-blocking exclusive advisory lock. It returns ErrBusy if
// the lock is already held (by this process or another). On success the caller
// must Release the returned Handle; closing the file descriptor on process
// exit also drops the lock, so a crashing holder never wedges it permanently.
func Acquire() (*Handle, error) {
	path := File()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o640)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return &Handle{f: f}, nil
}

// Release unlocks and closes the lock file. It is safe to call on a nil Handle
// and idempotent.
func (h *Handle) Release() error {
	if h == nil || h.f == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(h.f.Fd()), syscall.LOCK_UN)
	closeErr := h.f.Close()
	h.f = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
