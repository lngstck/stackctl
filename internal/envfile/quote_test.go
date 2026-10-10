package envfile

import (
	"strings"
	"testing"
)

// Werte mit $, #, Anführungszeichen oder Leerzeichen las docker compose
// anders zurück, als stackctl sie geschrieben hatte: Aus "$TY3ry3" in einem
// Admin-Passwort wurde eine leere Variable. stackctl muss jeden Wert
// unverändert zurücklesen, compose ebenso.
func TestQuoteRoundTrip(t *testing.T) {
	for _, v := range []string{
		"plain", "abc$TY3ry3!x", "a#b", "a #b", `say "hi"`, "it's", `back\slash`,
		`end\`, "$$", " lead", "trail ", "", `\"$`,
	} {
		f := New()
		f.Set(GlobalSection, "K", v)
		back, err := Parse(f.Render())
		if err != nil {
			t.Fatalf("%q: %v", v, err)
		}
		if got, _ := back.Get("K"); got != v {
			t.Errorf("Rundreise %q → %q (Zeile %q)", v, got, quote(v))
		}
	}
}

func TestQuoteLeavesGeneratedSecretsAlone(t *testing.T) {
	for _, v := range []string{"Xk3vP9q2", "sk-ls-3f9a_x.y", "postgresql+psycopg://u:p@ls-postgres:5432/db"} {
		if got := quote(v); got != v {
			t.Errorf("quote(%q) = %q, soll unverändert bleiben", v, got)
		}
	}
}

// So liest compose-go doppelt gequotete Werte: \\ und \" sind Escapes, $$
// ist ein wörtliches $.
func TestQuoteForCompose(t *testing.T) {
	for v, want := range map[string]string{
		"abc$TY3ry3!": `"abc$$TY3ry3!"`,
		`say "hi"`:    `"say \"hi\""`,
		`back\slash`:  `"back\\slash"`,
		"a #b":        `"a #b"`,
	} {
		if got := quote(v); got != want {
			t.Errorf("quote(%q) = %s, want %s", v, got, want)
		}
	}
	f := New()
	f.Set(GlobalSection, "ADMIN_PASSWORD", "abc$TY3ry3!")
	if !strings.Contains(f.Render(), `ADMIN_PASSWORD="abc$$TY3ry3!"`) {
		t.Errorf("Render:\n%s", f.Render())
	}
}
