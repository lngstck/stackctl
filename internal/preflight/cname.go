// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package preflight

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// publicDNSServer is asked directly for records the certificate authority
// sees from the internet, bypassing the school's DNS server. Quad9 only ever
// sees names under the school's own domain.
const publicDNSServer = "9.9.9.9:53"

// errNoCNAME means the name exists without a CNAME record, or not at all.
var errNoCNAME = errors.New("kein CNAME-Eintrag")

// lookupCNAMERecord asks a DNS server for the CNAME record of name itself.
//
// net.Resolver.LookupCNAME cannot do this: it chases the alias to an address
// and reports "no such host" when the target has none. That is the normal
// state of a challenge delegation — its target at deSEC carries a TXT record
// only while a certificate is being issued, and nothing at all in between.
func lookupCNAMERecord(ctx context.Context, server, name string) (string, error) {
	qname, err := dnsmessage.NewName(strings.TrimSuffix(name, ".") + ".")
	if err != nil {
		return "", err
	}
	var idBytes [2]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return "", err
	}
	id := binary.BigEndian.Uint16(idBytes[:])

	query, err := (&dnsmessage.Message{
		Header: dnsmessage.Header{ID: id, RecursionDesired: true},
		Questions: []dnsmessage.Question{{
			Name: qname, Type: dnsmessage.TypeCNAME, Class: dnsmessage.ClassINET,
		}},
	}).Pack()
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "udp", server)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(probeTimeout))
	}
	if _, err := conn.Write(query); err != nil {
		return "", err
	}

	buf := make([]byte, 1232)
	n, err := conn.Read(buf)
	if err != nil {
		return "", err
	}
	return parseCNAMEAnswer(buf[:n], id)
}

// parseCNAMEAnswer extracts the CNAME target from a DNS response.
func parseCNAMEAnswer(msg []byte, id uint16) (string, error) {
	var p dnsmessage.Parser
	h, err := p.Start(msg)
	if err != nil {
		return "", err
	}
	if h.ID != id {
		return "", errors.New("DNS-Antwort passt nicht zur Anfrage")
	}
	switch h.RCode {
	case dnsmessage.RCodeSuccess:
	case dnsmessage.RCodeNameError:
		return "", errNoCNAME
	default:
		return "", fmt.Errorf("DNS-Fehler %v", h.RCode)
	}
	if err := p.SkipAllQuestions(); err != nil {
		return "", err
	}
	for {
		ah, err := p.AnswerHeader()
		if errors.Is(err, dnsmessage.ErrSectionDone) {
			return "", errNoCNAME
		}
		if err != nil {
			return "", err
		}
		if ah.Type != dnsmessage.TypeCNAME {
			if err := p.SkipAnswer(); err != nil {
				return "", err
			}
			continue
		}
		r, err := p.CNAMEResource()
		if err != nil {
			return "", err
		}
		return strings.TrimSuffix(r.CNAME.String(), "."), nil
	}
}
