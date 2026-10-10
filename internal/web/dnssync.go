// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/desec"
)

// newDeSEC builds the deSEC client. A variable so tests can point it at a
// fake API.
var newDeSEC = desec.New

// dnsSyncTimeout bounds one sync. Setup waits for it before redirecting.
const dnsSyncTimeout = 20 * time.Second

// dnsSyncStatus remembers the outcome of the last sync for the dashboard. A
// failure there means apps are unreachable, and the admin would otherwise
// only find out from the log.
type dnsSyncStatus struct {
	mu  sync.Mutex
	err string
}

func (d *dnsSyncStatus) set(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err == nil {
		d.err = ""
	} else {
		d.err = err.Error()
	}
}

func (d *dnsSyncStatus) get() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.err
}

// managesWildcard reports whether stackctl keeps *.{base_domain} at deSEC:
// in the school-network mode when the school handed its domain to deSEC.
// With a delegation target the school keeps its records at its own provider,
// and stackctl must not touch DNS at all.
func managesWildcard(cfg *config.Config) bool {
	return cfg.Public.Transport == config.TransportLocal &&
		cfg.Public.Local.ChallengeDomain == "" &&
		cfg.Public.Local.DNSToken != "" &&
		cfg.Public.BaseDomain != ""
}

// syncWildcard points *.{base_domain} at this server's address in the
// school network. It does nothing where stackctl does not manage the record,
// writes only on a difference, and records the outcome for the dashboard.
func (s *Server) syncWildcard(ctx context.Context) error {
	if !managesWildcard(s.cfg) {
		s.dnsSync.set(nil)
		return nil
	}
	err := s.doSyncWildcard(ctx)
	s.dnsSync.set(err)
	if err != nil {
		log.Printf("web: wildcard record at deSEC: %v", err)
	}
	return err
}

func (s *Server) doSyncWildcard(ctx context.Context) error {
	base := s.cfg.Public.BaseDomain
	ip := net.ParseIP(s.cfg.School.ServerDomain)
	if ip == nil {
		return fmt.Errorf("die Server-IP in den Einstellungen (%q) ist keine IP-Adresse — *.%s braucht eine", s.cfg.School.ServerDomain, base)
	}

	ctx, cancel := context.WithTimeout(ctx, dnsSyncTimeout)
	defer cancel()
	changed, err := newDeSEC(s.cfg.Public.Local.DNSToken).EnsureWildcard(ctx, base, ip)
	if err != nil {
		return fmt.Errorf("*.%s bei deSEC nicht gesetzt: %w", base, err)
	}
	if changed {
		log.Printf("web: *.%s → %s bei deSEC gesetzt", base, ip)
	}
	return nil
}
