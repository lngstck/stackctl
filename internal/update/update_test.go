// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package update

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestParseChecksums(t *testing.T) {
	in := []byte(
		"abc123  stackctl-linux-amd64\n" +
			"def456 *stackctl-linux-arm64\n" + // binary-mode marker
			"789aaa  ./SHA256SUMS\n" + // leading ./
			"garbageline\n" + // ignored
			"\n", // blank ignored
	)
	got := parseChecksums(in)
	want := map[string]string{
		"stackctl-linux-amd64": "abc123",
		"stackctl-linux-arm64": "def456",
		"SHA256SUMS":           "789aaa",
	}
	if len(got) != len(want) {
		t.Fatalf("entries: want %d, got %d (%v)", len(want), len(got), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: want %q, got %q", k, v, got[k])
		}
	}
}

func TestSha256File(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bin")
	content := []byte("hello stackctl")
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	want := hex.EncodeToString(sum[:])

	got, err := sha256File(p)
	if err != nil {
		t.Fatalf("sha256File: %v", err)
	}
	if got != want {
		t.Fatalf("digest: want %s, got %s", want, got)
	}
}

func TestFetchExpectedSum(t *testing.T) {
	body := "aaaa1111  stackctl-linux-amd64\nbbbb2222  stackctl-linux-arm64\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	got, err := fetchExpectedSum(srv.URL, "stackctl-linux-arm64")
	if err != nil {
		t.Fatalf("fetchExpectedSum: %v", err)
	}
	if got != "bbbb2222" {
		t.Fatalf("sum: want bbbb2222, got %s", got)
	}

	// A filename not listed must be an error — never silently "verified".
	if _, err := fetchExpectedSum(srv.URL, "stackctl-linux-riscv"); err == nil {
		t.Fatal("expected error for missing filename, got nil")
	}
}
