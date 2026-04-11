package tunnel

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"time"
)

// buildSSHCmd constructs an exec.Cmd for an SSH reverse tunnel.
// If autossh is available on PATH it is preferred; otherwise plain ssh is used
// with keepalive flags that provide similar reliability.
//
//	remoteHost: sish virtual host, e.g. "langflow.phoenix"
//	localPort:  local port to forward, e.g. 8320
//	sshHost:    sish server, e.g. "sish.learningstack.online"
//	sshPort:    sish SSH port, usually 22
//	keyPath:    path to ed25519 private key
func buildSSHCmd(remoteHost string, localPort int, sshHost string, sshPort int, keyPath string) *exec.Cmd {
	remote := fmt.Sprintf("%s:80:localhost:%d", remoteHost, localPort)
	sshArgs := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		"-o", "ExitOnForwardFailure=yes",
		"-N",
		"-R", remote,
		"-i", keyPath,
		"-p", strconv.Itoa(sshPort),
		"tunnel@" + sshHost,
	}

	// Prefer autossh if available.
	if autossh, err := exec.LookPath("autossh"); err == nil {
		// AUTOSSH_PORT=0 disables the monitoring port (we use ServerAlive instead).
		cmd := exec.Command(autossh, append([]string{"-M", "0"}, sshArgs...)...)
		cmd.Env = append(cmd.Environ(), "AUTOSSH_PORT=0")
		return cmd
	}
	return exec.Command("ssh", sshArgs...)
}

// TestConnection performs a quick SSH handshake to sish and returns nil on
// success. This validates the key, network path, and sish authorization.
func TestConnection(sshHost string, sshPort int, keyPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ssh",
		"-o", "StrictHostKeyChecking=no",
		"-o", "ConnectTimeout=10",
		"-i", keyPath,
		"-p", strconv.Itoa(sshPort),
		"tunnel@"+sshHost,
		"exit",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ssh test: %w\n%s", err, out)
	}
	return nil
}
