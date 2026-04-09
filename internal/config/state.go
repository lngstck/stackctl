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
const StateVersion = "2.0"

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
	TunnelEnabled    bool     `yaml:"tunnel_enabled"`
	TunnelSubdomain  string   `yaml:"tunnel_subdomain,omitempty"`
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
