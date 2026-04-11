// Package tunnel manages SSH reverse tunnels to sish.learningstack.online.
//
// stackctl uses autossh (falling back to plain ssh) for each tunnel.
// The Manager tracks child processes in-memory with a background monitor
// goroutine that restarts dead tunnels automatically.
package tunnel

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/lngstck/stackctl/internal/paths"
)

// EnsureKey generates an ed25519 SSH key pair if one does not already exist.
// Returns nil if the key already exists.
func EnsureKey() error {
	if KeyExists() {
		return nil
	}
	if err := paths.EnsureDir(paths.ConfigDir(), 0o750); err != nil {
		return err
	}
	keyFile := paths.TunnelKeyFile()
	cmd := exec.Command(
		"ssh-keygen", "-t", "ed25519",
		"-N", "", // no passphrase
		"-C", "stackctl-tunnel",
		"-f", keyFile,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ssh-keygen: %w\n%s", err, out)
	}
	// Ensure private key is 0600.
	if err := os.Chmod(keyFile, 0o600); err != nil {
		return fmt.Errorf("chmod tunnel key: %w", err)
	}
	return nil
}

// KeyExists reports whether the tunnel SSH key has been generated.
func KeyExists() bool {
	_, err := os.Stat(paths.TunnelKeyFile())
	return err == nil
}

// PublicKey reads and returns the public key contents.
// Returns an error wrapping os.ErrNotExist if the key has not been generated.
func PublicKey() (string, error) {
	data, err := os.ReadFile(paths.TunnelPubKeyFile())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("tunnel key not generated: %w", err)
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
