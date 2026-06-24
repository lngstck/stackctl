// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

// Package backup creates and lists full stackctl backups: a logical Postgres
// dump (pg_dumpall) plus the config, compose and app-data file trees, bundled
// into one .tar.gz (optionally age-encrypted with a passphrase).
//
// Why a throwaway root container for the file tree: stackctl runs as the
// non-root learningstack user, but app data directories are owned by various
// container UIDs (postgres 999, dex 1001, …) and may be mode 0600 — a native
// Go tar would hit permission-denied. An ephemeral alpine container reads
// everything as root, mirroring the established docker.ChownHostPath pattern.
//
// Each archive is accompanied by an unencrypted <file>.meta.json sidecar so the
// web UI can list backups (date, size, apps, encrypted?) without opening — let
// alone decrypting — the archives. The sidecar carries no secrets.
package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"filippo.io/age"
	"gopkg.in/yaml.v3"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/docker"
	"github.com/lngstck/stackctl/internal/paths"
	"github.com/lngstck/stackctl/internal/postgres"
	"github.com/lngstck/stackctl/internal/update"
)

// SchemaVersion is the backup format version, written into every manifest and
// checked on restore. Bump it on incompatible format changes.
const SchemaVersion = 1

// Reporter receives progress updates so the web UI can render a live job view.
// The web *Job implements it structurally (Step/Log), as does install.Reporter.
type Reporter interface {
	Step(name string)
	Log(line string)
}

// nopReporter discards progress (used by tests / CLI paths).
type nopReporter struct{}

func (nopReporter) Step(string) {}
func (nopReporter) Log(string)  {}

// Options configures a backup run.
type Options struct {
	// Passphrase, if non-empty, age-encrypts the archive (scrypt recipient).
	Passphrase string
}

// AppEntry records one installed app in the manifest.
type AppEntry struct {
	ID      string `json:"id" yaml:"id"`
	Name    string `json:"name" yaml:"name"`
	Version string `json:"version" yaml:"version"`
}

// Info is both the in-archive manifest (meta/manifest.yaml) and the on-disk
// sidecar (<file>.meta.json). The File/SizeBytes/SHA256 fields describe the
// archive file itself and so only appear in the JSON sidecar (yaml:"-").
type Info struct {
	Schema          int        `json:"schema" yaml:"schema"`
	File            string     `json:"file" yaml:"-"`
	CreatedAt       string     `json:"created_at" yaml:"created_at"`
	StackctlVersion string     `json:"stackctl_version" yaml:"stackctl_version"`
	SchoolSlug      string     `json:"school_slug" yaml:"school_slug"`
	SchoolName      string     `json:"school_name" yaml:"school_name"`
	ConfigVersion   int        `json:"config_version" yaml:"config_version"`
	StateVersion    string     `json:"state_version" yaml:"state_version"`
	Encrypted       bool       `json:"encrypted" yaml:"encrypted"`
	IncludesDB      bool       `json:"includes_db" yaml:"includes_db"`
	Apps            []AppEntry `json:"apps" yaml:"apps"`
	SizeBytes       int64      `json:"size_bytes" yaml:"-"`
	SHA256          string     `json:"sha256" yaml:"-"`
}

// archiveNameRe validates a backup archive filename. Anchored, so no path
// separator or ".." can slip through — used for download/delete/restore.
var archiveNameRe = regexp.MustCompile(`^backup-[a-z0-9-]+-\d{8}-\d{6}\.tar\.gz(\.age)?$`)

// ValidName reports whether file is a well-formed backup archive name.
func ValidName(file string) bool { return archiveNameRe.MatchString(file) }

// Create runs a full backup and returns the resulting Info. cfg/state are read
// for the manifest only; nothing in them is mutated.
func Create(cfg *config.Config, state *config.State, opts Options, rep Reporter) (Info, error) {
	if rep == nil {
		rep = nopReporter{}
	}
	if err := paths.EnsureDir(paths.BackupsDir(), 0o700); err != nil {
		return Info{}, fmt.Errorf("create backups dir: %w", err)
	}

	now := time.Now()
	name := fmt.Sprintf("backup-%s-%s", slugOrDefault(cfg), now.Format("20060102-150405"))
	encrypted := opts.Passphrase != ""

	// Staging holds the metadata (pg dump + manifest) that gets tarred together
	// with the host trees. It lives inside BackupsDir (learningstack-owned) so
	// cleanup never depends on file ownership.
	staging, err := os.MkdirTemp(paths.BackupsDir(), ".staging-"+name+"-")
	if err != nil {
		return Info{}, fmt.Errorf("create staging dir: %w", err)
	}
	defer os.RemoveAll(staging)
	metaDir := filepath.Join(staging, "meta")
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		return Info{}, fmt.Errorf("create meta dir: %w", err)
	}

	// 1. Logical database dump.
	rep.Step("Datenbank sichern")
	includesDB := false
	if postgres.IsReachable() {
		dumpPath := filepath.Join(metaDir, "postgres-all.sql")
		if err := docker.ExecToFile(postgres.ContainerName,
			[]string{"pg_dumpall", "-U", "postgres", "--clean", "--if-exists"},
			dumpPath); err != nil {
			return Info{}, fmt.Errorf("pg_dumpall: %w", err)
		}
		includesDB = true
		rep.Log("Datenbank-Abzug erstellt (pg_dumpall).")
	} else {
		rep.Log("Postgres nicht erreichbar — Datenbank wird nicht gesichert.")
	}

	// 2. Manifest.
	info := Info{
		Schema:          SchemaVersion,
		CreatedAt:       now.UTC().Format(time.RFC3339),
		StackctlVersion: update.CurrentVersion(),
		SchoolSlug:      cfg.School.Slug,
		SchoolName:      cfg.School.Name,
		ConfigVersion:   cfg.Version,
		StateVersion:    state.Version,
		Encrypted:       encrypted,
		IncludesDB:      includesDB,
		Apps:            appEntries(state),
	}
	manifestYAML, err := yaml.Marshal(info)
	if err != nil {
		return Info{}, fmt.Errorf("marshal manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(metaDir, "manifest.yaml"), manifestYAML, 0o644); err != nil {
		return Info{}, fmt.Errorf("write manifest: %w", err)
	}

	// 3. Pack config, compose, app data and meta into one archive via a root
	//    container. The postgres data directory is excluded — the dump above
	//    supersedes it (and a raw copy of a live data dir is inconsistent).
	rep.Step("Konfiguration & Dateien einpacken")
	partial := name + ".tar.gz.partial"
	mounts := []string{
		paths.LearningstackDir() + ":/m/ls:ro",
		paths.ConfigDir() + ":/m/sc-config:ro",
		paths.ComposeDir() + ":/m/sc-compose:ro",
		metaDir + ":/m/meta:ro",
		paths.BackupsDir() + ":/out",
	}
	if out, err := docker.RunLong(mounts, docker.HelperImage,
		"tar", "czf", "/out/"+partial, "--exclude=ls/postgres",
		"-C", "/m", "ls", "sc-config", "sc-compose", "meta"); err != nil {
		_ = os.Remove(filepath.Join(paths.BackupsDir(), partial))
		return Info{}, fmt.Errorf("pack archive: %w (%s)", err, strings.TrimSpace(out))
	}
	partialPath := filepath.Join(paths.BackupsDir(), partial)

	// 4. Finalize: encrypt (passphrase) or rename into place.
	var finalPath string
	if encrypted {
		rep.Step("Verschlüsseln")
		finalPath = filepath.Join(paths.BackupsDir(), name+".tar.gz.age")
		if err := encryptFile(partialPath, finalPath, opts.Passphrase); err != nil {
			_ = os.Remove(partialPath)
			_ = os.Remove(finalPath)
			return Info{}, fmt.Errorf("encrypt: %w", err)
		}
		_ = os.Remove(partialPath)
	} else {
		rep.Step("Abschließen")
		finalPath = filepath.Join(paths.BackupsDir(), name+".tar.gz")
		// Rename succeeds despite the root-owned partial because BackupsDir is
		// learningstack-owned (unlink/rename is a directory-permission op).
		if err := os.Rename(partialPath, finalPath); err != nil {
			return Info{}, fmt.Errorf("finalize archive: %w", err)
		}
	}

	// 5. Sidecar (size + checksum + manifest summary, for fast listing).
	fi, err := os.Stat(finalPath)
	if err != nil {
		return Info{}, fmt.Errorf("stat archive: %w", err)
	}
	sum, err := sha256File(finalPath)
	if err != nil {
		return Info{}, fmt.Errorf("checksum: %w", err)
	}
	info.File = filepath.Base(finalPath)
	info.SizeBytes = fi.Size()
	info.SHA256 = sum
	if err := writeSidecar(info); err != nil {
		return Info{}, fmt.Errorf("write sidecar: %w", err)
	}

	rep.Log(fmt.Sprintf("Backup erstellt: %s (%s).", info.File, HumanSize(info.SizeBytes)))
	return info, nil
}

// List returns all valid backups (those with a readable sidecar and an existing
// archive), newest first.
func List() ([]Info, error) {
	entries, err := os.ReadDir(paths.BackupsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Info
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".meta.json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(paths.BackupsDir(), e.Name()))
		if err != nil {
			continue
		}
		var info Info
		if err := json.Unmarshal(data, &info); err != nil {
			continue
		}
		if !ValidName(info.File) {
			continue
		}
		if _, err := os.Stat(filepath.Join(paths.BackupsDir(), info.File)); err != nil {
			continue // orphaned sidecar
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

// Path returns the validated absolute path to a backup archive for download.
func Path(file string) (string, error) {
	if !ValidName(file) {
		return "", fmt.Errorf("invalid backup name")
	}
	p := filepath.Join(paths.BackupsDir(), file)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("backup not found")
	}
	return p, nil
}

// Delete removes a backup archive and its sidecar. Idempotent.
func Delete(file string) error {
	if !ValidName(file) {
		return fmt.Errorf("invalid backup name")
	}
	archive := filepath.Join(paths.BackupsDir(), file)
	if err := os.Remove(archive); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(archive + ".meta.json"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// -- internals --------------------------------------------------------------

func writeSidecar(info Info) error {
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(paths.BackupsDir(), info.File+".meta.json"), data, 0o600)
}

func appEntries(state *config.State) []AppEntry {
	var apps []AppEntry
	for _, id := range state.InstalledIDs() {
		cs := state.Containers[id]
		apps = append(apps, AppEntry{ID: cs.ID, Name: cs.Name, Version: cs.VersionInstalled})
	}
	return apps
}

func slugOrDefault(cfg *config.Config) string {
	if cfg.School.Slug != "" {
		return cfg.School.Slug
	}
	return "stackctl"
}

// encryptFile age-encrypts src into dst using a scrypt (passphrase) recipient.
func encryptFile(src, dst, pass string) error {
	rcp, err := age.NewScryptRecipient(pass)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	w, err := age.Encrypt(out, rcp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, in); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil { // flush the age stream
		return err
	}
	return out.Sync()
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// HumanSize formats a byte count as a short human-readable string.
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
