package preflight

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/lngstck/stackctl/internal/config"
)

// fakeResolver answers from a table. A host that is absent behaves like an
// NXDOMAIN, which is what an admin sees before the record propagates.
type fakeResolver map[string][]string

func (f fakeResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	ips, ok := f[host]
	if !ok {
		// A wildcard entry answers every name under the domain.
		for pattern, addrs := range f {
			if strings.HasPrefix(pattern, "*.") && strings.HasSuffix(host, pattern[1:]) {
				ips, ok = addrs, true
				break
			}
		}
	}
	if !ok {
		return nil, errors.New("no such host")
	}
	out := make([]net.IPAddr, 0, len(ips))
	for _, ip := range ips {
		out = append(out, net.IPAddr{IP: net.ParseIP(ip)})
	}
	return out, nil
}

func proberWith(res fakeResolver, local []string, portsFree bool) *Prober {
	return &Prober{
		Resolver: res,
		LocalIPs: func() ([]net.IP, error) {
			var ips []net.IP
			for _, s := range local {
				ips = append(ips, net.ParseIP(s))
			}
			return ips, nil
		},
		PortFree:    func(int) (bool, error) { return portsFree, nil },
		randomLabel: func() string { return "ls-probe-test" },
	}
}

func byID(t *testing.T, checks []Check, id string) Check {
	t.Helper()
	for _, c := range checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("check %q not in result: %+v", id, checks)
	return Check{}
}

func TestDirectAllPrerequisitesMet(t *testing.T) {
	p := proberWith(fakeResolver{"*.ls.gym-phoenix.de": {"198.51.100.7"}}, []string{"198.51.100.7"}, true)

	checks := p.Run(context.Background(), Input{Mode: ModeDirect, BaseDomain: "ls.gym-phoenix.de"})

	if got := Worst(checks); got != StatusOK {
		t.Errorf("worst = %q, want %q: %+v", got, StatusOK, checks)
	}
	for _, id := range []string{"base_domain", "dns_wildcard", "dns_target", "port_80", "port_443"} {
		if got := byID(t, checks, id).Status; got != StatusOK {
			t.Errorf("check %s = %q, want ok", id, got)
		}
	}
}

// A single A record for one hostname is the trap this check exists for: it
// makes auth.domain work while every app published later 404s.
func TestDirectSingleRecordDoesNotPassAsWildcard(t *testing.T) {
	p := proberWith(fakeResolver{"auth.ls.gym-phoenix.de": {"198.51.100.7"}}, []string{"198.51.100.7"}, true)

	checks := p.Run(context.Background(), Input{Mode: ModeDirect, BaseDomain: "ls.gym-phoenix.de"})

	if got := byID(t, checks, "dns_wildcard").Status; got != StatusFail {
		t.Errorf("wildcard check = %q, want fail — a single record must not pass", got)
	}
	// Without a resolved address there is nothing to compare, and inventing
	// a verdict would be worse than saying so.
	if got := byID(t, checks, "dns_target").Status; got != StatusSkip {
		t.Errorf("target check = %q, want skip", got)
	}
}

// Der Hinweis muss sagen, wohin der Eintrag zeigen soll — sonst baut die
// Admin eine Auflösung, die stimmt, und einen Zugang, der nie funktioniert.
func TestWildcardHintNamesTheTarget(t *testing.T) {
	p := proberWith(fakeResolver{}, nil, true)

	direct := byID(t, p.Run(context.Background(), Input{Mode: ModeDirect, BaseDomain: "ls.gym-phoenix.de"}), "dns_wildcard")
	if !strings.Contains(direct.Detail, "auf diesen Server") {
		t.Errorf("Hinweis muss auf diesen Server zeigen: %q", direct.Detail)
	}
}

// Behind NAT the server never sees its public address. Reporting that as a
// failure would tell a correctly configured admin their setup is broken.
func TestDirectForeignAddressWarnsRatherThanFails(t *testing.T) {
	p := proberWith(fakeResolver{"*.ls.gym-phoenix.de": {"198.51.100.7"}}, []string{"192.168.1.10"}, true)

	checks := p.Run(context.Background(), Input{Mode: ModeDirect, BaseDomain: "ls.gym-phoenix.de"})

	got := byID(t, checks, "dns_target")
	if got.Status != StatusWarn {
		t.Errorf("target check = %q, want warn", got.Status)
	}
	if !strings.Contains(got.Detail, "198.51.100.7") {
		t.Errorf("detail should name the resolved address: %q", got.Detail)
	}
}

func TestDirectOccupiedPortFails(t *testing.T) {
	p := proberWith(fakeResolver{"*.ls.gym-phoenix.de": {"198.51.100.7"}}, []string{"198.51.100.7"}, false)

	checks := p.Run(context.Background(), Input{Mode: ModeDirect, BaseDomain: "ls.gym-phoenix.de"})

	if got := byID(t, checks, "port_80").Status; got != StatusFail {
		t.Errorf("port 80 = %q, want fail", got)
	}
	if got := Worst(checks); got != StatusFail {
		t.Errorf("worst = %q, want fail", got)
	}
}

// An unprivileged stackctl cannot bind port 80. That is "unknown", not
// "occupied" — the difference decides whether the admin goes hunting for a
// web server that is not there.
func TestPortCheckWithoutPrivilegesWarns(t *testing.T) {
	p := proberWith(fakeResolver{"*.ls.gym-phoenix.de": {"198.51.100.7"}}, []string{"198.51.100.7"}, true)
	p.PortFree = func(int) (bool, error) { return false, errors.New("keine Berechtigung") }

	checks := p.Run(context.Background(), Input{Mode: ModeDirect, BaseDomain: "ls.gym-phoenix.de"})

	if got := byID(t, checks, "port_80").Status; got != StatusWarn {
		t.Errorf("port 80 = %q, want warn", got)
	}
}

// A pasted wildcard record is the single most likely typo, and it must be
// caught before it reaches the Dex issuer.
func TestInvalidDomainStopsBeforeNetworkChecks(t *testing.T) {
	p := proberWith(fakeResolver{}, nil, true)

	checks := p.Run(context.Background(), Input{Mode: ModeDirect, BaseDomain: "*.ls.gym-phoenix.de"})

	if len(checks) != 1 {
		t.Fatalf("invalid domain should end the run, got %+v", checks)
	}
	if checks[0].Status != StatusFail {
		t.Errorf("status = %q, want fail", checks[0].Status)
	}
	if !strings.Contains(checks[0].Detail, "schule.de statt *.schule.de") {
		t.Errorf("detail should explain the wildcard mistake in German: %q", checks[0].Detail)
	}
}

func TestUnknownModeIsRejected(t *testing.T) {
	p := proberWith(fakeResolver{}, nil, true)

	checks := p.Run(context.Background(), Input{Mode: "", BaseDomain: "ls.gym-phoenix.de"})

	if len(checks) != 1 || checks[0].Status != StatusFail {
		t.Errorf("empty mode should fail once, got %+v", checks)
	}
}

func TestModeDerivedFromConfig(t *testing.T) {
	direct := &config.Config{Public: config.Public{Transport: config.TransportDirect, BaseDomain: "ls.gym-phoenix.de"}}
	if got := Mode(direct); got != ModeDirect {
		t.Errorf("Mode = %q, want %q", got, ModeDirect)
	}
	// A transport this build does not know has no mode, rather than being
	// mistaken for one that would show the wrong instructions.
	if got := Mode(&config.Config{Public: config.Public{Transport: "relay"}}); got != "" {
		t.Errorf("Mode(relay) = %q, want empty", got)
	}
}
