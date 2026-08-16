// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package preflight

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"time"
)

// CertWarnDays is when a certificate starts being reported as a problem.
//
// Caddy renews at roughly a third of the remaining lifetime — for a 90-day
// Let's Encrypt certificate that is about 30 days before expiry. Anything
// under two weeks therefore means renewal has already been failing for a
// while, silently, and the certificate is the last thing still holding the
// site up.
const CertWarnDays = 14

// LiveInput describes a running install.
type LiveInput struct {
	Mode         string
	BaseDomain   string
	RelaySSHHost string
	// AuthHost is the hostname of the login. It is the one address every
	// install has, whatever else is published, which makes it the natural
	// probe target.
	AuthHost string
}

// Live runs the checks that matter while the system is in operation.
//
// The setup wizard answers "can this work?" once. These answer "does it still
// work?" — the two questions differ, because DNS gets edited, certificates
// expire, and a relay endpoint moves. What they share is the DNS check, so
// that one is literally the same code.
func (p *Prober) Live(ctx context.Context, in LiveInput) []Check {
	var checks []Check

	// The operator's own relay carries operator-managed DNS, so there is
	// nothing here the school could have broken — but the address still has
	// to answer, which the endpoint check below covers.
	if in.Mode != ModeRelayOperator {
		wildcard, resolved := p.checkWildcard(ctx, in.BaseDomain, in.Mode)
		checks = append(checks, wildcard)
		if in.Mode == ModeDirect {
			checks = append(checks, p.checkPointsHere(resolved, in.BaseDomain))
		} else {
			checks = append(checks, p.checkPointsAtRelay(ctx, resolved, Input{
				Mode: in.Mode, BaseDomain: in.BaseDomain, RelaySSHHost: in.RelaySSHHost,
			}))
		}
	}

	checks = append(checks, p.checkEndpoint(ctx, in.AuthHost))
	checks = append(checks, p.checkCertificate(ctx, in.AuthHost))
	return checks
}

// checkEndpoint asks the public address the same question a browser would.
// Everything else on this page can look right while this one fails, which is
// exactly why it is worth having.
func (p *Prober) checkEndpoint(ctx context.Context, host string) Check {
	const (
		id    = "endpoint"
		title = "Login erreichbar"
	)
	if host == "" {
		return Check{ID: id, Title: title, Status: StatusSkip, Detail: "Keine öffentliche Adresse konfiguriert."}
	}

	status, err := p.httpStatus(ctx, host)
	if err != nil {
		return Check{ID: id, Title: title, Status: StatusFail,
			Detail: fmt.Sprintf("%s antwortet nicht: %v", host, err)}
	}
	return Check{ID: id, Title: title, Status: StatusOK,
		Detail: fmt.Sprintf("%s antwortet (HTTP %d).", host, status)}
}

// httpStatus fetches the public address the way a browser would. Any status
// counts as reachable: a redirect to the login is as good an answer as a 200,
// and judging the content is not this check's job.
func (p *Prober) httpStatus(ctx context.Context, host string) (int, error) {
	if p.HTTPStatus != nil {
		return p.HTTPStatus(ctx, host)
	}

	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/", nil)
	if err != nil {
		return 0, err
	}
	client := &http.Client{
		Timeout: probeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// checkCertificate reports what a browser would see. Reading Caddy's storage
// directly would be quicker but would only prove what Caddy believes it has —
// not what it actually serves, and not anything at all for a relay install
// where the certificate belongs to the operator.
func (p *Prober) checkCertificate(ctx context.Context, host string) Check {
	const (
		id    = "certificate"
		title = "Zertifikat"
	)
	if host == "" {
		return Check{ID: id, Title: title, Status: StatusSkip, Detail: "Keine öffentliche Adresse konfiguriert."}
	}

	cert, err := p.peerCertificate(ctx, host)
	if err != nil {
		// A failure here is nearly always the endpoint being down, which the
		// check above already reports in plain words. Repeating it as a
		// second alarm would double the noise for one cause.
		return Check{ID: id, Title: title, Status: StatusWarn,
			Detail: fmt.Sprintf("Konnte nicht geprüft werden: %v", err)}
	}

	left := time.Until(cert.NotAfter)
	days := int(left.Hours() / 24)
	until := cert.NotAfter.Local().Format("02.01.2006")

	switch {
	case left <= 0:
		return Check{ID: id, Title: title, Status: StatusFail,
			Detail: fmt.Sprintf("Abgelaufen am %s. Browser verweigern die Verbindung.", until)}
	case days <= CertWarnDays:
		return Check{ID: id, Title: title, Status: StatusWarn,
			Detail: fmt.Sprintf("Läuft am %s ab (%d Tage). Die automatische Erneuerung sollte laengst gelaufen sein — bitte pruefen, ob Port 80 von aussen erreichbar ist.", until, days),
		}
	default:
		return Check{ID: id, Title: title, Status: StatusOK,
			Detail: fmt.Sprintf("Gültig bis %s (%d Tage), ausgestellt von %s.", until, days, issuerName(cert))}
	}
}

// peerCertificate opens a TLS connection and returns the leaf certificate.
func (p *Prober) peerCertificate(ctx context.Context, host string) (*x509.Certificate, error) {
	if p.TLSPeerCert != nil {
		return p.TLSPeerCert(ctx, host)
	}

	dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: probeTimeout}}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, "443"))
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	state := conn.(*tls.Conn).ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return nil, fmt.Errorf("keine Zertifikate im Handshake")
	}
	return state.PeerCertificates[0], nil
}

func issuerName(cert *x509.Certificate) string {
	if cert.Issuer.CommonName != "" {
		return cert.Issuer.CommonName
	}
	if len(cert.Issuer.Organization) > 0 {
		return cert.Issuer.Organization[0]
	}
	return "unbekannt"
}
