// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

// Package preflight checks whether the prerequisites for a chosen operating
// mode are actually in place.
//
// The three modes put the work in different hands. On the operator relay the
// school does nothing: DNS and TLS belong to the operator. With the school's
// own domain the admin has to create records at their DNS provider, and in
// direct operation the server additionally has to own ports 80 and 443 and be
// reachable from the internet. Those preconditions used to be prose in a
// runbook; this package turns them into answers the wizard can show while the
// admin is still typing.
//
// Every check is advisory. DNS propagates on its own schedule, and an admin
// who sets up the server before the records exist is doing nothing wrong — so
// a failing check explains what is missing instead of blocking the install.
package preflight

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/lngstck/stackctl/internal/config"
)

// Operating modes as the wizard presents them. They are a UI grouping of the
// two config axes, not a third axis: relay-operator and relay-own produce the
// same transport and differ only in who owns the base domain.
const (
	ModeRelayOperator = "relay_operator"
	ModeRelayOwn      = "relay_own"
	ModeDirect        = "direct"
)

// Check statuses. Warn means "could not confirm", which is a different thing
// from Fail ("confirmed missing") — an admin behind NAT gets Warn for the
// address comparison forever, and telling them their setup is broken would be
// wrong.
const (
	StatusOK   = "ok"
	StatusWarn = "warn"
	StatusFail = "fail"
	StatusSkip = "skip"
)

// probeTimeout bounds a single check. The wizard runs these interactively
// while someone waits, and an unreachable resolver must not hang the page.
const probeTimeout = 4 * time.Second

// Check is one prerequisite and its outcome.
type Check struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// Input describes what the admin has entered so far.
type Input struct {
	Mode       string
	BaseDomain string
	// RelaySSHHost is the sish endpoint, used to compare the school's DNS
	// records against the relay it is supposed to point at.
	RelaySSHHost string
}

// Resolver is the slice of net.Resolver these checks use. It is an interface
// so tests can answer lookups without a DNS server; *net.Resolver satisfies
// it as-is.
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// Prober runs the checks. The fields exist so tests can drive the checks
// without DNS or a privileged port.
type Prober struct {
	Resolver Resolver
	// LocalIPs returns the addresses this machine answers on.
	LocalIPs func() ([]net.IP, error)
	// PortFree reports whether a TCP port can be bound. The error is
	// non-nil when that could not be determined at all (missing privileges),
	// which is a different answer from "occupied".
	PortFree func(port int) (bool, error)
	// TLSPeerCert returns the certificate a host serves. Nil means dial for
	// real; tests set it to avoid needing a TLS server.
	TLSPeerCert func(ctx context.Context, host string) (*x509.Certificate, error)
	// HTTPStatus fetches https://host/ and returns the status code. Nil
	// means make the request for real.
	HTTPStatus func(ctx context.Context, host string) (int, error)
	// randomLabel produces the throwaway label used to prove a wildcard.
	randomLabel func() string
}

// NewProber returns a Prober wired to the real network.
func NewProber() *Prober {
	return &Prober{
		Resolver:    net.DefaultResolver,
		LocalIPs:    localIPs,
		PortFree:    portFree,
		randomLabel: randomLabel,
	}
}

// Run executes the checks that apply to the chosen mode.
func (p *Prober) Run(ctx context.Context, in Input) []Check {
	switch in.Mode {
	case ModeRelayOperator:
		return []Check{{
			ID:     "operator",
			Title:  "DNS und Zertifikate",
			Status: StatusOK,
			Detail: "Übernimmt der Betreiber. Für diese Betriebsart müssen Sie nichts vorbereiten.",
		}}
	case ModeRelayOwn, ModeDirect:
	default:
		return []Check{{
			ID:     "mode",
			Title:  "Betriebsart",
			Status: StatusFail,
			Detail: "Bitte eine Betriebsart auswählen.",
		}}
	}

	// Everything downstream builds on the domain, so a malformed one ends
	// the run here rather than producing a cascade of confusing failures.
	if err := config.ValidateBaseDomain(in.BaseDomain); err != nil {
		return []Check{{
			ID:     "base_domain",
			Title:  "Domain",
			Status: StatusFail,
			Detail: "Domain ungültig: " + TranslateDomainError(err),
		}}
	}

	checks := []Check{{
		ID:     "base_domain",
		Title:  "Domain",
		Status: StatusOK,
		Detail: fmt.Sprintf("Apps werden unter app.%s erreichbar sein, der Login unter auth.%s.", in.BaseDomain, in.BaseDomain),
	}}

	wildcard, resolved := p.checkWildcard(ctx, in.BaseDomain, in.Mode)
	checks = append(checks, wildcard)

	if in.Mode == ModeDirect {
		checks = append(checks, p.checkPointsHere(resolved, in.BaseDomain))
		checks = append(checks, p.checkPort(80, "HTTP (Port 80)", "Ohne Port 80 kann Let's Encrypt kein Zertifikat ausstellen und auch keins erneuern."))
		checks = append(checks, p.checkPort(443, "HTTPS (Port 443)", "Über diesen Port läuft der gesamte Zugriff auf die Apps."))
	} else {
		checks = append(checks, p.checkPointsAtRelay(ctx, resolved, in))
	}

	return checks
}

// checkWildcard proves the wildcard record by resolving a name nobody could
// have created by hand. Resolving "auth.domain" alone would pass on a single
// record and then break for the first app that gets published.
func (p *Prober) checkWildcard(ctx context.Context, base, mode string) (Check, []net.IP) {
	probe := p.randomLabel() + "." + base

	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	addrs, err := p.Resolver.LookupIPAddr(ctx, probe)
	if err != nil || len(addrs) == 0 {
		// Where the record has to point differs by mode, and naming the
		// wrong target is worse than naming none: an admin who follows it
		// builds a setup that resolves and still never works.
		target := "auf diesen Server"
		if mode == ModeRelayOwn {
			target = "auf den Relay des Betreibers (nicht auf diesen Server)"
		}
		return Check{
			ID:     "dns_wildcard",
			Title:  "Wildcard-DNS",
			Status: StatusFail,
			Detail: fmt.Sprintf(
				"*.%s löst nicht auf. Im DNS der Schuldomain muss ein Wildcard-Eintrag %s zeigen — sonst ist keine einzige App erreichbar. Frisch angelegte Einträge brauchen je nach Anbieter einige Minuten.",
				base, target),
		}, nil
	}

	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		ips = append(ips, a.IP)
	}
	return Check{
		ID:     "dns_wildcard",
		Title:  "Wildcard-DNS",
		Status: StatusOK,
		Detail: fmt.Sprintf("*.%s löst auf %s auf.", base, joinIPs(ips)),
	}, ips
}

// checkPointsHere compares the wildcard target against this machine's own
// addresses. Behind NAT the server never sees its public address, so a
// mismatch cannot be reported as an error — only as "not confirmed".
func (p *Prober) checkPointsHere(resolved []net.IP, base string) Check {
	const title = "Zeigt auf diesen Server"
	if len(resolved) == 0 {
		return Check{ID: "dns_target", Title: title, Status: StatusSkip,
			Detail: "Wird geprüft, sobald der Wildcard-Eintrag auflöst."}
	}

	local, err := p.LocalIPs()
	if err != nil {
		return Check{ID: "dns_target", Title: title, Status: StatusWarn,
			Detail: "Die eigenen Adressen dieses Servers konnten nicht ermittelt werden: " + err.Error()}
	}

	for _, r := range resolved {
		for _, l := range local {
			if r.Equal(l) {
				return Check{ID: "dns_target", Title: title, Status: StatusOK,
					Detail: fmt.Sprintf("%s ist eine Adresse dieses Servers.", r)}
			}
		}
	}

	return Check{ID: "dns_target", Title: title, Status: StatusWarn,
		Detail: fmt.Sprintf(
			"*.%s zeigt auf %s — diese Adresse kennt der Server nicht als eigene. Das ist normal, wenn er hinter einer Firewall oder NAT steht; dann bitte selbst prüfen, dass die Weiterleitung hierher zeigt. Andernfalls zeigt der DNS-Eintrag auf den falschen Rechner.",
			base, joinIPs(resolved))}
}

// checkPointsAtRelay verifies that the school's own domain points at the
// relay rather than at the school's server. This is the mistake that mode
// costs people an afternoon: the records look plausible, but traffic arrives
// at a machine that has no tunnel.
func (p *Prober) checkPointsAtRelay(ctx context.Context, resolved []net.IP, in Input) Check {
	const title = "Zeigt auf den Relay"
	if len(resolved) == 0 {
		return Check{ID: "dns_target", Title: title, Status: StatusSkip,
			Detail: "Wird geprüft, sobald der Wildcard-Eintrag auflöst."}
	}
	if in.RelaySSHHost == "" {
		return Check{ID: "dns_target", Title: title, Status: StatusSkip,
			Detail: "Kein Relay-Endpunkt konfiguriert."}
	}

	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	relayAddrs, err := p.Resolver.LookupIPAddr(ctx, in.RelaySSHHost)
	if err != nil || len(relayAddrs) == 0 {
		return Check{ID: "dns_target", Title: title, Status: StatusWarn,
			Detail: fmt.Sprintf("Die Adresse des Relays (%s) konnte nicht aufgelöst werden — der Abgleich ist damit offen.", in.RelaySSHHost)}
	}

	for _, r := range resolved {
		for _, a := range relayAddrs {
			if r.Equal(a.IP) {
				return Check{ID: "dns_target", Title: title, Status: StatusOK,
					Detail: fmt.Sprintf("*.%s zeigt auf den Relay (%s).", in.BaseDomain, r)}
			}
		}
	}

	return Check{ID: "dns_target", Title: title, Status: StatusFail,
		Detail: fmt.Sprintf(
			"*.%s zeigt auf %s, der Relay %s liegt aber auf %s. In dieser Betriebsart muss der Wildcard-Eintrag auf den Relay zeigen, nicht auf diesen Server.",
			in.BaseDomain, joinIPs(resolved), in.RelaySSHHost, joinIPAddrs(relayAddrs))}
}

// checkPort reports whether a privileged port is available for the reverse
// proxy. "Occupied" here usually means another web server is already running,
// which is worth finding out before the install rather than after.
func (p *Prober) checkPort(port int, title, why string) Check {
	id := fmt.Sprintf("port_%d", port)

	free, err := p.PortFree(port)
	if err != nil {
		return Check{ID: id, Title: title, Status: StatusWarn,
			Detail: fmt.Sprintf("Port %d konnte nicht geprüft werden (%v). %s", port, err, why)}
	}
	if !free {
		return Check{ID: id, Title: title, Status: StatusFail,
			Detail: fmt.Sprintf("Port %d ist belegt. Vermutlich läuft bereits ein Webserver (Apache, nginx) auf diesem Server — der muss gestoppt werden. %s", port, why)}
	}
	return Check{ID: id, Title: title, Status: StatusOK,
		Detail: fmt.Sprintf("Port %d ist frei.", port)}
}

// Mode derives the wizard's mode from a stored config. The mode is not a
// config field on purpose: it is fully determined by the transport and by
// whether the base domain is the one the operator hands out, and a stored
// copy could only ever disagree with those two.
func Mode(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if cfg.Public.Transport == config.TransportDirect {
		return ModeDirect
	}
	if cfg.School.Slug != "" && cfg.Public.BaseDomain == config.RelayBaseDomain(cfg.School.Slug) {
		return ModeRelayOperator
	}
	return ModeRelayOwn
}

// ModeLabel returns the German name of a mode for display.
func ModeLabel(mode string) string {
	switch mode {
	case ModeRelayOperator:
		return "Adresse des Betreibers"
	case ModeRelayOwn:
		return "Eigene Domain über den Relay"
	case ModeDirect:
		return "Direkter Betrieb"
	}
	return "Unbekannt"
}

// Worst reduces a run to a single status for a summary line, ignoring skips.
func Worst(checks []Check) string {
	worst := StatusOK
	for _, c := range checks {
		switch c.Status {
		case StatusFail:
			return StatusFail
		case StatusWarn:
			worst = StatusWarn
		}
	}
	return worst
}

func randomLabel() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		// A fixed label still proves a wildcard as long as nobody created
		// exactly this name by hand, which is a safe assumption.
		return "ls-probe-fallback"
	}
	return "ls-probe-" + hex.EncodeToString(b)
}

func localIPs() ([]net.IP, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	var ips []net.IP
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok {
			ips = append(ips, ipnet.IP)
		}
	}
	return ips, nil
}

// portFree binds the port to find out. Binding is the only answer that counts
// — a port can look free in /proc and still be taken by a container publishing
// it. EACCES means we are not privileged enough to tell, which is reported as
// an error rather than as a result.
func portFree(port int) (bool, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
			return false, errors.New("keine Berechtigung zum Prüfen privilegierter Ports")
		}
		return false, nil
	}
	ln.Close()
	return true, nil
}

// TranslateDomainError turns the English validation messages into something
// an admin reads under an input field.
func TranslateDomainError(err error) string {
	msg := err.Error()
	replacements := []struct{ from, to string }{
		{"must not be empty", "darf nicht leer sein"},
		{"must be a bare domain, without https://", "bitte ohne https:// eingeben"},
		{"must be a bare domain, without a path", "bitte ohne Pfad eingeben"},
		{"must be the domain itself, not a wildcard like *.example.org", "bitte die Domain selbst eintragen, nicht den Wildcard-Eintrag (also schule.de statt *.schule.de)"},
		{"must not carry a port", "darf keine Portnummer enthalten"},
		{"must be lowercase", "bitte klein schreiben"},
		{"must not start or end with a dot", "darf nicht mit einem Punkt beginnen oder enden"},
		{"must contain at least one dot, e.g. example.org", "muss mindestens einen Punkt enthalten, z.B. schule.de"},
		{"must not contain an empty part (two dots in a row)", "enthält zwei Punkte hintereinander"},
	}
	for _, r := range replacements {
		if msg == r.from {
			return r.to
		}
	}
	return msg
}

func joinIPs(ips []net.IP) string {
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func joinIPAddrs(addrs []net.IPAddr) string {
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		ips = append(ips, a.IP)
	}
	return joinIPs(ips)
}
