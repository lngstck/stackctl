package preflight

import (
	"errors"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func cnameResponse(t *testing.T, id uint16, rcode dnsmessage.RCode, target string) []byte {
	t.Helper()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id, Response: true, RCode: rcode})
	b.EnableCompression()
	name := dnsmessage.MustNewName("_acme-challenge.ls.gym-phoenix.de.")
	if err := b.StartQuestions(); err != nil {
		t.Fatal(err)
	}
	if err := b.Question(dnsmessage.Question{Name: name, Type: dnsmessage.TypeCNAME, Class: dnsmessage.ClassINET}); err != nil {
		t.Fatal(err)
	}
	if err := b.StartAnswers(); err != nil {
		t.Fatal(err)
	}
	if target != "" {
		err := b.CNAMEResource(
			dnsmessage.ResourceHeader{Name: name, Type: dnsmessage.TypeCNAME, Class: dnsmessage.ClassINET, TTL: 300},
			dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName(target)},
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	msg, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

// Die Abfrage fragt nach dem CNAME selbst und folgt ihm nicht — das Ziel bei
// deSEC hat ausserhalb einer Ausstellung gar keinen Eintrag.
func TestParseCNAMEAnswer(t *testing.T) {
	got, err := parseCNAMEAnswer(cnameResponse(t, 7, dnsmessage.RCodeSuccess, "_acme-challenge.gym-phoenix.dedyn.io."), 7)
	if err != nil || got != "_acme-challenge.gym-phoenix.dedyn.io" {
		t.Errorf("got %q, %v", got, err)
	}

	if _, err := parseCNAMEAnswer(cnameResponse(t, 7, dnsmessage.RCodeSuccess, ""), 7); !errors.Is(err, errNoCNAME) {
		t.Errorf("ohne Antwort: err = %v, want errNoCNAME", err)
	}
	if _, err := parseCNAMEAnswer(cnameResponse(t, 7, dnsmessage.RCodeNameError, ""), 7); !errors.Is(err, errNoCNAME) {
		t.Errorf("NXDOMAIN: err = %v, want errNoCNAME", err)
	}
	if _, err := parseCNAMEAnswer(cnameResponse(t, 7, dnsmessage.RCodeSuccess, "x.example.org."), 8); err == nil {
		t.Error("fremde Antwort-ID wurde akzeptiert")
	}
}
