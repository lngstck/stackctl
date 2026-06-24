// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package backup

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"
	"gopkg.in/yaml.v3"

	"github.com/lngstck/stackctl/internal/docker"
	"github.com/lngstck/stackctl/internal/paths"
	"github.com/lngstck/stackctl/internal/postgres"
	"github.com/lngstck/stackctl/internal/update"
)

// pgReadyTimeout bounds how long restore waits for postgres to accept
// connections after it is (re)started for the dump replay.
const pgReadyTimeout = 90 * time.Second

// Restore replays a backup archive over the live system. It is destructive:
// app data, config, compose and the database are overwritten with the backup's
// contents. The caller MUST have already taken the op-lock, confirmed intent
// with the admin, and — for an encrypted archive — collected the passphrase.
//
// Restore does NOT restart stackctl itself; on success the web worker calls
// update.RestartService() so the process reloads config.yaml/state.yaml/.env
// fresh from disk. That sidesteps any in-memory state reload (and the tunnel
// manager's shared state pointer). stackctl runs on the host, not as a
// container, so stopping the stack does not kill it.
//
// File placement runs through ephemeral root containers (ownership-preserving
// tar extract): app data dirs are owned by container UIDs the non-root
// learningstack user cannot recreate. The extract is an overlay — it restores
// every file from the backup but does not delete files that exist now yet were
// absent at backup time. The postgres data directory is not in the archive and
// is left in place; its contents are replaced logically by the dump replay.
func Restore(file, passphrase string, rep Reporter) error {
	if rep == nil {
		rep = nopReporter{}
	}
	if !ValidName(file) {
		return fmt.Errorf("ungültiger Backup-Name")
	}
	archivePath := filepath.Join(paths.BackupsDir(), file)
	if _, err := os.Stat(archivePath); err != nil {
		return fmt.Errorf("Backup nicht gefunden")
	}
	encrypted := strings.HasSuffix(file, ".age")
	if encrypted && passphrase == "" {
		return fmt.Errorf("dieses Backup ist verschlüsselt — bitte Passphrase angeben")
	}

	// Staging holds the plaintext tar.gz (if we decrypt) and the extracted DB
	// dump. Both are single learningstack-owned files, so cleanup is trivial.
	staging, err := os.MkdirTemp(paths.BackupsDir(), ".restore-")
	if err != nil {
		return fmt.Errorf("Arbeitsverzeichnis anlegen: %w", err)
	}
	defer os.RemoveAll(staging)

	// 1. Verify the archive: decrypt if needed, read & check the manifest, and
	//    extract the SQL dump to a host file for the replay.
	rep.Step("Sicherung prüfen")
	tarGz := archivePath
	if encrypted {
		tarGz = filepath.Join(staging, "archive.tar.gz")
		if err := decryptToFile(archivePath, tarGz, passphrase); err != nil {
			return fmt.Errorf("entschlüsseln fehlgeschlagen (falsche Passphrase?): %w", err)
		}
	}

	manifest, dumpExtracted, err := inspectAndExtract(tarGz, filepath.Join(staging, "postgres-all.sql"))
	if err != nil {
		return fmt.Errorf("Sicherung lesen: %w", err)
	}
	if manifest.Schema != SchemaVersion {
		return fmt.Errorf("inkompatibles Backup-Format (Schema %d, dieses stackctl erwartet %d)", manifest.Schema, SchemaVersion)
	}
	rep.Log(fmt.Sprintf("Backup vom %s, stackctl %s, Schule %q, %d App(s).",
		manifest.CreatedAt, manifest.StackctlVersion, manifest.SchoolSlug, len(manifest.Apps)))
	if manifest.StackctlVersion != "" && manifest.StackctlVersion != update.CurrentVersion() {
		rep.Log(fmt.Sprintf("Hinweis: Backup wurde mit stackctl %s erstellt, läuft jetzt unter %s.",
			manifest.StackctlVersion, update.CurrentVersion()))
	}

	// 2. Quiesce the stack so nothing writes while we overlay files. The shared
	//    network is external, so stop/up does not tear it down.
	rep.Step("Dienste stoppen")
	if code, out := docker.ComposeStop(paths.ComposeFile()); code != 0 {
		rep.Log("compose stop: " + strings.TrimSpace(out))
	}

	// 3. Overlay config, compose and app data from the archive (root container,
	//    preserving uid/gid). postgres/ is not in the archive → left untouched.
	rep.Step("Dateien wiederherstellen")
	type tree struct{ member, dest string }
	for _, t := range []tree{
		{"sc-config", paths.ConfigDir()},
		{"sc-compose", paths.ComposeDir()},
		{"ls", paths.LearningstackDir()},
	} {
		if err := paths.EnsureDir(t.dest, 0o755); err != nil {
			return fmt.Errorf("Zielverzeichnis %s: %w", t.dest, err)
		}
		mounts := []string{
			tarGz + ":/in.tar.gz:ro",
			t.dest + ":/dst",
		}
		if out, err := docker.RunLong(mounts, docker.HelperImage,
			"tar", "xzf", "/in.tar.gz", "-C", "/dst", "--strip-components=1", t.member); err != nil {
			return fmt.Errorf("%s wiederherstellen: %w (%s)", t.member, err, strings.TrimSpace(out))
		}
		rep.Log("wiederhergestellt: " + t.dest)
	}

	// 4. Database: bring postgres up against the restored .env/compose, wait for
	//    readiness, then replay the dump (--clean --if-exists makes it idempotent).
	if dumpExtracted {
		rep.Step("Datenbank wiederherstellen")
		if code, out := docker.ComposeUp(paths.ComposeFile(), docker.ContainerName("postgres")); code != 0 {
			return fmt.Errorf("postgres starten: %s", strings.TrimSpace(out))
		}
		if err := waitForPostgres(pgReadyTimeout); err != nil {
			return err
		}
		out, err := docker.ExecFromFile(postgres.ContainerName,
			[]string{"psql", "-U", "postgres", "-v", "ON_ERROR_STOP=0"},
			filepath.Join(staging, "postgres-all.sql"))
		if err != nil {
			return fmt.Errorf("Datenbank-Replay: %w (%s)", err, lastLines(out, 5))
		}
		rep.Log("Datenbank-Abzug eingespielt.")
	} else {
		rep.Log("Backup enthält keinen Datenbank-Abzug — Datenbank unverändert.")
	}

	// 5. Start the full restored set; remove containers no longer in the compose.
	rep.Step("Dienste starten")
	if code, out := docker.ComposeUpRemoveOrphans(paths.ComposeFile()); code != 0 {
		return fmt.Errorf("Dienste starten: %s", strings.TrimSpace(out))
	}

	return nil
}

// -- internals --------------------------------------------------------------

// inspectAndExtract reads the manifest and extracts meta/postgres-all.sql from a
// tar.gz in a single pass (Go tar reader — no root needed to read the stream).
// Returns the manifest and whether a non-empty DB dump was present.
func inspectAndExtract(tarGzPath, sqlDest string) (Info, bool, error) {
	f, err := os.Open(tarGzPath)
	if err != nil {
		return Info{}, false, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return Info{}, false, err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var manifest Info
	gotManifest := false
	gotDump := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Info{}, false, err
		}
		switch hdr.Name {
		case "meta/manifest.yaml":
			data, err := io.ReadAll(tr)
			if err != nil {
				return Info{}, false, err
			}
			if err := yaml.Unmarshal(data, &manifest); err != nil {
				return Info{}, false, fmt.Errorf("manifest parsen: %w", err)
			}
			gotManifest = true
		case "meta/postgres-all.sql":
			out, err := os.OpenFile(sqlDest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				return Info{}, false, err
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return Info{}, false, err
			}
			if err := out.Close(); err != nil {
				return Info{}, false, err
			}
			if hdr.Size > 0 {
				gotDump = true
			}
		}
	}
	if !gotManifest {
		return Info{}, false, fmt.Errorf("kein Manifest im Archiv — keine gültige stackctl-Sicherung")
	}
	return manifest, gotDump, nil
}

// readManifest reads only meta/manifest.yaml from a plaintext tar.gz, stopping
// as soon as it is found (used to build a sidecar for an uploaded backup).
func readManifest(tarGzPath string) (Info, error) {
	f, err := os.Open(tarGzPath)
	if err != nil {
		return Info{}, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return Info{}, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Info{}, err
		}
		if hdr.Name == "meta/manifest.yaml" {
			data, err := io.ReadAll(tr)
			if err != nil {
				return Info{}, err
			}
			var info Info
			if err := yaml.Unmarshal(data, &info); err != nil {
				return Info{}, err
			}
			return info, nil
		}
	}
	return Info{}, fmt.Errorf("kein Manifest im Archiv")
}

// decryptToFile age-decrypts src into dst using a scrypt (passphrase) identity.
func decryptToFile(src, dst, pass string) error {
	id, err := age.NewScryptIdentity(pass)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	r, err := age.Decrypt(in, id)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, r); err != nil {
		return err
	}
	return out.Sync()
}

// waitForPostgres polls until the postgres container accepts connections or the
// timeout elapses.
func waitForPostgres(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if postgres.IsReachable() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("postgres wurde nicht rechtzeitig bereit (%s)", timeout)
		}
		time.Sleep(2 * time.Second)
	}
}

// lastLines returns the final n non-empty lines of s, for compact error display.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
