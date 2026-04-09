package secrets

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// BcryptCost is the bcrypt work factor used for admin passwords
// (ARCHITECTURE.md §16). Bumping this invalidates nothing because the
// cost is embedded in every hash.
const BcryptCost = 10

// HashPassword returns a bcrypt hash at BcryptCost. Empty passwords are
// rejected — setup must force a real password.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", errors.New("secrets: password must not be empty")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	if err != nil {
		return "", fmt.Errorf("secrets: bcrypt: %w", err)
	}
	return string(h), nil
}

// VerifyPassword checks a plaintext password against a bcrypt hash.
// Returns true on match, false on mismatch or malformed hash.
func VerifyPassword(hash, password string) bool {
	if hash == "" || password == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
