package llm

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestGenerateAPIKeyFormat(t *testing.T) {
	plaintext, prefix, hash, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.HasPrefix(prefix, "llm-") || len(prefix) != len("llm-")+8 {
		t.Errorf("prefix wrong shape: %q", prefix)
	}
	if !strings.HasPrefix(plaintext, prefix+"-") {
		t.Errorf("plaintext must start with prefix+'-': %q", plaintext)
	}
	// secret hat 43 chars (base64url von 32 bytes ohne padding)
	secret := strings.TrimPrefix(plaintext, prefix+"-")
	if len(secret) != 43 {
		t.Errorf("secret length = %d, want 43", len(secret))
	}
	// Hash matched gegen Secret
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(secret)); err != nil {
		t.Errorf("hash does not verify against secret: %v", err)
	}
}

func TestGenerateAPIKeyUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		_, prefix, _, err := GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if seen[prefix] {
			t.Errorf("prefix collision after %d iterations: %s", i, prefix)
		}
		seen[prefix] = true
	}
}
