package secrets

import (
	"strings"
	"testing"
)

func TestRandomHexLength(t *testing.T) {
	s, err := RandomHex(16)
	if err != nil {
		t.Fatalf("RandomHex: %v", err)
	}
	if len(s) != 32 {
		t.Errorf("len = %d, want 32", len(s))
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			t.Errorf("non-hex char: %q", r)
		}
	}
}

func TestRandomHexRejectsNonPositive(t *testing.T) {
	if _, err := RandomHex(0); err == nil {
		t.Error("expected error for 0")
	}
	if _, err := RandomHex(-1); err == nil {
		t.Error("expected error for -1")
	}
}

func TestRandomHexUnique(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 50; i++ {
		s, err := RandomHex(16)
		if err != nil {
			t.Fatalf("RandomHex: %v", err)
		}
		if _, dup := seen[s]; dup {
			t.Fatalf("duplicate after %d iterations", i)
		}
		seen[s] = struct{}{}
	}
}

func TestRandomPasswordLength(t *testing.T) {
	p, err := RandomPassword(20)
	if err != nil {
		t.Fatalf("RandomPassword: %v", err)
	}
	if len(p) != 20 {
		t.Errorf("len = %d, want 20", len(p))
	}
	for _, c := range []byte(p) {
		if !strings.ContainsRune(passwordAlphabet, rune(c)) {
			t.Errorf("unexpected char %q", c)
		}
	}
}

func TestRandomPasswordDefaultLength(t *testing.T) {
	p, err := RandomPassword(0)
	if err != nil {
		t.Fatalf("RandomPassword(0): %v", err)
	}
	if len(p) != DefaultPasswordLength {
		t.Errorf("len = %d, want %d", len(p), DefaultPasswordLength)
	}
}

func TestRandomPasswordRejectsShort(t *testing.T) {
	if _, err := RandomPassword(4); err == nil {
		t.Error("expected error for length 4")
	}
}

func TestRandomAPIKey(t *testing.T) {
	key, err := RandomAPIKey("sk")
	if err != nil {
		t.Fatalf("RandomAPIKey: %v", err)
	}
	if !strings.HasPrefix(key, "sk_") {
		t.Errorf("prefix missing: %q", key)
	}
	hexPart := strings.TrimPrefix(key, "sk_")
	if len(hexPart) != 32 {
		t.Errorf("hex part len = %d, want 32", len(hexPart))
	}
}

func TestRandomAPIKeyRejectsEmptyPrefix(t *testing.T) {
	if _, err := RandomAPIKey(""); err == nil {
		t.Error("expected error for empty prefix")
	}
}

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$2") {
		t.Errorf("unexpected hash format: %q", hash)
	}
	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Error("VerifyPassword should succeed on correct password")
	}
	if VerifyPassword(hash, "wrong password") {
		t.Error("VerifyPassword should fail on wrong password")
	}
}

func TestHashPasswordRejectsEmpty(t *testing.T) {
	if _, err := HashPassword(""); err == nil {
		t.Error("expected error for empty password")
	}
}

func TestVerifyPasswordEmptyHash(t *testing.T) {
	if VerifyPassword("", "pw") {
		t.Error("empty hash should not verify")
	}
	if VerifyPassword("notabcrypthash", "pw") {
		t.Error("garbage hash should not verify")
	}
}
