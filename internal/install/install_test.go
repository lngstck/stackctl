package install

import (
	"testing"

	"github.com/lngstck/stackctl/internal/catalog"
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
