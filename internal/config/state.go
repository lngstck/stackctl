package config

import (
	"errors"
	"fmt"
	"os"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/lngstck/stackctl/internal/paths"
)

// StateVersion is the schema version of state.yaml.
//
// 3.0 renamed tunnel_enabled/tunnel_subdomain to public_enabled/public_host.
// The flag never meant "a tunnel exists" but "this app is reachable from the
// internet", and with a direct-transport install there is no tunnel at all.
const StateVersion = "3.0"

// State mirrors state.yaml from ARCHITECTURE.md §12.
type State struct {
	Version    string                     `yaml:"version"`
	Containers map[string]*ContainerState `yaml:"containers"`
	// Ports maps host_port -> app_id so we can reject conflicts on install.
	Ports map[int]string `yaml:"ports"`
}

// ContainerState tracks everything we know about an installed app from the
// control plane's point of view.
type ContainerState struct {
	ID               string   `yaml:"id"`
	Name             string   `yaml:"name"`
	VersionInstalled string   `yaml:"version_installed,omitempty"`
	Ports            []int    `yaml:"ports"`
	EnvKeys          []string `yaml:"env_keys"`
	InstalledAt      string   `yaml:"installed_at"`
	// PublicEnabled is true when this app is reachable from the internet —
	// through a relay tunnel or a local proxy route, depending on the
	// install's transport.
	PublicEnabled bool `yaml:"public_enabled"`
	// PublicHost is the FQDN the app answers on, e.g.
	// "pylearn.phoenix.learningstack.online".
	PublicHost string `yaml:"public_host,omitempty"`
	// AutoUpdateDisabled schliesst diese App vom naechtlichen Auto-Update
	// aus. Manuelle Updates (Web-UI "Aktualisieren") sind weiterhin moeglich.
	AutoUpdateDisabled bool `yaml:"auto_update_disabled,omitempty"`
}

// NewState returns an empty state with the current schema version.
func NewState() *State {
	return &State{
		Version:    StateVersion,
		Containers: map[string]*ContainerState{},
		Ports:      map[int]string{},
	}
}

// LoadState reads state.yaml. If the file does not exist, an empty State
// is returned with nil error so callers don't have to branch on first run.
func LoadState() (*State, error) {
	data, err := os.ReadFile(paths.StateFile())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return NewState(), nil
		}
		return nil, err
	}
	s := NewState()
	if err := yaml.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", paths.StateFile(), err)
	}
	if s.Version == "" {
		s.Version = StateVersion
	}
	if s.Containers == nil {
		s.Containers = map[string]*ContainerState{}
	}
	if s.Ports == nil {
		s.Ports = map[int]string{}
	}
	return s, nil
}

// Save writes state.yaml atomically with 0640 permissions.
func (s *State) Save() error {
	if s == nil {
		return errors.New("state.Save: nil receiver")
	}
	if s.Version == "" {
		s.Version = StateVersion
	}
	if err := paths.EnsureDir(paths.ConfigDir(), 0o750); err != nil {
		return err
	}
	data, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	return paths.AtomicWrite(paths.StateFile(), data, FilePerm)
}

// Clone returns a deep copy of the state: the Containers and Ports maps and
// each ContainerState (incl. its Ports/EnvKeys slices) are duplicated, so a
// caller can mutate the copy without racing readers of the original. The web
// UI uses this to run a long install/update on a clone and merge the result
// back under a lock (see Server.commitState).
func (s *State) Clone() *State {
	if s == nil {
		return NewState()
	}
	ns := &State{
		Version:    s.Version,
		Containers: make(map[string]*ContainerState, len(s.Containers)),
		Ports:      make(map[int]string, len(s.Ports)),
	}
	for id, cs := range s.Containers {
		cp := *cs
		cp.Ports = append([]int(nil), cs.Ports...)
		cp.EnvKeys = append([]string(nil), cs.EnvKeys...)
		ns.Containers[id] = &cp
	}
	for p, id := range s.Ports {
		ns.Ports[p] = id
	}
	return ns
}

// IsInstalled reports whether appID is registered in the state file.
func (s *State) IsInstalled(appID string) bool {
	_, ok := s.Containers[appID]
	return ok
}

// PortOwner returns the app ID that currently holds the given host port, or
// empty string if the port is free.
func (s *State) PortOwner(port int) string {
	return s.Ports[port]
}

// InstalledIDs returns the stable sorted list of installed app IDs.
func (s *State) InstalledIDs() []string {
	out := make([]string, 0, len(s.Containers))
	for id := range s.Containers {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
