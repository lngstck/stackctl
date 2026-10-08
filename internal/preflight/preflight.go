// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

// Package preflight checks whether the prerequisites for a chosen operating
// mode are actually in place: the DNS records at the school's provider, the
// ports the reverse proxy needs. Those preconditions used to be prose in a
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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/lngstck/stackctl/internal/config"
)

// Operating modes as the wizard presents them, one per transport.
const (
	ModeLocal  = config.TransportLocal
	ModeDirect = config.TransportDirect
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
	// DNSToken and ChallengeDomain configure DNS-01 in ModeLocal (see
	// config.PublicLocal).
	DNSToken        string
	ChallengeDomain string
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
	// PublicResolver asks a public DNS server directly. It is consulted only
	// when Resolver finds nothing, to tell "record missing" from "record
	// dropped on the way" — routers with DNS rebind protection discard
	// public answers that point at private addresses, which is exactly what
	// the school-network mode publishes. Nil skips that distinction.
	PublicResolver Resolver
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
	// CNAME returns the CNAME target of a name as the internet sees it —
	// the view the certificate authority has — or errNoCNAME.
	CNAME func(ctx context.Context, name string) (string, error)
	// DNSZones lists the zones a deSEC token may manage. It returns
	// errTokenRejected for a token deSEC does not accept.
	DNSZones func(ctx context.Context, token string) ([]string, error)
	// randomLabel produces the throwaway label used to prove a wildcard.
	randomLabel func() string
}

// NewProber returns a Prober wired to the real network.
func NewProber() *Prober {
	return &Prober{
		Resolver:       net.DefaultResolver,
		PublicResolver: publicResolver(),
		LocalIPs:       localIPs,
		PortFree:       portFree,
		CNAME: func(ctx context.Context, name string) (string, error) {
			return lookupCNAMERecord(ctx, publicDNSServer, name)
		},
		DNSZones:    desecZones,
		randomLabel: randomLabel,
	}
}

// Run executes the checks that apply to the chosen mode.
func (p *Prober) Run(ctx context.Context, in Input) []Check {
	switch in.Mode {
	case ModeLocal, ModeDirect:
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

	if in.Mode == ModeLocal {
		checks = append(checks, p.checkPointsHereLocal(resolved, in.BaseDomain))
		checks = append(checks, p.checkChallenge(ctx, in.BaseDomain, in.ChallengeDomain))
		checks = append(checks, p.checkDNSToken(ctx, in.DNSToken, challengeZone(in.BaseDomain, in.ChallengeDomain)))
		checks = append(checks, p.checkPort(80, "HTTP (Port 80)", "Darüber leitet der Proxy Aufrufe ohne https:// auf HTTPS um."))
		checks = append(checks, p.checkPort(443, "HTTPS (Port 443)", "Über diesen Port läuft der gesamte Zugriff auf die Apps."))
		return checks
	}

	checks = append(checks, p.checkPointsHere(resolved, in.BaseDomain))
	checks = append(checks, p.checkPort(80, "HTTP (Port 80)", "Ohne Port 80 kann Let's Encrypt kein Zertifikat ausstellen und auch keins erneuern."))
	checks = append(checks, p.checkPort(443, "HTTPS (Port 443)", "Über diesen Port läuft der gesamte Zugriff auf die Apps."))
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
		return p.explainMissingWildcard(ctx, probe, base, mode), nil
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

// explainMissingWildcard tells a record that does not exist from one that
// exists but never arrives. The second is what DNS rebind protection does to
// a public name pointing at a private address — and its fix is in the
// school's router, not at the DNS provider, so naming the wrong one would
// send the admin to change a record that is already right.
func (p *Prober) explainMissingWildcard(ctx context.Context, probe, base, mode string) Check {
	check := Check{ID: "dns_wildcard", Title: "Wildcard-DNS", Status: StatusFail}

	if p.PublicResolver != nil {
		if addrs, err := p.PublicResolver.LookupIPAddr(ctx, probe); err == nil && len(addrs) > 0 {
			ips := make([]net.IP, 0, len(addrs))
			for _, a := range addrs {
				ips = append(ips, a.IP)
			}
			if anyPrivate(ips) {
				check.Detail = fmt.Sprintf(
					"*.%s löst im Internet auf %s auf, hier im Netz aber nicht. Vermutlich verwirft der DNS-Server oder Router des Schulnetzes Antworten mit privaten Adressen (DNS-Rebind-Schutz). Dort eine Ausnahme für %s eintragen — bei einer FRITZ!Box unter Heimnetz → Netzwerk → Netzwerkeinstellungen.",
					base, joinIPs(ips), base)
				return check
			}
			check.Detail = fmt.Sprintf(
				"*.%s ist im Internet schon sichtbar (%s), der DNS-Server hier kennt den Eintrag aber noch nicht. Meist hilft es, einige Minuten zu warten.",
				base, joinIPs(ips))
			return check
		}
	}

	target := "auf diesen Server"
	if mode == ModeLocal {
		target = "auf die Adresse dieses Servers im Schulnetz"
	}
	check.Detail = fmt.Sprintf(
		"*.%s löst nicht auf. Im DNS der Schuldomain muss ein Wildcard-Eintrag %s zeigen — sonst ist keine einzige App erreichbar. Frisch angelegte Einträge brauchen je nach Anbieter einige Minuten.",
		base, target)
	return check
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

// checkPointsHereLocal compares the wildcard target against this machine's
// own addresses. In the school network the server knows its address, so a
// mismatch is a finding — and a public address is plainly the wrong mode.
func (p *Prober) checkPointsHereLocal(resolved []net.IP, base string) Check {
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

	own := joinIPs(privateIPs(local))
	if own == "" {
		own = "unbekannt"
	}
	if !anyPrivate(resolved) {
		return Check{ID: "dns_target", Title: title, Status: StatusFail,
			Detail: fmt.Sprintf(
				"*.%s zeigt auf die öffentliche Adresse %s. Für den Betrieb im Schulnetz muss der Eintrag auf die Adresse dieses Servers im Schulnetz zeigen (%s).",
				base, joinIPs(resolved), own)}
	}
	return Check{ID: "dns_target", Title: title, Status: StatusWarn,
		Detail: fmt.Sprintf(
			"*.%s zeigt auf %s, dieser Server hat aber %s. Steht kein Weiterleiter dazwischen, zeigt der Eintrag auf den falschen Rechner.",
			base, joinIPs(resolved), own)}
}

// checkChallenge verifies the delegation of the ACME challenge: the
// certificate authority looks up _acme-challenge.{base}, and the CNAME has to
// lead it into the zone the token can write to.
func (p *Prober) checkChallenge(ctx context.Context, base, target string) Check {
	const (
		id    = "dns_challenge"
		title = "Zertifikat (DNS-01)"
	)
	name := config.ChallengeName(base)
	if target == "" {
		return Check{ID: id, Title: title, Status: StatusSkip,
			Detail: fmt.Sprintf("Keine Delegation angegeben — dann muss deSEC die Zone von %s selbst verwalten.", base)}
	}

	if p.CNAME == nil {
		return Check{ID: id, Title: title, Status: StatusSkip, Detail: "Nicht geprüft."}
	}

	want := fmt.Sprintf("Beim DNS-Anbieter der Schuldomain anlegen: %s CNAME %s.", name, target)
	cname, err := p.CNAME(ctx, name)
	cname = strings.TrimSuffix(strings.ToLower(cname), ".")
	switch {
	case errors.Is(err, errNoCNAME):
		return Check{ID: id, Title: title, Status: StatusFail,
			Detail: fmt.Sprintf("%s hat keinen CNAME-Eintrag. %s", name, want)}
	case err != nil:
		return Check{ID: id, Title: title, Status: StatusWarn,
			Detail: fmt.Sprintf("%s konnte nicht abgefragt werden: %v", name, err)}
	case cname != target:
		return Check{ID: id, Title: title, Status: StatusFail,
			Detail: fmt.Sprintf("%s zeigt auf %s statt auf %s. %s", name, cname, target, want)}
	}
	return Check{ID: id, Title: title, Status: StatusOK,
		Detail: fmt.Sprintf("%s zeigt auf %s.", name, target)}
}

// checkDNSToken asks deSEC whether the token is accepted and may write the
// zone the challenge lands in. This request goes to desec.io — it carries the
// token and nothing about the school beyond what deSEC already holds.
func (p *Prober) checkDNSToken(ctx context.Context, token, name string) Check {
	const (
		id    = "dns_token"
		title = "deSEC-Token"
	)
	if token == "" {
		return Check{ID: id, Title: title, Status: StatusFail,
			Detail: "Ohne Token bei deSEC bekommt der Server kein Zertifikat."}
	}
	if p.DNSZones == nil {
		return Check{ID: id, Title: title, Status: StatusSkip, Detail: "Nicht geprüft."}
	}

	zones, err := p.DNSZones(ctx, token)
	switch {
	case errors.Is(err, errTokenRejected):
		return Check{ID: id, Title: title, Status: StatusFail,
			Detail: "deSEC lehnt den Token ab. Bitte einen neuen Token anlegen und vollständig kopieren."}
	case err != nil:
		return Check{ID: id, Title: title, Status: StatusWarn,
			Detail: "deSEC war nicht erreichbar: " + err.Error()}
	}

	for _, z := range zones {
		if name == z || strings.HasSuffix(name, "."+z) {
			return Check{ID: id, Title: title, Status: StatusOK,
				Detail: fmt.Sprintf("Der Token verwaltet %s.", z)}
		}
	}
	have := strings.Join(zones, ", ")
	if have == "" {
		have = "keine"
	}
	return Check{ID: id, Title: title, Status: StatusFail,
		Detail: fmt.Sprintf("Der Token verwaltet nicht die Zone von %s (vorhanden: %s).", name, have)}
}

// challengeZone is the name the TXT record ends up under: the delegation
// target if there is one, otherwise the challenge name itself.
func challengeZone(base, target string) string {
	if target != "" {
		return target
	}
	return config.ChallengeName(base)
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
// config field on purpose: it is determined by the transport, and a stored
// copy could only ever disagree with it.
func Mode(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	switch cfg.Public.Transport {
	case config.TransportLocal:
		return ModeLocal
	case config.TransportDirect:
		return ModeDirect
	}
	return ""
}

// ModeLabel returns the German name of a mode for display.
func ModeLabel(mode string) string {
	switch mode {
	case ModeLocal:
		return "Nur im Schulnetz"
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

// errTokenRejected is what DNSZones returns when deSEC refuses the token.
var errTokenRejected = errors.New("token rejected")

// desecZones lists the domains a token may manage, from deSEC's API.
func desecZones(ctx context.Context, token string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://desec.io/api/v1/domains/", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Token "+token)
	resp, err := (&http.Client{Timeout: probeTimeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, errTokenRejected
	default:
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var domains []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&domains); err != nil {
		return nil, fmt.Errorf("Antwort unlesbar: %w", err)
	}
	names := make([]string, 0, len(domains))
	for _, d := range domains {
		names = append(names, d.Name)
	}
	return names, nil
}

// publicResolver asks Quad9 directly, bypassing the school's DNS server. It
// only ever sees the random probe name under the school's domain.
func publicResolver() Resolver {
	dialer := &net.Dialer{Timeout: probeTimeout}
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, "9.9.9.9:53")
		},
	}
}

func anyPrivate(ips []net.IP) bool {
	return len(privateIPs(ips)) > 0
}

// privateIPs keeps the addresses that belong to a local network.
func privateIPs(ips []net.IP) []net.IP {
	var out []net.IP
	for _, ip := range ips {
		if ip.IsPrivate() {
			out = append(out, ip)
		}
	}
	return out
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
