// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package publish

import (
	"errors"
	"log"

	"github.com/lngstck/stackctl/internal/config"
)

// For returns the publisher that config.public.transport asks for.
//
// It never returns nil and never fails: an install whose transport this build
// cannot serve still gets a Publisher, one that refuses every operation with a
// clear error. Refusing to start instead would take the admin web UI down with
// it — and the UI is exactly where the admin would have to go to fix the
// setting.
func For(cfg *config.Config) Publisher {
	switch cfg.Public.Transport {
	case config.TransportDirect:
		return NewDirect(cfg)
	case config.TransportRelay, "":
		// An install whose transport was never written down is a relay —
		// that is the historical default, not an error.
		return NewRelay(cfg)
	default:
		log.Printf("publish: unknown transport %q — apps stay unpublished", cfg.Public.Transport)
		return unsupported{kind: cfg.Public.Transport}
	}
}

// unsupported stands in for a transport this build does not implement — a
// config from a newer stackctl, or a hand-edited value. It keeps the rest of
// stackctl running and makes the gap visible in the UI instead of crashing or
// silently publishing nothing.
type unsupported struct{ kind string }

var errUnsupported = errors.New("diese Betriebsart wird von dieser stackctl-Version nicht unterstuetzt")

func (u unsupported) Kind() string               { return u.kind }
func (u unsupported) EnsureAuth() error          { return errUnsupported }
func (u unsupported) AuthStatus() string         { return StatusError }
func (u unsupported) StartAuth() error           { return errUnsupported }
func (u unsupported) StopAuth() error            { return nil }
func (u unsupported) StartAdmin(int) error       { return errUnsupported }
func (u unsupported) StopAdmin() error           { return nil }
func (u unsupported) AdminStatus() string        { return StatusError }
func (u unsupported) Enable(App) (string, error) { return "", errUnsupported }
func (u unsupported) Disable(string) error       { return nil }
func (u unsupported) Restore([]App)              {}
func (u unsupported) Status(string) string       { return StatusError }
func (u unsupported) StartMonitor()              {}
func (u unsupported) Shutdown()                  {}
