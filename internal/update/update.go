// Package update implements stackctl self-update via GitHub Releases.
//
// The update flow (ARCHITECTURE.md §11.5):
//  1. GET https://api.github.com/repos/lngstck/stackctl/releases/latest
//  2. Compare tag with current version
//  3. Download binary, verify it runs ("stackctl version"), atomic replace
//  4. Write new version to stackctl.version
//  5. Optionally restart via systemctl
package update

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/lngstck/stackctl/internal/paths"
)

const (
	// GitHubRepo is the repository checked for releases.
	GitHubRepo = "lngstck/stackctl"
	// releasesURL is the GitHub API endpoint for the latest release.
	releasesURL = "https://api.github.com/repos/" + GitHubRepo + "/releases/latest"
	// httpTimeout is the timeout for GitHub API and download requests.
	httpTimeout = 60 * time.Second
	// checksumAsset is the release asset listing SHA-256 sums of the binaries,
	// in `shasum -a 256` format (one "<hex>␣␣<filename>" line per binary).
	checksumAsset = "SHA256SUMS"
)

// ReleaseInfo holds metadata about the latest GitHub release.
type ReleaseInfo struct {
	Tag         string `json:"tag_name"`
	Name        string `json:"name"`
	PublishedAt string `json:"published_at"`
	HTMLURL     string `json:"html_url"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

// CheckResult is returned by Check.
type CheckResult struct {
	CurrentVersion string
	LatestVersion  string
	UpdateAvailable bool
	Release         *ReleaseInfo
}

// Version is set at startup from main.version (injected via -ldflags).
// When non-empty and not "dev", it takes precedence over the on-disk
// stackctl.version file — the running binary is the source of truth.
var Version string

// CurrentVersion returns the version string for display and update checks.
// Preference: Version (ldflags) > stackctl.version file > "dev".
func CurrentVersion() string {
	if Version != "" && Version != "dev" {
		return strings.TrimPrefix(Version, "v")
	}
	data, err := os.ReadFile(paths.VersionFile())
	if err == nil {
		if s := strings.TrimSpace(string(data)); s != "" {
			return s
		}
	}
	if Version != "" {
		return Version
	}
	return "dev"
}

// Check queries GitHub for the latest release and compares it to the current
// installed version.
func Check() (*CheckResult, error) {
	current := CurrentVersion()

	rel, err := fetchLatestRelease()
	if err != nil {
		return nil, err
	}

	latest := strings.TrimPrefix(rel.Tag, "v")
	currentClean := strings.TrimPrefix(current, "v")

	return &CheckResult{
		CurrentVersion:  current,
		LatestVersion:   rel.Tag,
		UpdateAvailable: latest != currentClean && current != "dev",
		Release:         rel,
	}, nil
}

// Apply downloads and installs the latest release. It returns the new version
// string on success. The caller should restart the process afterwards (e.g.
// via systemctl restart stackctl).
func Apply(rel *ReleaseInfo) (string, error) {
	assetName := fmt.Sprintf("stackctl-linux-%s", runtime.GOARCH)
	var downloadURL, checksumURL string
	for _, a := range rel.Assets {
		switch a.Name {
		case assetName:
			downloadURL = a.BrowserDownloadURL
		case checksumAsset:
			checksumURL = a.BrowserDownloadURL
		}
	}
	if downloadURL == "" {
		return "", fmt.Errorf("no asset %q in release %s", assetName, rel.Tag)
	}
	// Refuse to update without a checksum to verify against. Every release from
	// the checksummed build onward ships SHA256SUMS; a missing file means a
	// hand-rolled or tampered release, which we will not auto-install.
	if checksumURL == "" {
		return "", fmt.Errorf("release %s has no %s asset — aborting for safety", rel.Tag, checksumAsset)
	}

	binaryPath := filepath.Join(paths.StackctlDir(), "stackctl")
	newPath := binaryPath + ".new"

	// Download.
	if err := downloadFile(downloadURL, newPath); err != nil {
		_ = os.Remove(newPath)
		return "", fmt.Errorf("download: %w", err)
	}

	// Verify the SHA-256 checksum before doing anything else with the file.
	expected, err := fetchExpectedSum(checksumURL, assetName)
	if err != nil {
		_ = os.Remove(newPath)
		return "", fmt.Errorf("fetch checksum: %w", err)
	}
	actual, err := sha256File(newPath)
	if err != nil {
		_ = os.Remove(newPath)
		return "", fmt.Errorf("hash download: %w", err)
	}
	if !strings.EqualFold(actual, expected) {
		_ = os.Remove(newPath)
		return "", fmt.Errorf("checksum mismatch for %s: expected %s, got %s", assetName, expected, actual)
	}

	// Make executable.
	if err := os.Chmod(newPath, 0o755); err != nil {
		_ = os.Remove(newPath)
		return "", fmt.Errorf("chmod: %w", err)
	}

	// Verify: new binary must print a version.
	out, err := exec.Command(newPath, "version").CombinedOutput()
	if err != nil {
		_ = os.Remove(newPath)
		return "", fmt.Errorf("verify new binary: %w\n%s", err, out)
	}

	// Atomic replace.
	if err := os.Rename(newPath, binaryPath); err != nil {
		_ = os.Remove(newPath)
		return "", fmt.Errorf("replace binary: %w", err)
	}

	// Write version file.
	newVersion := strings.TrimPrefix(rel.Tag, "v")
	if err := os.WriteFile(paths.VersionFile(), []byte(newVersion+"\n"), 0o644); err != nil {
		return newVersion, fmt.Errorf("write version file: %w (update succeeded though)", err)
	}

	return newVersion, nil
}

// RestartService veranlasst einen Neustart des Prozesses durch kontrollierten
// Self-Exit. Die systemd-Unit (Restart=always, RestartSec=1) bringt stackctl
// innerhalb ~1s mit dem neuen Binary wieder hoch. Kein sudo, kein sudoers,
// keine Privilege-Escalation — siehe ARCHITECTURE.md §11.5 und Issue #10.
//
// Die Funktion kehrt sofort zurueck (return nil), damit der HTTP-Handler dem
// Browser noch eine "Neustart…"-Antwort schicken kann. Nach RestartDelay
// killt eine Goroutine den Prozess via os.Exit(0); systemd uebernimmt.
//
// In Dev-Modus (kein systemd-Wrapper) wuerde os.Exit den Prozess tot lassen.
// Aufrufer (handlers_system) pruefen devMode und rufen RestartService dort
// nicht auf, sondern zeigen den "manuell starten"-Hinweis.
func RestartService() error {
	go func() {
		time.Sleep(RestartDelay)
		log.Printf("update: self-exit for systemd restart")
		os.Exit(0)
	}()
	return nil
}

// RestartDelay ist die Pause zwischen HTTP-Antwort und os.Exit. Lang genug,
// damit der Browser die Redirect-Antwort vollstaendig empfaengt; kurz genug,
// dass der User den Restart unmittelbar wahrnimmt.
var RestartDelay = 500 * time.Millisecond

// fetchLatestRelease calls the GitHub API.
func fetchLatestRelease() (*ReleaseInfo, error) {
	client := &http.Client{Timeout: httpTimeout}
	req, err := http.NewRequest("GET", releasesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "stackctl-self-update")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("no releases found")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("github api: %d %s", resp.StatusCode, body)
	}

	var rel ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("parse release: %w", err)
	}
	return &rel, nil
}

// downloadFile fetches url and writes it to dest.
func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %d", url, resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return f.Sync()
}

// fetchExpectedSum downloads the SHA256SUMS asset and returns the hex digest
// listed for filename. The file is the standard `shasum -a 256` format:
// "<hex>␣␣<filename>" per line (the second space may be "*" for binary mode).
func fetchExpectedSum(url, filename string) (string, error) {
	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %d", checksumAsset, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", err
	}
	sums := parseChecksums(data)
	sum, ok := sums[filename]
	if !ok {
		return "", fmt.Errorf("%s lists no entry for %s", checksumAsset, filename)
	}
	return sum, nil
}

// parseChecksums turns `shasum -a 256` output into a filename→hex map. It
// tolerates the binary-mode "*" marker and a leading "./" on the filename.
func parseChecksums(data []byte) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(strings.TrimPrefix(fields[1], "*"), "./")
		out[name] = fields[0]
	}
	return out
}

// sha256File returns the lower-case hex SHA-256 digest of the file at path.
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
