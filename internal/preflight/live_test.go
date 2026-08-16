package preflight

import (
	"context"
	"crypto/x509"
	"errors"
	"strings"
	"testing"
	"time"
)

func certExpiringIn(d time.Duration) *x509.Certificate {
	c := &x509.Certificate{NotAfter: time.Now().Add(d)}
	c.Issuer.CommonName = "E7"
	return c
}

// liveProber haelt jede Pruefung im Prozess — ohne diese Einstiegspunkte
// wuerden die Tests echte DNS-, TLS- und HTTP-Anfragen ins Netz schicken.
func liveProber(res fakeResolver, local []string, cert *x509.Certificate, certErr error) *Prober {
	p := proberWith(res, local, true)
	p.TLSPeerCert = func(context.Context, string) (*x509.Certificate, error) {
		return cert, certErr
	}
	p.HTTPStatus = func(context.Context, string) (int, error) { return 302, nil }
	return p
}

// Auf dem Betreiber-Relay gehoeren DNS und Zertifikat dem Betreiber. Die
// Schule kann dort nichts kaputtmachen — aber die Adresse muss antworten.
func TestLiveOperatorRelayChecksOnlyReachability(t *testing.T) {
	p := liveProber(fakeResolver{}, nil, certExpiringIn(60*24*time.Hour), nil)

	checks := p.Live(context.Background(), LiveInput{
		Mode:       ModeRelayOperator,
		BaseDomain: "phoenix.learningstack.online",
		AuthHost:   "auth.phoenix.learningstack.online",
	})

	for _, c := range checks {
		if c.ID == "dns_wildcard" || c.ID == "dns_target" {
			t.Errorf("Betreiber-Relay braucht keine DNS-Karte: %+v", c)
		}
	}
	if _, ok := findCheck(checks, "endpoint"); !ok {
		t.Error("Erreichbarkeit fehlt")
	}
	if _, ok := findCheck(checks, "certificate"); !ok {
		t.Error("Zertifikats-Karte fehlt")
	}
}

// Nach dem Setup kann sich DNS aendern — genau dafuer laeuft die Pruefung
// weiter. Ein Eintrag, der auf einen fremden Rechner umgebogen wurde, ist im
// direkten Betrieb der Unterschied zwischen "laeuft" und "weg".
func TestLiveDetectsDNSDrift(t *testing.T) {
	p := liveProber(
		fakeResolver{"*.ls.gym-phoenix.de": {"203.0.113.99"}},
		[]string{"198.51.100.7"},
		certExpiringIn(60*24*time.Hour), nil,
	)

	checks := p.Live(context.Background(), LiveInput{
		Mode: ModeDirect, BaseDomain: "ls.gym-phoenix.de", AuthHost: "auth.ls.gym-phoenix.de",
	})

	got, ok := findCheck(checks, "dns_target")
	if !ok {
		t.Fatal("dns_target fehlt")
	}
	if got.Status != StatusWarn {
		t.Errorf("dns_target = %q, want warn", got.Status)
	}
	if !strings.Contains(got.Detail, "203.0.113.99") {
		t.Errorf("Detail muss die abweichende Adresse nennen: %q", got.Detail)
	}
}

func TestLiveCertificateStates(t *testing.T) {
	tests := []struct {
		name       string
		cert       *x509.Certificate
		certErr    error
		want       string
		wantDetail string
	}{
		{
			name: "reichlich Zeit", cert: certExpiringIn(60 * 24 * time.Hour),
			want: StatusOK, wantDetail: "E7",
		},
		{
			// Caddy erneuert rund 30 Tage vorher. Unter zwei Wochen heisst:
			// die Erneuerung scheitert schon eine Weile still.
			name: "Erneuerung scheitert offenbar", cert: certExpiringIn(5 * 24 * time.Hour),
			want: StatusWarn, wantDetail: "Port 80",
		},
		{
			name: "abgelaufen", cert: certExpiringIn(-2 * time.Hour),
			want: StatusFail, wantDetail: "Abgelaufen",
		},
		{
			// Ist der Endpunkt unten, meldet das die Karte darueber im
			// Klartext. Ein zweiter Alarm fuer dieselbe Ursache waere nur
			// Laerm — deshalb warn statt fail.
			name: "nicht erreichbar", certErr: errors.New("connection refused"),
			want: StatusWarn, wantDetail: "Konnte nicht geprüft werden",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := liveProber(fakeResolver{}, nil, tt.cert, tt.certErr)

			checks := p.Live(context.Background(), LiveInput{
				Mode: ModeRelayOperator, BaseDomain: "phoenix.learningstack.online",
				AuthHost: "auth.phoenix.learningstack.online",
			})

			got, ok := findCheck(checks, "certificate")
			if !ok {
				t.Fatal("Zertifikats-Karte fehlt")
			}
			if got.Status != tt.want {
				t.Errorf("Status = %q, want %q (%s)", got.Status, tt.want, got.Detail)
			}
			if !strings.Contains(got.Detail, tt.wantDetail) {
				t.Errorf("Detail = %q, soll %q enthalten", got.Detail, tt.wantDetail)
			}
		})
	}
}

// Antwortet die Adresse nicht, ist das der eine Befund, der zaehlt: alles
// andere kann richtig aussehen und die Seite trotzdem unerreichbar sein.
func TestLiveUnreachableEndpointFails(t *testing.T) {
	p := liveProber(fakeResolver{}, nil, certExpiringIn(60*24*time.Hour), nil)
	p.HTTPStatus = func(context.Context, string) (int, error) {
		return 0, errors.New("dial tcp: connect: connection refused")
	}

	checks := p.Live(context.Background(), LiveInput{
		Mode: ModeRelayOperator, AuthHost: "auth.phoenix.learningstack.online",
	})

	got, ok := findCheck(checks, "endpoint")
	if !ok {
		t.Fatal("endpoint fehlt")
	}
	if got.Status != StatusFail {
		t.Errorf("endpoint = %q, want fail", got.Status)
	}
	if !strings.Contains(got.Detail, "auth.phoenix.learningstack.online") {
		t.Errorf("Detail muss den Host nennen: %q", got.Detail)
	}
}

// Ohne konfigurierte Adresse gibt es nichts zu messen — und ein erfundenes
// Urteil waere schlechter als ein ehrliches "offen".
func TestLiveWithoutAddressSkips(t *testing.T) {
	p := liveProber(fakeResolver{}, nil, nil, errors.New("kein Host"))

	checks := p.Live(context.Background(), LiveInput{Mode: ModeRelayOperator})

	for _, id := range []string{"endpoint", "certificate"} {
		got, ok := findCheck(checks, id)
		if !ok {
			t.Fatalf("%s fehlt", id)
		}
		if got.Status != StatusSkip {
			t.Errorf("%s = %q, want skip", id, got.Status)
		}
	}
}

func findCheck(checks []Check, id string) (Check, bool) {
	for _, c := range checks {
		if c.ID == id {
			return c, true
		}
	}
	return Check{}, false
}
