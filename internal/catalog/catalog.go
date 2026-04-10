package catalog

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/lngstck/stackctl/internal/paths"
)

// httpClient is the shared HTTP client for catalog downloads. The timeout
// is intentionally generous — school networks are often slow.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// Sync downloads the catalog index from catalogURL and caches it locally.
// Returns (true, nil) on success, (false, err) on failure. An existing
// cache is overwritten.
func Sync(catalogURL string) (bool, error) {
	if catalogURL == "" {
		return false, errors.New("catalog: empty catalog URL")
	}
	indexURL := strings.TrimRight(catalogURL, "/") + "/catalog.yaml"

	body, err := httpGet(indexURL)
	if err != nil {
		return false, fmt.Errorf("catalog: fetch index: %w", err)
	}

	// Validate that it's parseable.
	var idx Index
	if err := yaml.Unmarshal(body, &idx); err != nil {
		return false, fmt.Errorf("catalog: invalid index: %w", err)
	}
	if len(idx.Apps) == 0 {
		return false, errors.New("catalog: index has no apps")
	}

	if err := paths.EnsureDir(paths.CatalogCacheDir(), 0o750); err != nil {
		return false, err
	}
	if err := paths.AtomicWrite(paths.CatalogIndexFile(), body, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// LoadIndex reads the cached catalog index from disk.
func LoadIndex() (*Index, error) {
	data, err := os.ReadFile(paths.CatalogIndexFile())
	if err != nil {
		return nil, err
	}
	var idx Index
	if err := yaml.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("catalog: parse index: %w", err)
	}
	return &idx, nil
}

// ListApps returns the app summaries from the cached index, sorted by
// category then name.
func ListApps() ([]AppSummary, error) {
	idx, err := LoadIndex()
	if err != nil {
		return nil, err
	}
	apps := make([]AppSummary, len(idx.Apps))
	copy(apps, idx.Apps)
	sort.Slice(apps, func(i, j int) bool {
		if apps[i].Category != apps[j].Category {
			return apps[i].Category < apps[j].Category
		}
		return apps[i].Name < apps[j].Name
	})
	return apps, nil
}

// FetchDefinition downloads a single app definition from the catalog URL
// and caches it locally. Returns the parsed Definition.
func FetchDefinition(catalogURL, appID string) (*Definition, error) {
	if catalogURL == "" || appID == "" {
		return nil, errors.New("catalog: empty URL or appID")
	}
	defURL := strings.TrimRight(catalogURL, "/") + "/containers/" + appID + ".yaml"

	body, err := httpGet(defURL)
	if err != nil {
		return nil, fmt.Errorf("catalog: fetch %s: %w", appID, err)
	}

	def, err := parseDefinition(body)
	if err != nil {
		return nil, fmt.Errorf("catalog: %s: %w", appID, err)
	}

	if err := paths.EnsureDir(paths.CatalogContainersDir(), 0o750); err != nil {
		return nil, err
	}
	if err := paths.AtomicWrite(paths.AppDefinitionFile(appID), body, 0o644); err != nil {
		return nil, err
	}
	return def, nil
}

// LoadDefinition reads a cached app definition from disk.
func LoadDefinition(appID string) (*Definition, error) {
	data, err := os.ReadFile(paths.AppDefinitionFile(appID))
	if err != nil {
		return nil, err
	}
	return parseDefinition(data)
}

// GetOrFetch loads from cache first; on cache miss fetches from catalogURL.
func GetOrFetch(catalogURL, appID string) (*Definition, error) {
	def, err := LoadDefinition(appID)
	if err == nil {
		return def, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return FetchDefinition(catalogURL, appID)
}

// Validate performs a structural check on a definition. Returns a list of
// problems; empty slice means valid.
func Validate(def *Definition) []string {
	var problems []string
	check := func(cond bool, msg string) {
		if cond {
			problems = append(problems, msg)
		}
	}
	check(def.ID == "", "id is required")
	check(def.Name == "", "name is required")
	check(def.Version == "", "version is required")
	check(def.Image.Name == "", "image.name is required")
	check(def.Image.Tag == "", "image.tag is required")
	check(len(def.Ports) == 0, "at least one port is required")

	for i, p := range def.Ports {
		check(p.Host == 0, fmt.Sprintf("ports[%d].host is 0", i))
		check(p.Container == 0, fmt.Sprintf("ports[%d].container is 0", i))
	}
	for i, v := range def.Volumes {
		check(v.Host == "", fmt.Sprintf("volumes[%d].host is empty", i))
		check(v.Container == "", fmt.Sprintf("volumes[%d].container is empty", i))
	}
	if def.OIDC != nil {
		check(def.OIDC.ClientID == "", "oidc.client_id is required")
		check(def.OIDC.RedirectURITemplate == "", "oidc.redirect_uri_template is required")
	}
	return problems
}

// MissingDependencies returns the dep IDs from def.DependsOn that are not
// in installedIDs.
func MissingDependencies(def *Definition, installedIDs map[string]bool) []string {
	var missing []string
	for _, dep := range def.DependsOn {
		if !installedIDs[dep] {
			missing = append(missing, dep)
		}
	}
	return missing
}

// HasUpdate compares the installed version (from state) to the catalog
// definition version. Returns true if the catalog version is different
// (simple string comparison — SemVer ordering is not needed because the
// catalog always serves the latest version).
func HasUpdate(installedVersion, catalogVersion string) bool {
	return installedVersion != "" &&
		catalogVersion != "" &&
		installedVersion != catalogVersion
}

// -- internals --------------------------------------------------------------

func parseDefinition(data []byte) (*Definition, error) {
	var def Definition
	if err := yaml.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("parse definition: %w", err)
	}
	if def.ID == "" {
		return nil, errors.New("definition missing id")
	}
	return &def, nil
}

func httpGet(url string) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body from %s: %w", url, err)
	}
	return body, nil
}
