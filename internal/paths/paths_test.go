package paths

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStackctlDirDefault(t *testing.T) {
	t.Setenv(EnvStackctlDir, "")
	if got := StackctlDir(); got != DefaultStackctlDir {
		t.Errorf("StackctlDir() = %q, want %q", got, DefaultStackctlDir)
	}
}

func TestStackctlDirOverride(t *testing.T) {
	t.Setenv(EnvStackctlDir, "/tmp/fake-stackctl")
	if got := StackctlDir(); got != "/tmp/fake-stackctl" {
		t.Errorf("StackctlDir() = %q, want /tmp/fake-stackctl", got)
	}
}

func TestConfigPathsRespectOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvStackctlDir, dir)

	cases := map[string]string{
		"ConfigDir":            filepath.Join(dir, "config"),
		"ConfigFile":           filepath.Join(dir, "config", "config.yaml"),
		"StateFile":            filepath.Join(dir, "config", "state.yaml"),
		"DexConfigFile":        filepath.Join(dir, "config", "dex-config.yaml"),
		"TunnelKeyFile":        filepath.Join(dir, "config", "tunnel_key"),
		"TunnelPubKeyFile":     filepath.Join(dir, "config", "tunnel_key.pub"),
		"CatalogCacheDir":      filepath.Join(dir, "config", "catalog"),
		"CatalogIndexFile":     filepath.Join(dir, "config", "catalog", "catalog.yaml"),
		"CatalogContainersDir": filepath.Join(dir, "config", "catalog", "containers"),
		"ComposeDir":           filepath.Join(dir, "compose"),
		"ComposeFile":          filepath.Join(dir, "compose", "docker-compose.yml"),
		"EnvFile":              filepath.Join(dir, "compose", ".env"),
		"VersionFile":          filepath.Join(dir, "stackctl.version"),
	}
	for name, want := range cases {
		var got string
		switch name {
		case "ConfigDir":
			got = ConfigDir()
		case "ConfigFile":
			got = ConfigFile()
		case "StateFile":
			got = StateFile()
		case "DexConfigFile":
			got = DexConfigFile()
		case "TunnelKeyFile":
			got = TunnelKeyFile()
		case "TunnelPubKeyFile":
			got = TunnelPubKeyFile()
		case "CatalogCacheDir":
			got = CatalogCacheDir()
		case "CatalogIndexFile":
			got = CatalogIndexFile()
		case "CatalogContainersDir":
			got = CatalogContainersDir()
		case "ComposeDir":
			got = ComposeDir()
		case "ComposeFile":
			got = ComposeFile()
		case "EnvFile":
			got = EnvFile()
		case "VersionFile":
			got = VersionFile()
		}
		if got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestAppDataDirRespectsLearningstackDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvLearningstackDir, dir)
	want := filepath.Join(dir, "langflow")
	if got := AppDataDir("langflow"); got != want {
		t.Errorf("AppDataDir = %q, want %q", got, want)
	}
}

func TestRegistrationPackageFile(t *testing.T) {
	t.Setenv(EnvStackctlDir, "/opt/stackctl")
	want := "/opt/stackctl/config/registration-phoenix.age"
	if got := RegistrationPackageFile("phoenix"); got != want {
		t.Errorf("RegistrationPackageFile = %q, want %q", got, want)
	}
}

func TestAtomicWriteCreatesFileWithPerm(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sub", "file.yaml")
	data := []byte("hello: world\n")

	if err := AtomicWrite(target, data, 0o640); err != nil {
		t.Fatalf("AtomicWrite: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("content mismatch: got %q, want %q", got, data)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o640 {
		t.Errorf("perm = %o, want 0640", perm)
	}
}

func TestAtomicWriteOverwrites(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "file.txt")
	if err := AtomicWrite(target, []byte("first"), 0o644); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := AtomicWrite(target, []byte("second"), 0o644); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "second" {
		t.Errorf("overwrite failed: got %q", got)
	}
	// Ensure no leftover temp files in the directory.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}
