// Package envfile reads and writes the sectioned .env file that docker
// compose consumes. Sections are purely cosmetic — docker compose ignores
// the headers — but they make the file readable for the admin and let
// stackctl group per-app variables cleanly.
//
// File format (see ARCHITECTURE.md §12):
//
//	# === global ===
//	SCHOOL_NAME=Gymnasium Phoenix
//	SCHOOL_SLUG=phoenix
//
//	# === postgres ===
//	POSTGRES_PASSWORD=...
//
//	# === langflow ===
//	LANGFLOW_DB_PASSWORD=...
//
// Keys are unique across the whole file; a key can only live in one
// section at a time. Values are treated as opaque strings: no quoting,
// no escaping, no variable substitution. Trailing whitespace on the value
// is stripped on load.
package envfile

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/lngstck/stackctl/internal/paths"
)

// GlobalSection is the conventional name for system-wide variables. It is
// always emitted first when present.
const GlobalSection = "global"

// FilePerm for the .env file: 0640, so readable by the learningstack user
// and group, nothing else.
const FilePerm = 0o640

// sectionHeader matches `# === name ===` lines. Allows extra whitespace.
var sectionHeader = regexp.MustCompile(`^#\s*===\s*(\S+)\s*===\s*$`)

// File is an in-memory view of a .env file with section grouping.
// Zero value is not usable; construct via New() or Load().
type File struct {
	// sectionOrder is the order in which sections are emitted. A section
	// is added on first Set into it (or when Load sees its header).
	sectionOrder []string
	// keyOrder maps section -> ordered list of keys in that section.
	keyOrder map[string][]string
	// values maps key -> value (flat: keys are unique across sections).
	values map[string]string
	// sectionOf maps key -> section name (for fast relocation on Set).
	sectionOf map[string]string
}

// New returns an empty File.
func New() *File {
	return &File{
		sectionOrder: nil,
		keyOrder:     map[string][]string{},
		values:       map[string]string{},
		sectionOf:    map[string]string{},
	}
}

// Load reads path and returns its parsed representation. If path does not
// exist, Load returns (New(), nil) — callers usually want a blank file on
// first run rather than a distinct error.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return New(), nil
		}
		return nil, err
	}
	return Parse(string(data))
}

// Parse turns raw .env content into a File.
func Parse(content string) (*File, error) {
	f := New()
	section := GlobalSection
	scanner := bufio.NewScanner(strings.NewReader(content))
	lineno := 0
	for scanner.Scan() {
		lineno++
		raw := scanner.Text()
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if m := sectionHeader.FindStringSubmatch(line); m != nil {
			section = m[1]
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue // ordinary comment
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("envfile: line %d: invalid entry %q", lineno, raw)
		}
		key := strings.TrimSpace(line[:eq])
		value := strings.TrimRight(line[eq+1:], " \t")
		if key == "" {
			return nil, fmt.Errorf("envfile: line %d: empty key", lineno)
		}
		f.Set(section, key, value)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("envfile: scan: %w", err)
	}
	return f, nil
}

// Set stores key=value in the named section. If the key already exists in
// a different section, it is moved. An empty section name is treated as
// GlobalSection.
func (f *File) Set(section, key, value string) {
	if section == "" {
		section = GlobalSection
	}
	if prev, ok := f.sectionOf[key]; ok {
		if prev == section {
			f.values[key] = value
			return
		}
		// Remove from old section's key order.
		f.removeKeyFromSection(prev, key)
	}
	f.ensureSection(section)
	f.keyOrder[section] = append(f.keyOrder[section], key)
	f.values[key] = value
	f.sectionOf[key] = section
}

// Get returns the value for key if present.
func (f *File) Get(key string) (string, bool) {
	v, ok := f.values[key]
	return v, ok
}

// Delete removes key if present. Empty sections are pruned.
func (f *File) Delete(key string) {
	section, ok := f.sectionOf[key]
	if !ok {
		return
	}
	f.removeKeyFromSection(section, key)
	delete(f.values, key)
	delete(f.sectionOf, key)
}

// DeleteKeys bulk-removes keys.
func (f *File) DeleteKeys(keys []string) {
	for _, k := range keys {
		f.Delete(k)
	}
}

// Keys returns the ordered list of keys in the given section. Empty slice
// if the section does not exist.
func (f *File) Keys(section string) []string {
	if section == "" {
		section = GlobalSection
	}
	src := f.keyOrder[section]
	out := make([]string, len(src))
	copy(out, src)
	return out
}

// Sections returns the ordered list of sections currently present.
func (f *File) Sections() []string {
	out := make([]string, len(f.sectionOrder))
	copy(out, f.sectionOrder)
	return out
}

// AllKeys returns a sorted list of every key in the file. Useful for
// diagnostics and deterministic diffs in tests.
func (f *File) AllKeys() []string {
	out := make([]string, 0, len(f.values))
	for k := range f.values {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Render returns the serialized form of the file as a string.
// GlobalSection is emitted first if present; other sections follow in
// insertion order. Sections are separated by a blank line.
func (f *File) Render() string {
	var b strings.Builder
	b.WriteString("# Generated by stackctl – do not edit manually.\n")
	b.WriteString("# Sections are cosmetic; docker compose ignores them.\n")
	b.WriteString("\n")

	ordered := f.orderedSections()
	for i, sec := range ordered {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "# === %s ===\n", sec)
		for _, k := range f.keyOrder[sec] {
			fmt.Fprintf(&b, "%s=%s\n", k, f.values[k])
		}
	}
	return b.String()
}

// Save atomically writes the file to path with 0640 permissions.
func (f *File) Save(path string) error {
	return paths.AtomicWrite(path, []byte(f.Render()), FilePerm)
}

// WriteTo implements io.WriterTo for tests and ad-hoc use.
func (f *File) WriteTo(w io.Writer) (int64, error) {
	n, err := w.Write([]byte(f.Render()))
	return int64(n), err
}

// -- internals --------------------------------------------------------------

func (f *File) ensureSection(section string) {
	if _, ok := f.keyOrder[section]; ok {
		return
	}
	f.keyOrder[section] = nil
	f.sectionOrder = append(f.sectionOrder, section)
}

func (f *File) removeKeyFromSection(section, key string) {
	keys := f.keyOrder[section]
	for i, k := range keys {
		if k == key {
			f.keyOrder[section] = append(keys[:i], keys[i+1:]...)
			break
		}
	}
	if len(f.keyOrder[section]) == 0 {
		delete(f.keyOrder, section)
		for i, s := range f.sectionOrder {
			if s == section {
				f.sectionOrder = append(f.sectionOrder[:i], f.sectionOrder[i+1:]...)
				break
			}
		}
	}
}

// orderedSections returns sections with GlobalSection moved to the front.
func (f *File) orderedSections() []string {
	out := make([]string, 0, len(f.sectionOrder))
	hasGlobal := false
	for _, s := range f.sectionOrder {
		if s == GlobalSection {
			hasGlobal = true
			continue
		}
		out = append(out, s)
	}
	if hasGlobal {
		out = append([]string{GlobalSection}, out...)
	}
	return out
}
