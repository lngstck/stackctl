package install

import (
	"strings"
	"testing"

	"github.com/lngstck/stackctl/internal/catalog"
	"github.com/lngstck/stackctl/internal/compose"
	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/envfile"
)

func TestDependsOn(t *testing.T) {
	def := &catalog.Definition{}
	def.DependsOn = []string{"postgres", "dex"}

	if !dependsOn(def, "postgres") {
		t.Error("should depend on postgres")
	}
	if dependsOn(def, "redis") {
		t.Error("should not depend on redis")
	}
}

func TestGenerateSecretTypes(t *testing.T) {
	// secret (default)
	val, err := generateSecret(catalog.SecretSpec{Key: "X", Generate: "secret"})
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	if len(val) != 40 { // 20 bytes = 40 hex chars
		t.Errorf("secret len = %d, want 40", len(val))
	}

	// password
	val, err = generateSecret(catalog.SecretSpec{Key: "X", Generate: "password"})
	if err != nil {
		t.Fatalf("password: %v", err)
	}
	if len(val) < 8 {
		t.Errorf("password too short: %d", len(val))
	}

	// api_key
	val, err = generateSecret(catalog.SecretSpec{Key: "X", Generate: "api_key", Prefix: "sk-lf"})
	if err != nil {
		t.Fatalf("api_key: %v", err)
	}
	if val[:6] != "sk-lf_" {
		t.Errorf("api_key prefix: %q", val[:6])
	}

	// empty generate → defaults to secret
	val, err = generateSecret(catalog.SecretSpec{Key: "X"})
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	if len(val) != 40 {
		t.Errorf("default len = %d", len(val))
	}
}

func TestCollectComposeDefs(t *testing.T) {
	existing := []*catalog.Definition{
		{},
	}
	existing[0].ID = "postgres"

	newDef := &catalog.Definition{}
	newDef.ID = "langflow"

	result := collectComposeDefs(existing, newDef)
	if len(result) != 2 {
		t.Errorf("count = %d, want 2", len(result))
	}
	ids := map[string]bool{}
	for _, d := range result {
		ids[d.ID] = true
	}
	if !ids["postgres"] || !ids["langflow"] {
		t.Errorf("IDs = %v", ids)
	}
}

func TestReconstructDexClients(t *testing.T) {
	cfg := &config.Config{}
	cfg.School.Slug = "demo"

	owui := &catalog.Definition{}
	owui.ID = "open-webui"
	owui.Name = "Open WebUI"
	owui.OIDC = &catalog.OIDCSpec{ClientID: "open-webui", RedirectPath: "/oauth/oidc/callback"}

	pl := &catalog.Definition{}
	pl.ID = "pylearn"
	pl.Name = "PyLearn"
	pl.OIDC = &catalog.OIDCSpec{ClientID: "pylearn", RedirectPath: "/auth/callback"}

	// Hat einen oidc:-Block, aber noch KEIN Secret → muss uebersprungen werden.
	pending := &catalog.Definition{}
	pending.ID = "grafana"
	pending.Name = "Grafana"
	pending.OIDC = &catalog.OIDCSpec{ClientID: "grafana", RedirectPath: "/login/generic_oauth"}

	// Keine OIDC → kein Client.
	noOIDC := &catalog.Definition{}
	noOIDC.ID = "postgres"

	env := envfile.New()
	env.Set("open-webui", "OPEN_WEBUI_OIDC_SECRET", "owui-secret")
	env.Set("pylearn", "PYLEARN_OIDC_SECRET", "pl-secret")

	defs := []*catalog.Definition{owui, pl, pending, noOIDC}
	clients := ReconstructDexClients(defs, env, cfg)

	if len(clients) != 2 {
		t.Fatalf("got %d clients, want 2 (pending hat kein Secret, postgres kein oidc)", len(clients))
	}
	byID := map[string]string{}
	redirects := map[string]string{}
	for _, c := range clients {
		byID[c.ID] = c.Secret
		if len(c.RedirectURIs) == 1 {
			redirects[c.ID] = c.RedirectURIs[0]
		}
	}
	if byID["open-webui"] != "owui-secret" {
		t.Errorf("open-webui secret = %q", byID["open-webui"])
	}
	if byID["pylearn"] != "pl-secret" {
		t.Errorf("pylearn secret = %q", byID["pylearn"])
	}
	if redirects["open-webui"] != "https://open-webui.demo.learningstack.online/oauth/oidc/callback" {
		t.Errorf("open-webui redirect = %q", redirects["open-webui"])
	}
	if _, ok := byID["grafana"]; ok {
		t.Error("grafana sollte ohne Secret uebersprungen werden")
	}
}

func TestCollectComposeDefsNoDuplicate(t *testing.T) {
	existing := []*catalog.Definition{
		{},
	}
	existing[0].ID = "langflow"

	newDef := &catalog.Definition{}
	newDef.ID = "langflow"

	result := collectComposeDefs(existing, newDef)
	if len(result) != 1 {
		t.Errorf("count = %d, want 1 (no duplicate)", len(result))
	}
}

func TestExpandMessagePlaceholders(t *testing.T) {
	env := envfile.New()
	env.Set(envfile.GlobalSection, "SCHOOL_SLUG", "phoenix")

	cfg := &config.Config{}
	cfg.School.Slug = "phoenix"
	cfg.School.ServerDomain = "server.local"

	cases := []struct {
		name string
		in   string
		want string
	}{
		// Die Katalog-YAMLs nutzen ueberwiegend die Klammer-Form; vor dem Fix
		// blieb sie woertlich stehen, weil os.Expand nur $-Syntax kennt.
		{"brace lower", "https://a.{school_slug}.x/admin", "https://a.phoenix.x/admin"},
		{"brace upper", "https://a.{SCHOOL_SLUG}.x/station", "https://a.phoenix.x/station"},
		{"server domain", "http://{server_domain}:8340", "http://server.local:8340"},
		{"app id", "Container ls-{app_id}", "Container ls-sponsorenlauf"},
		{"dollar form still works", "https://a.${SCHOOL_SLUG}.x", "https://a.phoenix.x"},
		{"mixed", "{server_domain} + ${SCHOOL_SLUG}", "server.local + phoenix"},
		{"unknown env var stays put", "${NOPE}", "${NOPE}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := expandMessage(tc.in, env, cfg, "sponsorenlauf"); got != tc.want {
				t.Errorf("expandMessage(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Die Adress-Platzhalter sind der Ersatz fuer die frueher fest eingebaute
// Root-Domain: eine Katalog-YAML kann die oeffentliche Adresse nicht mehr aus
// dem Slug zusammensetzen, weil sie von der Betriebsart abhaengt. caddy.yaml
// nutzt {public_base_domain} bereits — ohne diese Ersetzung stuende die
// Klammer woertlich in der Meldung nach der Installation.
func TestExpandMessagePublicAddressPlaceholders(t *testing.T) {
	env := envfile.New()

	cases := []struct {
		name string
		cfg  *config.Config
		in   string
		want string
	}{
		{
			name: "relay des betreibers",
			cfg: &config.Config{
				School: config.School{Slug: "phoenix"},
				Public: config.Public{Transport: config.TransportRelay, BaseDomain: "phoenix.learningstack.online"},
			},
			in:   "*.{public_base_domain} → {public_app_url}",
			want: "*.phoenix.learningstack.online → https://sponsorenlauf.phoenix.learningstack.online",
		},
		{
			name: "eigene domain, direkter betrieb",
			cfg: &config.Config{
				School: config.School{Slug: "phoenix"},
				Public: config.Public{Transport: config.TransportDirect, BaseDomain: "ls.gym-phoenix.de"},
			},
			in:   "*.{public_base_domain} → {public_app_url}, Login {public_auth_url}",
			want: "*.ls.gym-phoenix.de → https://sponsorenlauf.ls.gym-phoenix.de, Login https://auth.ls.gym-phoenix.de",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := expandMessage(tc.in, env, tc.cfg, "sponsorenlauf"); got != tc.want {
				t.Errorf("expandMessage = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExpandMessageNilConfig(t *testing.T) {
	// Defensive: ohne Config duerfen die Env-Platzhalter trotzdem aufloesen.
	env := envfile.New()
	env.Set(envfile.GlobalSection, "SCHOOL_SLUG", "phoenix")
	if got := expandMessage("${SCHOOL_SLUG}", env, nil, "x"); got != "phoenix" {
		t.Errorf("got %q, want %q", got, "phoenix")
	}
}

func oidcDef() *catalog.Definition {
	def := &catalog.Definition{}
	def.ID = "pylearn"
	def.Name = "PyLearn"
	def.OIDC = &catalog.OIDCSpec{ClientID: "pylearn", RedirectPath: "/auth/callback"}
	def.Ports = []compose.PortSpec{{Host: 8330, Container: 8000}}
	return def
}

func directCfgFor(domain string) *config.Config {
	return &config.Config{
		School: config.School{Slug: "phoenix"},
		Public: config.Public{Transport: config.TransportDirect, BaseDomain: domain},
	}
}

// Die Adresse der App gehoert in .env, damit eine Katalog-Definition sie nicht
// mehr selbst aus Slug + Root-Domain zusammenbauen muss.
func TestApplyAddressEnvWritesPublicAddress(t *testing.T) {
	env := envfile.New()
	def := oidcDef()

	keys, redirectURI := applyAddressEnv(def, directCfgFor("ls.gym-phoenix.de"), env)

	if got, _ := env.Get("PYLEARN_PUBLIC_URL"); got != "https://pylearn.ls.gym-phoenix.de" {
		t.Errorf("PYLEARN_PUBLIC_URL = %q", got)
	}
	if got, _ := env.Get("PYLEARN_OIDC_REDIRECT_URI"); got != "https://pylearn.ls.gym-phoenix.de/auth/callback" {
		t.Errorf("PYLEARN_OIDC_REDIRECT_URI = %q", got)
	}
	if redirectURI != "https://pylearn.ls.gym-phoenix.de/auth/callback" {
		t.Errorf("zurueckgegebene Redirect-URI = %q", redirectURI)
	}
	if len(keys) != 2 {
		t.Errorf("gesetzte Keys = %v, want 2", keys)
	}
}

// Die Schluessel tragen die App-ID, weil .env ein flacher Namensraum ist —
// die Abschnitte im File sind Layout, kein Geltungsbereich. Ohne Praefix
// wuerde die naechste installierte App den Wert der vorigen ueberschreiben.
func TestAddressEnvKeysDoNotCollide(t *testing.T) {
	env := envfile.New()
	cfg := directCfgFor("ls.gym-phoenix.de")

	first := &catalog.Definition{}
	first.ID = "pylearn"
	second := &catalog.Definition{}
	second.ID = "sponsorenlauf"

	applyAddressEnv(first, cfg, env)
	applyAddressEnv(second, cfg, env)

	if got, _ := env.Get("PYLEARN_PUBLIC_URL"); got != "https://pylearn.ls.gym-phoenix.de" {
		t.Errorf("erste App wurde ueberschrieben: %q", got)
	}
	if got, _ := env.Get("SPONSORENLAUF_PUBLIC_URL"); got != "https://sponsorenlauf.ls.gym-phoenix.de" {
		t.Errorf("zweite App = %q", got)
	}
}

// Bindestriche sind in Env-Keys nicht erlaubt — open-webui wuerde sonst einen
// Key erzeugen, den die Shell-Expansion nicht aufloest.
func TestAddressEnvKeyNormalisesDashes(t *testing.T) {
	if got := PublicURLEnvKey("open-webui"); got != "OPEN_WEBUI_PUBLIC_URL" {
		t.Errorf("PublicURLEnvKey = %q", got)
	}
}

// Der eigentliche Zweck: eine Definition, die ihre Redirect-URI selbst baut,
// widerspricht dem, was in Dex landet. Dex meldet den Fall mit einer Seite,
// die keine der beiden URIs nennt — deshalb hier abbrechen.
func TestCheckRedirectURIsCatchesHardcodedAddress(t *testing.T) {
	env := envfile.New()
	env.Set(envfile.GlobalSection, "SCHOOL_SLUG", "phoenix")
	def := oidcDef()
	def.Environment = []compose.EnvVar{
		{Key: "OIDC_REDIRECT_URI", Value: "https://pylearn.${SCHOOL_SLUG}.learningstack.online/auth/callback"},
	}

	err := checkRedirectURIs(def, env, "https://pylearn.ls.gym-phoenix.de/auth/callback")
	if err == nil {
		t.Fatal("Widerspruch wurde nicht erkannt")
	}
	// Die Meldung muss beide Adressen und den Ausweg nennen — sonst sucht
	// jemand im falschen File.
	for _, want := range []string{
		"pylearn.phoenix.learningstack.online",
		"pylearn.ls.gym-phoenix.de",
		"PYLEARN_OIDC_REDIRECT_URI",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Meldung nennt %q nicht: %v", want, err)
		}
	}
}

func TestCheckRedirectURIsAcceptsInjectedValue(t *testing.T) {
	env := envfile.New()
	def := oidcDef()
	cfg := directCfgFor("ls.gym-phoenix.de")
	_, redirectURI := applyAddressEnv(def, cfg, env)
	def.Environment = []compose.EnvVar{
		{Key: "OIDC_REDIRECT_URI", Value: "${PYLEARN_OIDC_REDIRECT_URI}"},
	}

	if err := checkRedirectURIs(def, env, redirectURI); err != nil {
		t.Errorf("korrekte Definition abgelehnt: %v", err)
	}
}

// Ohne OIDC-Block registriert stackctl nichts — dann gibt es auch nichts,
// dem eine App widersprechen koennte.
func TestCheckRedirectURIsIgnoresAppsWithoutOIDC(t *testing.T) {
	def := &catalog.Definition{}
	def.ID = "grafana"
	def.Environment = []compose.EnvVar{
		{Key: "SOME_REDIRECT_URI", Value: "https://irgendwo.example.org/cb"},
	}

	if err := checkRedirectURIs(def, envfile.New(), ""); err != nil {
		t.Errorf("App ohne OIDC abgelehnt: %v", err)
	}
}
