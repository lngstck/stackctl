package llm

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"

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
