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

// The operator relay is the frictionless path: the school prepares nothing,
// so the wizard must not present it with DNS homework.
func TestRelayOperatorNeedsNoPreparation(t *testing.T) {
	p := proberWith(fakeResolver{}, nil, true)

	checks := p.Run(context.Background(), Input{Mode: ModeRelayOperator})

	if got := Worst(checks); got != StatusOK {
		t.Errorf("operator relay reported %q, want %q: %+v", got, StatusOK, checks)
	}
	for _, c := range checks {
		if c.ID == "dns_wildcard" || strings.HasPrefix(c.ID, "port_") {
			t.Errorf("operator relay must not check %q", c.ID)
		}
	}
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

// Wohin der Eintrag zeigen muss, unterscheidet sich je Betriebsart. Ein
// Hinweis auf das falsche Ziel ist schlimmer als keiner: wer ihm folgt, baut
// eine Auflösung, die stimmt, und einen Zugang, der trotzdem nie funktioniert.
func TestWildcardHintNamesTheRightTarget(t *testing.T) {
	p := proberWith(fakeResolver{}, nil, true)

	direct := byID(t, p.Run(context.Background(), Input{Mode: ModeDirect, BaseDomain: "ls.gym-phoenix.de"}), "dns_wildcard")
	if !strings.Contains(direct.Detail, "auf diesen Server") {
		t.Errorf("direkter Betrieb: Hinweis muss auf diesen Server zeigen: %q", direct.Detail)
	}

	relay := byID(t, p.Run(context.Background(), Input{
		Mode: ModeRelayOwn, BaseDomain: "ls.gym-phoenix.de", RelaySSHHost: "sish.learningstack.online",
	}), "dns_wildcard")
	if !strings.Contains(relay.Detail, "auf den Relay") {
		t.Errorf("Relay-Betrieb: Hinweis muss auf den Relay zeigen: %q", relay.Detail)
	}
	if strings.Contains(relay.Detail, "auf diesen Server zeigen") {
		t.Errorf("Relay-Betrieb darf nicht auf diesen Server verweisen: %q", relay.Detail)
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

// The school's own domain at the relay must point at the relay. Pointing it
// at the school's own server looks plausible and silently never works.
func TestRelayOwnDomainPointingAtOwnServerFails(t *testing.T) {
	p := proberWith(fakeResolver{
		"*.ls.gym-phoenix.de":       {"198.51.100.7"}, // the school's server
		"sish.learningstack.online": {"203.0.113.9"},
	}, []string{"198.51.100.7"}, true)

	checks := p.Run(context.Background(), Input{
		Mode:         ModeRelayOwn,
		BaseDomain:   "ls.gym-phoenix.de",
		RelaySSHHost: "sish.learningstack.online",
	})

	got := byID(t, checks, "dns_target")
	if got.Status != StatusFail {
		t.Errorf("target check = %q, want fail", got.Status)
	}
	if !strings.Contains(got.Detail, "203.0.113.9") {
		t.Errorf("detail should name the relay address so the admin can fix the record: %q", got.Detail)
	}
	// Ports belong to the direct mode only — nothing binds them here.
	for _, c := range checks {
		if strings.HasPrefix(c.ID, "port_") {
			t.Errorf("relay mode must not check ports: %+v", c)
		}
	}
}

func TestRelayOwnDomainPointingAtRelayPasses(t *testing.T) {
	p := proberWith(fakeResolver{
		"*.ls.gym-phoenix.de":       {"203.0.113.9"},
		"sish.learningstack.online": {"203.0.113.9"},
	}, []string{"198.51.100.7"}, true)

	checks := p.Run(context.Background(), Input{
		Mode:         ModeRelayOwn,
		BaseDomain:   "ls.gym-phoenix.de",
		RelaySSHHost: "sish.learningstack.online",
	})

	if got := Worst(checks); got != StatusOK {
		t.Errorf("worst = %q, want ok: %+v", got, checks)
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
	tests := []struct {
		name string
		cfg  *config.Config
		want string
	}{
		{
			name: "operator relay",
			cfg: &config.Config{
				School: config.School{Slug: "phoenix"},
				Public: config.Public{Transport: config.TransportRelay, BaseDomain: "phoenix.learningstack.online"},
			},
			want: ModeRelayOperator,
		},
		{
			name: "own domain at the relay",
			cfg: &config.Config{
				School: config.School{Slug: "phoenix"},
				Public: config.Public{Transport: config.TransportRelay, BaseDomain: "ls.gym-phoenix.de"},
			},
			want: ModeRelayOwn,
		},
		{
			name: "direct",
			cfg: &config.Config{
				School: config.School{Slug: "phoenix"},
				Public: config.Public{Transport: config.TransportDirect, BaseDomain: "ls.gym-phoenix.de"},
			},
			want: ModeDirect,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Mode(tt.cfg); got != tt.want {
				t.Errorf("Mode = %q, want %q", got, tt.want)
			}
		})
	}
}
