// Package secrets centralises random-value generation for stackctl.
//
// All randomness comes from crypto/rand. Functions return an error if the
// system RNG fails — callers must not ignore it, because silently falling
// back to math/rand would defeat the security guarantees.
package secrets

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
)

// DefaultPasswordLength is the length used by RandomPassword when no
// explicit length is given (ARCHITECTURE.md §11 – generated app passwords).
const DefaultPasswordLength = 20

// passwordAlphabet is the character set used for generated passwords.
// It is intentionally conservative: no quotes, backslash, whitespace, or
// characters that could trip up shells, docker-compose variable
// substitution, or YAML parsers. Upper, lower, digits, plus a few safe
// specials.
const passwordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ" +
	"abcdefghijkmnopqrstuvwxyz" +
	"23456789" +
	"!@#%^&*+-=?"

// RandomHex returns a lowercase hex string of 2*nBytes characters. Use
// this for secrets that live inside config files where arbitrary bytes
// would be unsafe (YAML, .env).
func RandomHex(nBytes int) (string, error) {
	if nBytes <= 0 {
		return "", errors.New("secrets: nBytes must be positive")
	}
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("secrets: read random: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// RandomPassword returns a string of the given length drawn uniformly from
// passwordAlphabet. A length of 0 yields the DefaultPasswordLength.
// Minimum length is 8.
func RandomPassword(length int) (string, error) {
	if length == 0 {
		length = DefaultPasswordLength
	}
	if length < 8 {
		return "", fmt.Errorf("secrets: password length %d too short (min 8)", length)
	}
	alphabet := []byte(passwordAlphabet)
	max := big.NewInt(int64(len(alphabet)))
	out := make([]byte, length)
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("secrets: random int: %w", err)
		}
		out[i] = alphabet[n.Int64()]
	}
	return string(out), nil
}

// RandomAPIKey returns a string of the form "{prefix}_{hex32}", where the
// hex portion is 32 hex characters (16 random bytes). Empty prefix is
// rejected to keep the format uniform.
func RandomAPIKey(prefix string) (string, error) {
	if prefix == "" {
		return "", errors.New("secrets: prefix must not be empty")
	}
	h, err := RandomHex(16)
	if err != nil {
		return "", err
	}
	return prefix + "_" + h, nil
}
