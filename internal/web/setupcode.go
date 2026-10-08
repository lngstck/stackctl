// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/paths"
)

// The setup code closes the window between installing stackctl and finishing
// setup. Without it, whoever reaches port 8090 first in that window sets the
// admin password. stackctl creates the code on its first start, install.sh
// prints it as part of the setup link, and setup only goes through with it.
// Afterwards the file is deleted: the admin password takes over.

// setupCodeAlphabet leaves out characters people confuse when reading the
// code off a terminal: 0/O, 1/I/L.
const setupCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// newSetupCode returns eight characters as XXXX-XXXX, about 40 bits — plenty
// against guessing when every miss counts towards the login rate limit.
func newSetupCode() (string, error) {
	var b strings.Builder
	max := big.NewInt(int64(len(setupCodeAlphabet)))
	for i := 0; i < 8; i++ {
		if i == 4 {
			b.WriteByte('-')
		}
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b.WriteByte(setupCodeAlphabet[n.Int64()])
	}
	return b.String(), nil
}

// normalizeSetupCode makes a typed code comparable: case and the separator
// do not matter, and neither do blanks pasted along with it.
func normalizeSetupCode(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, strings.ToUpper(s))
}

// ensureSetupCode returns the stored setup code, creating it on first use.
// It survives restarts so the link install.sh printed keeps working.
func ensureSetupCode() (string, error) {
	data, err := os.ReadFile(paths.SetupCodeFile())
	if err == nil {
		if code := strings.TrimSpace(string(data)); code != "" {
			return code, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read setup code: %w", err)
	}

	code, err := newSetupCode()
	if err != nil {
		return "", fmt.Errorf("generate setup code: %w", err)
	}
	if err := paths.AtomicWrite(paths.SetupCodeFile(), []byte(code+"\n"), config.FilePerm); err != nil {
		return "", fmt.Errorf("write setup code: %w", err)
	}
	return code, nil
}

// removeSetupCode deletes the code once setup is done.
func removeSetupCode() error {
	if err := os.Remove(paths.SetupCodeFile()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// validSetupCode compares a typed code against the stored one. Without a
// stored code nothing matches: setup stays closed rather than open.
func (s *Server) validSetupCode(typed string) bool {
	want := normalizeSetupCode(s.setupCode)
	got := normalizeSetupCode(typed)
	if want == "" || got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}
