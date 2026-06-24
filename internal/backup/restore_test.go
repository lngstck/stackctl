// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package backup

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// writeTestArchive builds a minimal tar.gz with a manifest and (optionally) a
// SQL dump member, mirroring what Create produces.
func writeTestArchive(t *testing.T, path string, manifest Info, sql string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	add := func(name string, data []byte) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	y, _ := yaml.Marshal(manifest)
	add("meta/manifest.yaml", y)
	if sql != "" {
		add("meta/postgres-all.sql", []byte(sql))
	}
}

func TestInspectAndExtract(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.tar.gz")
	want := Info{Schema: SchemaVersion, SchoolSlug: "musterschule", StackctlVersion: "v0.7.0",
		Apps: []AppEntry{{ID: "open-webui", Name: "OpenWebUI", Version: "1.0"}}}
	writeTestArchive(t, archive, want, "-- dump\nSELECT 1;\n")

	sqlDest := filepath.Join(dir, "out.sql")
	got, hasDB, err := inspectAndExtract(archive, sqlDest)
	if err != nil {
		t.Fatalf("inspectAndExtract: %v", err)
	}
	if !hasDB {
		t.Error("hasDB = false, want true")
	}
	if got.SchoolSlug != want.SchoolSlug || got.Schema != want.Schema || len(got.Apps) != 1 {
		t.Errorf("manifest mismatch: got %+v", got)
	}
	data, err := os.ReadFile(sqlDest)
	if err != nil || string(data) != "-- dump\nSELECT 1;\n" {
		t.Errorf("sql dump not extracted correctly: %q (%v)", data, err)
	}
}

func TestInspectAndExtractNoDB(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.tar.gz")
	writeTestArchive(t, archive, Info{Schema: SchemaVersion}, "")
	_, hasDB, err := inspectAndExtract(archive, filepath.Join(dir, "out.sql"))
	if err != nil {
		t.Fatalf("inspectAndExtract: %v", err)
	}
	if hasDB {
		t.Error("hasDB = true, want false for archive without dump")
	}
}

func TestInspectAndExtractMissingManifest(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.tar.gz")
	// Archive with only a SQL member, no manifest.
	f, _ := os.Create(archive)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "meta/postgres-all.sql", Mode: 0o644, Size: 3})
	_, _ = tw.Write([]byte("abc"))
	tw.Close()
	gz.Close()
	f.Close()

	if _, _, err := inspectAndExtract(archive, filepath.Join(dir, "out.sql")); err == nil {
		t.Fatal("expected error for archive without manifest, got nil")
	}
}

func TestReadManifest(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.tar.gz")
	writeTestArchive(t, archive, Info{Schema: SchemaVersion, SchoolName: "Musterschule"}, "x")
	got, err := readManifest(archive)
	if err != nil {
		t.Fatalf("readManifest: %v", err)
	}
	if got.SchoolName != "Musterschule" {
		t.Errorf("SchoolName = %q, want Musterschule", got.SchoolName)
	}
}

func TestCreatedAtFromName(t *testing.T) {
	fallback := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	got := createdAtFromName("backup-musterschule-20260624-143000.tar.gz.age", fallback)
	parsed, err := time.Parse(time.RFC3339, got)
	if err != nil {
		t.Fatalf("createdAtFromName returned unparseable %q: %v", got, err)
	}
	// 2026-06-24 14:30:00 local → check the date survived the round-trip.
	if parsed.Year() != 2026 || parsed.Month() != time.June || parsed.Day() != 24 {
		t.Errorf("createdAtFromName = %q, want 2026-06-24", got)
	}

	// Unparseable name → fallback mtime.
	if got := createdAtFromName("weird.tar.gz", fallback); got != "2000-01-01T00:00:00Z" {
		t.Errorf("fallback not used: %q", got)
	}
}
