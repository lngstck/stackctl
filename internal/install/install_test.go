package install

import (
	"testing"

	"github.com/lngstck/stackctl/internal/catalog"
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
