// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package update

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lngstck/stackctl/internal/paths"
)

// newReleaseServer serves a fake binary and a SHA256SUMS file. The checksum
// listed for the binary is `sumOverride` if non-empty, else the real digest.
func newReleaseServer(t *testing.T, binary []byte, assetName, sumOverride string) (*httptest.Server, *ReleaseInfo) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/binary", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(binary) })

	sum := sumOverride
	if sum == "" {
		d := sha256.Sum256(binary)
		sum = hex.EncodeToString(d[:])
	}
	sums := fmt.Sprintf("%s  %s\n", sum, assetName)
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(sums)) })

	srv := httptest.NewServer(mux)
	rel := &ReleaseInfo{Tag: "v9.9.9"}
	rel.Assets = append(rel.Assets,
		struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		}{Name: assetName, BrowserDownloadURL: srv.URL + "/binary"},
		struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		}{Name: checksumAsset, BrowserDownloadURL: srv.URL + "/sums"},
	)
	return srv, rel
}

func TestApplyRejectsChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(paths.EnvStackctlDir, dir)
	assetName := fmt.Sprintf("stackctl-linux-%s", runtime.GOARCH)

	srv, rel := newReleaseServer(t, []byte("the real binary"), assetName,
		"00000000000000000000000000000000000000000000000000000000deadbeef")
	defer srv.Close()

	_, err := Apply(rel)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want checksum mismatch error, got %v", err)
	}
	// The tampered download must not be left behind as stackctl.new.
	if _, statErr := os.Stat(filepath.Join(dir, "stackctl.new")); !os.IsNotExist(statErr) {
		t.Fatal("stackctl.new should have been removed after mismatch")
	}
}

func TestApplyRejectsMissingChecksumAsset(t *testing.T) {
	t.Setenv(paths.EnvStackctlDir, t.TempDir())
	assetName := fmt.Sprintf("stackctl-linux-%s", runtime.GOARCH)

	// Release with only the binary, no SHA256SUMS.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("bin"))
	}))
	defer srv.Close()
	rel := &ReleaseInfo{Tag: "v9.9.9"}
	rel.Assets = append(rel.Assets, struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	}{Name: assetName, BrowserDownloadURL: srv.URL})

	_, err := Apply(rel)
	if err == nil || !strings.Contains(err.Error(), checksumAsset) {
		t.Fatalf("want missing-checksum error, got %v", err)
	}
}

// A matching checksum opens the gate: Apply proceeds past verification and only
// then fails because the fake "binary" is not actually executable. That proves
// the checksum stage passed rather than blocked.
func TestApplyMatchingChecksumPassesGate(t *testing.T) {
	t.Setenv(paths.EnvStackctlDir, t.TempDir())
	assetName := fmt.Sprintf("stackctl-linux-%s", runtime.GOARCH)

	srv, rel := newReleaseServer(t, []byte("not a real elf binary"), assetName, "")
	defer srv.Close()

	_, err := Apply(rel)
	if err == nil {
		t.Fatal("expected exec-verify failure for a non-executable payload")
	}
	if strings.Contains(err.Error(), "checksum") {
		t.Fatalf("checksum stage should have passed, got %v", err)
	}
	if !strings.Contains(err.Error(), "verify new binary") {
		t.Fatalf("want exec-verify failure, got %v", err)
	}
}
