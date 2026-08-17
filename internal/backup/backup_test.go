// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package backup

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"gopkg.in/yaml.v3"
)

func TestValidName(t *testing.T) {
	good := []string{
		"backup-musterschule-20260624-143000.tar.gz",
		"backup-musterschule-20260624-143000.tar.gz.age",
		"backup-st-marien-gymnasium-20260101-000000.tar.gz",
	}
	for _, n := range good {
		if !ValidName(n) {
			t.Errorf("ValidName(%q) = false, want true", n)
		}
	}
	bad := []string{
		"",
		"backup.tar.gz",
		"../etc/passwd",
		"backup-school-20260624-143000.tar.gz/../x",
		"backup-school-2026-143000.tar.gz",       // short date
		"backup-School-20260624-143000.tar.gz",   // uppercase slug
		"backup-school-20260624-143000.zip",      // wrong ext
		"backup-school-20260624-143000.tar.gz.x", // wrong trailing ext
	}
	for _, n := range bad {
		if ValidName(n) {
			t.Errorf("ValidName(%q) = true, want false", n)
		}
	}
}

func TestEncryptDecryptRoundtrip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "plain.bin")
	enc := filepath.Join(dir, "cipher.age")
	payload := bytes.Repeat([]byte("learningstack-backup-"), 4096)
	if err := os.WriteFile(src, payload, 0o600); err != nil {
		t.Fatal(err)
	}

	const pass = "correct horse battery staple"
	if err := encryptFile(src, enc, pass); err != nil {
		t.Fatalf("encryptFile: %v", err)
	}

	// Wrong passphrase must fail.
	if _, err := decrypt(t, enc, "wrong passphrase"); err == nil {
		t.Fatal("decrypt with wrong passphrase succeeded, want error")
	}

	got, err := decrypt(t, enc, pass)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("roundtrip mismatch: got %d bytes, want %d", len(got), len(payload))
	}
}

func decrypt(t *testing.T, path, pass string) ([]byte, error) {
	t.Helper()
	id, err := age.NewScryptIdentity(pass)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r, err := age.Decrypt(f, id)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

func TestManifestYAMLOmitsFileFields(t *testing.T) {
	info := Info{
		Schema:    SchemaVersion,
		File:      "backup-x-20260624-143000.tar.gz",
		SizeBytes: 12345,
		SHA256:    "deadbeef",
		Apps:      []AppEntry{{ID: "open-webui", Name: "OpenWebUI", Version: "1.2.3"}},
	}
	out, err := yaml.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"backup-x-", "12345", "deadbeef"} {
		if strings.Contains(string(out), forbidden) {
			t.Errorf("manifest YAML leaked file-only field %q:\n%s", forbidden, out)
		}
	}
	if !strings.Contains(string(out), "open-webui") {
		t.Errorf("manifest YAML missing app entry:\n%s", out)
	}
}

func TestSidecarJSONRoundtrip(t *testing.T) {
	want := Info{
		Schema:          SchemaVersion,
		File:            "backup-x-20260624-143000.tar.gz.age",
		CreatedAt:       "2026-06-24T14:30:00Z",
		StackctlVersion: "v0.7.0",
		SchoolSlug:      "musterschule",
		Encrypted:       true,
		IncludesDB:      true,
		SizeBytes:       999,
		SHA256:          "abc123",
		Apps:            []AppEntry{{ID: "dex", Name: "Dex", Version: "2.45.1"}},
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Info
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.File != want.File || got.SizeBytes != want.SizeBytes || got.SHA256 != want.SHA256 ||
		got.Encrypted != want.Encrypted || got.SchoolSlug != want.SchoolSlug || len(got.Apps) != 1 {
		t.Errorf("sidecar roundtrip mismatch:\n got %+v\nwant %+v", got, want)
	}
}
