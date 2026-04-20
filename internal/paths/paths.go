// Package paths centralizes all filesystem locations stackctl reads or writes.
//
// The two roots STACKCTL_DIR and LEARNINGSTACK_DIR can be overridden via
// environment variables for development on a devbox (see ARCHITECTURE.md §7).
// Defaults point at the production layout under /opt.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

// Default roots on a production install (learningstack user owns both).
const (
	DefaultStackctlDir      = "/opt/stackctl"
	DefaultLearningstackDir = "/opt/learningstack"
)

// Environment variables that override the defaults.
const (
	EnvStackctlDir      = "STACKCTL_DIR"
	EnvLearningstackDir = "LEARNINGSTACK_DIR"
)

// StackctlDir returns the root directory that holds the stackctl binary,
// config and compose files. Honours $STACKCTL_DIR for dev overrides.
func StackctlDir() string {
	if v := os.Getenv(EnvStackctlDir); v != "" {
		return v
	}
	return DefaultStackctlDir
}

// LearningstackDir returns the root for container data volumes.
// Honours $LEARNINGSTACK_DIR for dev overrides.
func LearningstackDir() string {
	if v := os.Getenv(EnvLearningstackDir); v != "" {
		return v
	}
	return DefaultLearningstackDir
}

// -- config tree ------------------------------------------------------------

func ConfigDir() string    { return filepath.Join(StackctlDir(), "config") }
func ConfigFile() string   { return filepath.Join(ConfigDir(), "config.yaml") }
func StateFile() string    { return filepath.Join(ConfigDir(), "state.yaml") }
// DexConfigFile is the host path stackctl writes the Dex config to. It
// lives under the dex container's data dir so the same directory that is
// bind-mounted into /etc/dex contains the file. The container reads it as
// /etc/dex/config.yaml — keep that name in sync with dex.yaml's command:.
func DexConfigFile() string {
	return filepath.Join(LearningstackDir(), "dex", "config", "config.yaml")
}
func TunnelKeyFile() string { return filepath.Join(ConfigDir(), "tunnel_key") }
func TunnelPubKeyFile() string {
	return filepath.Join(ConfigDir(), "tunnel_key.pub")
}

// CatalogCacheDir is where the catalog index and per-app definitions are
// cached after a sync.
func CatalogCacheDir() string { return filepath.Join(ConfigDir(), "catalog") }
func CatalogIndexFile() string {
	return filepath.Join(CatalogCacheDir(), "catalog.yaml")
}
func CatalogContainersDir() string {
	return filepath.Join(CatalogCacheDir(), "containers")
}

// AppDefinitionFile returns the cached app definition path for an app ID.
func AppDefinitionFile(appID string) string {
	return filepath.Join(CatalogContainersDir(), appID+".yaml")
}

// RegistrationPackageFile is the age-encrypted registration package path.
func RegistrationPackageFile(slug string) string {
	return filepath.Join(ConfigDir(), fmt.Sprintf("registration-%s.age", slug))
}

// -- compose tree -----------------------------------------------------------

func ComposeDir() string  { return filepath.Join(StackctlDir(), "compose") }
func ComposeFile() string { return filepath.Join(ComposeDir(), "docker-compose.yml") }
func EnvFile() string     { return filepath.Join(ComposeDir(), ".env") }

// VersionFile holds the currently installed version as plain text.
func VersionFile() string { return filepath.Join(StackctlDir(), "stackctl.version") }

// -- data tree --------------------------------------------------------------

// AppDataDir returns the host-side data directory for an app: the one that
// container volumes bind-mount into.
func AppDataDir(appID string) string {
	return filepath.Join(LearningstackDir(), appID)
}

// -- helpers ----------------------------------------------------------------

// EnsureDir creates dir (and parents) with the given permissions if missing.
// Existing directories are left untouched.
func EnsureDir(dir string, perm os.FileMode) error {
	if err := os.MkdirAll(dir, perm); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	return nil
}

// AtomicWrite writes data to path via a temp file in the same directory and
// renames it into place, so readers never see a half-written file. The final
// file mode is set to perm after rename (umask-safe).
func AtomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("ensure parent %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if anything below fails.
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		cleanup()
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("rename %s -> %s: %w", tmpName, path, err)
	}
	return nil
}
