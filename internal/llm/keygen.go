package llm

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// GenerateAPIKey erzeugt einen neuen Klartext-Key fuer llmd im Format
//   llm-<8-hex-prefix>-<43-char-base64url-secret>
//
// Liefert (plaintext, prefix, bcrypt-hash, error). Der Plaintext wird
// nur einmalig dem Aufrufer gezeigt — er ist nicht aus dem Hash
// rekonstruierbar. Der Prefix landet im Klartext in der Config (fuer
// O(1)-Lookup), der Hash separat. Aufbau aequivalent zu llmd-internem
// Parsing in internal/server/auth.go.
func GenerateAPIKey() (plaintext, prefix, hashStr string, err error) {
	prefixBytes := make([]byte, 4)
	if _, err = rand.Read(prefixBytes); err != nil {
		return "", "", "", fmt.Errorf("random prefix: %w", err)
	}
	prefix = "llm-" + hex.EncodeToString(prefixBytes) // 8 hex chars

	secretBytes := make([]byte, 32)
	if _, err = rand.Read(secretBytes); err != nil {
		return "", "", "", fmt.Errorf("random secret: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(secretBytes) // 43 chars

	plaintext = prefix + "-" + secret

	h, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		return "", "", "", fmt.Errorf("bcrypt: %w", err)
	}
	hashStr = string(h)
	return plaintext, prefix, hashStr, nil
}

// HashAPIKey nimmt einen bestehenden Klartext-Key im Format
//   llm-<8hex>-<secret>
// und liefert (prefix, bcrypt-hash) — analog zu GenerateAPIKey, aber ohne
// neuen Zufall. Wird beim Re-Seed von llmd genutzt: wenn LLM_API_KEY schon
// in .env steht (z.B. von einem vorherigen Install), darf seedLLMConfig den
// Klartext nicht neu generieren — sonst muesste Open WebUI ueber den
// .env-Eintrag nachjustiert + neu gestartet werden. Statt dessen
// re-bcrypten wir dieselbe Plaintext-Sekrete (bcrypt liefert bei gleicher
// Eingabe einen anderen Hash, validiert aber dieselbe Plaintext).
func HashAPIKey(plaintext string) (prefix, hashStr string, err error) {
	if !strings.HasPrefix(plaintext, "llm-") {
		return "", "", fmt.Errorf("invalid key format (missing llm- prefix)")
	}
	rest := plaintext[len("llm-"):]
	dash := strings.IndexByte(rest, '-')
	if dash <= 0 || dash == len(rest)-1 {
		return "", "", fmt.Errorf("invalid key format (expected llm-<prefix>-<secret>)")
	}
	prefix = "llm-" + rest[:dash]
	secret := rest[dash+1:]

	h, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		return "", "", fmt.Errorf("bcrypt: %w", err)
	}
	return prefix, string(h), nil
}
