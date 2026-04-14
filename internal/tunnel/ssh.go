package tunnel

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
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
//
// sish only accepts port-forwarding sessions, not exec/shell, so we cannot
// run a remote command. A successful ssh.Dial (which completes the auth
// handshake) is sufficient proof that key + network + authorization work.
func TestConnection(sshHost string, sshPort int, keyPath string) error {
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("read key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(raw)
	if err != nil {
		return fmt.Errorf("parse key: %w", err)
	}

	cfg := &ssh.ClientConfig{
		User:            "tunnel",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	addr := net.JoinHostPort(sshHost, strconv.Itoa(sshPort))
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	_ = client.Close()
	return nil
}
