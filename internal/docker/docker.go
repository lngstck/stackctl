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
