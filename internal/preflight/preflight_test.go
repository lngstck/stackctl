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
// NXDOMAIN, which is what an admin sees before the record propagates. CNAME
// records are keyed "cname:<host>" and answered through cname().
type fakeResolver map[string][]string

func (f fakeResolver) cname(_ context.Context, host string) (string, error) {
	if target, ok := f["cname:"+host]; ok && len(target) > 0 {
		return target[0] + ".", nil
	}
	return "", errNoCNAME
}

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
		CNAME:    res.cname,
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
	local := &config.Config{Public: config.Public{Transport: config.TransportLocal, BaseDomain: "ls.gym-phoenix.de"}}
	if got := Mode(local); got != ModeLocal {
		t.Errorf("Mode = %q, want %q", got, ModeLocal)
	}
	// A transport this build does not know has no mode, rather than being
	// mistaken for one that would show the wrong instructions.
	if got := Mode(&config.Config{Public: config.Public{Transport: "relay"}}); got != "" {
		t.Errorf("Mode(relay) = %q, want empty", got)
	}
}

// localProber is set up for the school network: the server sits on
// 192.168.1.10, the challenge is delegated to deSEC, and the token manages
// that zone.
func localProber(res fakeResolver) *Prober {
	p := proberWith(res, []string{"127.0.0.1", "192.168.1.10"}, true)
	p.DNSZones = func(context.Context, string) ([]string, error) {
		return []string{"gym-phoenix.dedyn.io"}, nil
	}
	return p
}

func localInput() Input {
	return Input{
		Mode:            ModeLocal,
		BaseDomain:      "ls.gym-phoenix.de",
		DNSToken:        "token",
		ChallengeDomain: "_acme-challenge.gym-phoenix.dedyn.io",
	}
}

func localDNS() fakeResolver {
	return fakeResolver{
		"*.ls.gym-phoenix.de":                     {"192.168.1.10"},
		"cname:_acme-challenge.ls.gym-phoenix.de": {"_acme-challenge.gym-phoenix.dedyn.io"},
	}
}

func TestLocalAllPrerequisitesMet(t *testing.T) {
	checks := localProber(localDNS()).Run(context.Background(), localInput())

	if got := Worst(checks); got != StatusOK {
		t.Errorf("worst = %q, want ok: %+v", got, checks)
	}
	for _, id := range []string{"base_domain", "dns_wildcard", "dns_target", "dns_challenge", "dns_token", "port_80", "port_443"} {
		if got := byID(t, checks, id).Status; got != StatusOK {
			t.Errorf("check %s = %q, want ok", id, got)
		}
	}
}

// Im Schulnetz-Betrieb ist eine oeffentliche Adresse schlicht falsch — sie
// gehoert zum direkten Betrieb.
func TestLocalPublicAddressFails(t *testing.T) {
	dns := localDNS()
	dns["*.ls.gym-phoenix.de"] = []string{"203.0.113.9"}

	got := byID(t, localProber(dns).Run(context.Background(), localInput()), "dns_target")
	if got.Status != StatusFail {
		t.Errorf("dns_target = %q, want fail", got.Status)
	}
	if !strings.Contains(got.Detail, "192.168.1.10") {
		t.Errorf("Detail soll die richtige Adresse nennen: %q", got.Detail)
	}
}

// Eine andere private Adresse kann ein Weiterleiter sein — nicht bestaetigbar,
// also gelb statt rot.
func TestLocalOtherPrivateAddressWarns(t *testing.T) {
	dns := localDNS()
	dns["*.ls.gym-phoenix.de"] = []string{"192.168.1.99"}

	got := byID(t, localProber(dns).Run(context.Background(), localInput()), "dns_target")
	if got.Status != StatusWarn {
		t.Errorf("dns_target = %q, want warn", got.Status)
	}
}

// Der Eintrag existiert, kommt aber im Schulnetz nicht an: Rebind-Schutz. Die
// Loesung liegt im Router, nicht beim DNS-Anbieter — das muss der Hinweis sagen.
func TestLocalDetectsRebindProtection(t *testing.T) {
	p := localProber(fakeResolver{})
	p.PublicResolver = fakeResolver{"*.ls.gym-phoenix.de": {"192.168.1.10"}}

	checks := p.Run(context.Background(), localInput())

	got := byID(t, checks, "dns_wildcard")
	if got.Status != StatusFail {
		t.Errorf("dns_wildcard = %q, want fail", got.Status)
	}
	if !strings.Contains(got.Detail, "Rebind") || !strings.Contains(got.Detail, "Router") {
		t.Errorf("Detail soll auf den Rebind-Schutz im Router zeigen: %q", got.Detail)
	}
}

// Ohne jeden Eintrag — auch oeffentlich nicht — ist es ein fehlender Eintrag,
// kein Rebind-Schutz.
func TestLocalMissingRecordIsNotRebind(t *testing.T) {
	p := localProber(fakeResolver{})
	p.PublicResolver = fakeResolver{}

	got := byID(t, p.Run(context.Background(), localInput()), "dns_wildcard")
	if strings.Contains(got.Detail, "Rebind") {
		t.Errorf("fehlender Eintrag als Rebind gemeldet: %q", got.Detail)
	}
	if !strings.Contains(got.Detail, "im Schulnetz") {
		t.Errorf("Hinweis soll auf die Adresse im Schulnetz zeigen: %q", got.Detail)
	}
}

func TestLocalChallengeDelegation(t *testing.T) {
	missing := localDNS()
	delete(missing, "cname:_acme-challenge.ls.gym-phoenix.de")
	got := byID(t, localProber(missing).Run(context.Background(), localInput()), "dns_challenge")
	if got.Status != StatusFail || !strings.Contains(got.Detail, "CNAME _acme-challenge.gym-phoenix.dedyn.io") {
		t.Errorf("fehlender CNAME: %q %q", got.Status, got.Detail)
	}

	wrong := localDNS()
	wrong["cname:_acme-challenge.ls.gym-phoenix.de"] = []string{"_acme-challenge.andere.dedyn.io"}
	got = byID(t, localProber(wrong).Run(context.Background(), localInput()), "dns_challenge")
	if got.Status != StatusFail || !strings.Contains(got.Detail, "andere.dedyn.io") {
		t.Errorf("falscher CNAME: %q %q", got.Status, got.Detail)
	}

	// Ohne Delegationsziel liegt die Domain selbst bei deSEC — dann gibt es
	// keinen CNAME zu pruefen.
	in := localInput()
	in.ChallengeDomain = ""
	for _, c := range localProber(localDNS()).Run(context.Background(), in) {
		if c.ID == "dns_challenge" {
			t.Errorf("ohne Delegationsziel keine CNAME-Pruefung, got %+v", c)
		}
	}
}

// handedOver is the standard school-network setup: the school gave its
// domain to deSEC with two NS records; the token manages it there.
func handedOver(res fakeResolver, ns map[string][]string) *Prober {
	p := proberWith(res, []string{"127.0.0.1", "192.168.1.10"}, true)
	p.DNSZones = func(context.Context, string) ([]string, error) {
		return []string{"ls.gym-phoenix.de"}, nil
	}
	p.NS = func(_ context.Context, name string) ([]string, error) {
		if servers, ok := ns[name]; ok {
			return servers, nil
		}
		return nil, errors.New("no such host")
	}
	return p
}

func handedOverInput() Input {
	return Input{Mode: ModeLocal, BaseDomain: "ls.gym-phoenix.de", DNSToken: "token"}
}

var desecNS = []string{"ns1.desec.io.", "ns2.desec.org."}

// Vor der Einrichtung fehlt der Wildcard-Eintrag zu Recht: stackctl legt ihn
// erst danach bei deSEC an. Das darf das Setup nicht rot faerben.
func TestHandedOverBeforeSetup(t *testing.T) {
	p := handedOver(fakeResolver{}, map[string][]string{"ls.gym-phoenix.de": desecNS})
	p.PublicResolver = fakeResolver{}
	checks := p.Run(context.Background(), handedOverInput())

	if got := Worst(checks); got != StatusOK {
		t.Errorf("worst = %q, want ok: %+v", got, checks)
	}
	if got := byID(t, checks, "dns_delegation"); got.Status != StatusOK {
		t.Errorf("dns_delegation = %q %q", got.Status, got.Detail)
	}
	if got := byID(t, checks, "dns_wildcard"); got.Status != StatusSkip || !strings.Contains(got.Detail, "stackctl") {
		t.Errorf("dns_wildcard = %q %q, want skip mentioning stackctl", got.Status, got.Detail)
	}
	if got := byID(t, checks, "dns_token"); got.Status != StatusOK {
		t.Errorf("dns_token = %q %q", got.Status, got.Detail)
	}
}

// Noch nicht uebergeben: der Hinweis nennt genau die beiden NS-Eintraege.
func TestHandedOverMissingDelegation(t *testing.T) {
	p := handedOver(fakeResolver{}, map[string][]string{
		"gym-phoenix.de": {"ns1045.ui-dns.biz.", "ns1050.ui-dns.com."},
	})
	got := byID(t, p.Run(context.Background(), handedOverInput()), "dns_delegation")
	if got.Status != StatusFail {
		t.Errorf("status = %q, want fail", got.Status)
	}
	for _, want := range []string{"ui-dns", "ls.gym-phoenix.de NS ns1.desec.io.", "ls.gym-phoenix.de NS ns2.desec.org."} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("Detail ohne %q: %q", want, got.Detail)
		}
	}
}

// Hat die Schule gleich die ganze Domain an deSEC gegeben, zaehlt das auch.
func TestHandedOverParentZone(t *testing.T) {
	p := handedOver(fakeResolver{}, map[string][]string{"gym-phoenix.de": desecNS})
	p.DNSZones = func(context.Context, string) ([]string, error) { return []string{"gym-phoenix.de"}, nil }
	checks := p.Run(context.Background(), handedOverInput())
	if got := byID(t, checks, "dns_delegation"); got.Status != StatusOK || !strings.Contains(got.Detail, "gym-phoenix.de") {
		t.Errorf("dns_delegation = %q %q", got.Status, got.Detail)
	}
	if got := byID(t, checks, "dns_token"); got.Status != StatusOK {
		t.Errorf("dns_token = %q %q", got.Status, got.Detail)
	}
}

// Nameserver von deSEC und vom alten Anbieter gemischt: gelb, mit Namen.
func TestHandedOverMixedNameserversWarn(t *testing.T) {
	p := handedOver(fakeResolver{}, map[string][]string{
		"ls.gym-phoenix.de": {"ns1.desec.io.", "ns1045.ui-dns.biz."},
	})
	got := byID(t, p.Run(context.Background(), handedOverInput()), "dns_delegation")
	if got.Status != StatusWarn || !strings.Contains(got.Detail, "ui-dns") {
		t.Errorf("dns_delegation = %q %q", got.Status, got.Detail)
	}
}

// Domain bei deSEC nicht angelegt: Der Hinweis sagt, was zu tun ist.
func TestHandedOverDomainNotAtDeSEC(t *testing.T) {
	p := handedOver(fakeResolver{}, map[string][]string{"ls.gym-phoenix.de": desecNS})
	p.DNSZones = func(context.Context, string) ([]string, error) { return []string{"andere.de"}, nil }
	got := byID(t, p.Run(context.Background(), handedOverInput()), "dns_token")
	if got.Status != StatusFail || !strings.Contains(got.Detail, "hinzufügen") {
		t.Errorf("dns_token = %q %q", got.Status, got.Detail)
	}
}

// Der Eintrag existiert schon, zeigt aber woanders hin: Der Hinweis sagt,
// dass stackctl ihn bei der Einrichtung korrigiert.
func TestHandedOverWrongTargetIsCorrected(t *testing.T) {
	p := handedOver(fakeResolver{"*.ls.gym-phoenix.de": {"192.168.1.99"}}, map[string][]string{"ls.gym-phoenix.de": desecNS})
	got := byID(t, p.Run(context.Background(), handedOverInput()), "dns_target")
	if !strings.Contains(got.Detail, "setzt stackctl") {
		t.Errorf("dns_target soll die Korrektur ankuendigen: %q", got.Detail)
	}
}

func TestLocalDNSToken(t *testing.T) {
	in := localInput()
	in.DNSToken = ""
	if got := byID(t, localProber(localDNS()).Run(context.Background(), in), "dns_token"); got.Status != StatusFail {
		t.Errorf("ohne Token: %q, want fail", got.Status)
	}

	p := localProber(localDNS())
	p.DNSZones = func(context.Context, string) ([]string, error) { return nil, errTokenRejected }
	if got := byID(t, p.Run(context.Background(), localInput()), "dns_token"); got.Status != StatusFail || !strings.Contains(got.Detail, "lehnt") {
		t.Errorf("abgelehnter Token: %q %q", got.Status, got.Detail)
	}

	p.DNSZones = func(context.Context, string) ([]string, error) { return []string{"andere.dedyn.io"}, nil }
	if got := byID(t, p.Run(context.Background(), localInput()), "dns_token"); got.Status != StatusFail || !strings.Contains(got.Detail, "andere.dedyn.io") {
		t.Errorf("falsche Zone: %q %q", got.Status, got.Detail)
	}

	// deSEC nicht erreichbar heisst "nicht bestaetigbar", nicht "kaputt".
	p.DNSZones = func(context.Context, string) ([]string, error) { return nil, errors.New("timeout") }
	if got := byID(t, p.Run(context.Background(), localInput()), "dns_token"); got.Status != StatusWarn {
		t.Errorf("deSEC unerreichbar: %q, want warn", got.Status)
	}
}
