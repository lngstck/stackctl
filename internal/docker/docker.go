// Package docker is a thin wrapper around the docker and docker-compose CLIs.
//
// Every invocation goes through exec.Command with an args slice — never
// through sh -c — to prevent command injection (ARCHITECTURE.md §16).
// Functions return combined stdout+stderr and the exit code so callers can
// surface errors in the web UI without parsing them.
package docker

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// NetworkName is the shared Docker network for all learningstack containers.
const NetworkName = "learningstack"

// ContainerPrefix is prepended to the app ID to form the container name.
const ContainerPrefix = "ls-"

// ContainerName returns the canonical Docker container name for an app.
func ContainerName(appID string) string {
	return ContainerPrefix + appID
}

// defaultTimeout is applied to every docker/compose command unless the
// caller provides a context with its own deadline.
const defaultTimeout = 5 * time.Minute

// EnsureNetwork creates the shared Docker bridge network if it doesn't
// already exist. Idempotent.
func EnsureNetwork() error {
	_, _, err := run(nil, "docker", "network", "inspect", NetworkName)
	if err == nil {
		return nil // already exists
	}
	_, output, err := run(nil, "docker", "network", "create", NetworkName)
	if err != nil {
		// Race: another process created it between inspect and create.
		if strings.Contains(output, "already exists") {
			return nil
		}
		return fmt.Errorf("create network %s: %s", NetworkName, output)
	}
	return nil
}

// ComposeUp runs `docker compose up -d` for the given services (or all if
// empty). composeFile is the path to docker-compose.yml.
func ComposeUp(composeFile string, services ...string) (int, string) {
	args := composeArgs(composeFile, "up", "-d")
	args = append(args, services...)
	code, out, _ := run(nil, "docker", args...)
	return code, out
}

// ComposeDown stops and removes the given services (or all if empty).
func ComposeDown(composeFile string, services ...string) (int, string) {
	args := composeArgs(composeFile, "down")
	args = append(args, services...)
	code, out, _ := run(nil, "docker", args...)
	return code, out
}

// ComposeRm stops AND removes the given services (does not touch networks
// or volumes). Use this when a service was removed from docker-compose.yml
// — plain stop leaves the container lingering in `docker ps -a` and blocks
// the container name on reinstall.
func ComposeRm(composeFile string, services ...string) (int, string) {
	args := composeArgs(composeFile, "rm", "-f", "-s")
	args = append(args, services...)
	code, out, _ := run(nil, "docker", args...)
	return code, out
}

// ComposeStop stops the given services without removing them.
func ComposeStop(composeFile string, services ...string) (int, string) {
	args := composeArgs(composeFile, "stop")
	args = append(args, services...)
	code, out, _ := run(nil, "docker", args...)
	return code, out
}

// ComposeStart starts existing containers for the given services.
func ComposeStart(composeFile string, services ...string) (int, string) {
	args := composeArgs(composeFile, "start")
	args = append(args, services...)
	code, out, _ := run(nil, "docker", args...)
	return code, out
}

// ComposePS runs `docker compose ps --format json` and returns the raw
// JSON output. Parsing is left to the caller because the JSON schema
// varies between docker compose versions.
func ComposePS(composeFile string) (string, error) {
	_, out, err := run(nil, "docker", composeArgs(composeFile, "ps", "--format", "json")...)
	if err != nil {
		return "", fmt.Errorf("compose ps: %s", out)
	}
	return out, nil
}

// RestartContainer restarts a single container by name.
func RestartContainer(name string) error {
	_, out, err := run(nil, "docker", "restart", name)
	if err != nil {
		return fmt.Errorf("restart %s: %s", name, out)
	}
	return nil
}

// SendSignal sends a Unix signal to a container's main process via
// `docker kill --signal=<sig>`. signal must be a bare name like "HUP" or
// "TERM" (no leading "SIG"). Used for graceful config-reload of daemons
// that watch for SIGHUP (llmd, nginx, ...).
func SendSignal(name, signal string) error {
	if name == "" {
		return fmt.Errorf("kill: empty container name")
	}
	if !validSignal(signal) {
		return fmt.Errorf("kill: invalid signal %q", signal)
	}
	_, out, err := run(nil, "docker", "kill", "--signal="+signal, name)
	if err != nil {
		return fmt.Errorf("kill -s %s %s: %s", signal, name, strings.TrimSpace(out))
	}
	return nil
}

func validSignal(s string) bool {
	if s == "" || len(s) > 10 {
		return false
	}
	for _, c := range s {
		if !(c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

// IsRunning checks whether the named container is in "running" state.
func IsRunning(name string) bool {
	_, out, err := run(nil, "docker", "inspect", "-f", "{{.State.Running}}", name)
	return err == nil && strings.TrimSpace(out) == "true"
}

// Exec runs a command inside a running container. Returns exit code,
// combined output, and error.
func Exec(container string, cmd []string) (int, string, error) {
	args := []string{"exec", container}
	args = append(args, cmd...)
	return run(nil, "docker", args...)
}

// Pull pulls an image. Returns combined output.
func Pull(image string) (string, error) {
	_, out, err := run(nil, "docker", "pull", image)
	if err != nil {
		return out, fmt.Errorf("pull %s: %s", image, out)
	}
	return out, nil
}

// ComposePull runs `docker compose pull` for the given services (or all if
// empty). Used by the manual-update flow so that a subsequent `up -d`
// recreates the container against the freshly fetched image.
func ComposePull(composeFile string, services ...string) (int, string) {
	args := composeArgs(composeFile, "pull")
	args = append(args, services...)
	code, out, _ := run(nil, "docker", args...)
	return code, out
}

// RemoveImage deletes a local image. Errors are surfaced but the function is
// tolerant of "no such image" and "image in use" situations so callers can
// invoke it during uninstall without aborting the whole flow.
func RemoveImage(image string) error {
	if image == "" {
		return fmt.Errorf("rmi: empty image")
	}
	_, out, err := run(nil, "docker", "rmi", image)
	if err != nil {
		// Image already gone — treat as success.
		if strings.Contains(out, "No such image") || strings.Contains(out, "reference does not exist") {
			return nil
		}
		return fmt.Errorf("rmi %s: %s", image, strings.TrimSpace(out))
	}
	return nil
}

// HelperImage ist das Wegwerf-Image fuer kleine Host-Filesystem-Ops
// (chown, rm -rf) die stackctl als Non-Root-User nicht selbst machen kann,
// der Docker-Daemon aber als Root schon. Pin auf Major-Version, damit
// zukuenftige Alpine-Majors nicht unerwartet eingreifen.
const HelperImage = "alpine:3"

// ChownHostPath setzt rekursiv Owner:Group auf einem Host-Verzeichnis,
// indem es einen ephemeren alpine-Container mit dem Pfad gemountet
// startet und dort `chown -R` ausfuehrt. owner im Format "uid:gid"
// (z.B. "999:999"). Notwendig fuer Container, die als Non-Root laufen
// (postgres: 999:999, dex: 1001:1001, grafana: 472:472 usw.).
func ChownHostPath(hostPath, owner string) error {
	if hostPath == "" || owner == "" {
		return fmt.Errorf("chown: hostPath and owner are required")
	}
	if !strings.HasPrefix(hostPath, "/") {
		return fmt.Errorf("chown: hostPath must be absolute, got %q", hostPath)
	}
	// owner Whitelist: nur Ziffern + ein Doppelpunkt, keine Shell-Tricks.
	if !validOwner(owner) {
		return fmt.Errorf("chown: invalid owner %q (expected uid:gid)", owner)
	}
	mount := hostPath + ":/target"
	_, out, err := run(nil, "docker", "run", "--rm",
		"-v", mount, HelperImage,
		"chown", "-R", owner, "/target")
	if err != nil {
		return fmt.Errorf("chown %s -> %s: %s", hostPath, owner, out)
	}
	return nil
}

// RemoveHostPath loescht ein Host-Verzeichnis rekursiv via Throwaway-
// Container. Vorgesehen fuer Deinstallations-Flows mit "Daten loeschen"-
// Option. Aus Sicherheitsgruenden nur unter /opt/learningstack/ erlaubt.
func RemoveHostPath(hostPath string) error {
	if !strings.HasPrefix(hostPath, "/opt/learningstack/") {
		return fmt.Errorf("rm: refuse to remove path outside /opt/learningstack/: %q", hostPath)
	}
	// Parent mounten + nur den Leaf-Namen loeschen, damit der Container
	// nur die spezifische App-Daten weghaut, nicht irgendwas anderes.
	parent := "/opt/learningstack"
	leaf := strings.TrimPrefix(hostPath, parent+"/")
	if leaf == "" || strings.Contains(leaf, "/") || strings.Contains(leaf, "..") {
		return fmt.Errorf("rm: invalid app-data path %q", hostPath)
	}
	mount := parent + ":/target"
	_, out, err := run(nil, "docker", "run", "--rm",
		"-v", mount, HelperImage,
		"rm", "-rf", "/target/"+leaf)
	if err != nil {
		return fmt.Errorf("rm %s: %s", hostPath, out)
	}
	return nil
}

// validOwner checkt "uid:gid" — nur Ziffern + ein einziger Doppelpunkt.
func validOwner(s string) bool {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	for _, p := range parts {
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}

// -- internals --------------------------------------------------------------

// composeArgs builds the argument slice for docker compose sub-commands.
func composeArgs(composeFile, subCmd string, extra ...string) []string {
	args := []string{"compose", "-f", composeFile, subCmd}
	args = append(args, extra...)
	return args
}

// run executes a binary with args and returns exit code + combined output.
// A nil context gets the default timeout.
func run(ctx context.Context, name string, args ...string) (int, string, error) {
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), defaultTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			code = -1
		}
	}
	return code, out, err
}
