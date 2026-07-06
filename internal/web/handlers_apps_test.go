package web

import (
	"strings"
	"testing"
)

func TestLinkifyAdminNotes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string // substrings that must appear
		not  []string // substrings that must NOT appear
	}{
		{
			name: "plain text bleibt escaped",
			in:   "Kein Link, aber <b>Markup</b> & Sonderzeichen.",
			want: []string{"&lt;b&gt;Markup&lt;/b&gt;", "&amp;"},
			not:  []string{"<b>"},
		},
		{
			name: "URL wird zum Anker",
			in:   "Anleitung unter:\nhttps://sponsorenlauf.schule.example/admin\nDanach Token eingeben.",
			want: []string{
				`<a href="https://sponsorenlauf.schule.example/admin" target="_blank" rel="noopener">https://sponsorenlauf.schule.example/admin</a>`,
				"Danach Token eingeben.",
			},
		},
		{
			name: "Satzzeichen am Ende bleibt draussen",
			in:   "Siehe https://example.org/docs.",
			want: []string{`href="https://example.org/docs"`, "</a>."},
			not:  []string{`href="https://example.org/docs."`},
		},
		{
			name: "Injection ueber URL-Umgebung unmoeglich",
			in:   `<script>x</script> https://example.org/?a=1&b=2 <img src=x>`,
			want: []string{"&lt;script&gt;", `href="https://example.org/?a=1&amp;b=2"`, "&lt;img"},
			not:  []string{"<script>", "<img"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(linkifyAdminNotes(tc.in))
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("Ergebnis enthaelt %q nicht:\n%s", w, got)
				}
			}
			for _, n := range tc.not {
				if strings.Contains(got, n) {
					t.Errorf("Ergebnis enthaelt unerwartet %q:\n%s", n, got)
				}
			}
		})
	}

	if linkifyAdminNotes("") != "" {
		t.Error("leerer Input muss leer bleiben")
	}
}
