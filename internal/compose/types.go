// Package compose generates the docker-compose.yml consumed by the
// learningstack container stack.
//
// The AppDefinition types here mirror the container definition YAML schema
// (see CLAUDE.md and docs/catalog-spec.md) — but only the fields needed for
// compose generation. The catalog package loads them from YAML; compose only
// reads them.
package compose

// AppDefinition is the subset of a catalog app definition that compose needs
// to build a docker-compose service block. Fields are tagged for yaml.v3
// so the catalog package can unmarshal directly into this type.
type AppDefinition struct {
	ID          string      `yaml:"id"`
	Name        string      `yaml:"name"`
	Version     string      `yaml:"version"`
	Image       ImageSpec   `yaml:"image"`
	Ports       []PortSpec  `yaml:"ports,omitempty"`
	Volumes     []VolumeSpec `yaml:"volumes,omitempty"`
	Environment []EnvVar    `yaml:"environment,omitempty"`
	DependsOn   []string    `yaml:"depends_on,omitempty"`
	Command     []string    `yaml:"command,omitempty"`
	Configs     []ConfigSpec `yaml:"configs,omitempty"`
}

// ImageSpec identifies the Docker image.
type ImageSpec struct {
	Name string `yaml:"name"`
	Tag  string `yaml:"tag"`
}

// FullImage returns "name:tag".
func (i ImageSpec) FullImage() string {
	if i.Tag == "" {
		return i.Name
	}
	return i.Name + ":" + i.Tag
}

// PortSpec maps a host port to a container port with optional bind address.
type PortSpec struct {
	Host        int    `yaml:"host"`
	Container   int    `yaml:"container"`
	Bind        string `yaml:"bind,omitempty"`        // e.g. "127.0.0.1", default "0.0.0.0"
	Description string `yaml:"description,omitempty"` // for UI only
}

// VolumeSpec maps a host path to a container path.
type VolumeSpec struct {
	Host      string `yaml:"host"`
	Container string `yaml:"container"`
	ReadOnly  bool   `yaml:"readonly,omitempty"`
}

// EnvVar is a key=value pair injected into the container environment.
// Values may contain ${VAR} references resolved by docker compose from .env.
type EnvVar struct {
	Key   string `yaml:"key"`
	Value string `yaml:"value"`
}

// ConfigSpec is a file written to the host before the container starts.
// compose itself does not use this (it's for create_data_directories), but
// it lives here so AppDefinition is self-contained.
type ConfigSpec struct {
	Path    string `yaml:"path"`
	Content string `yaml:"content"`
	Mode    string `yaml:"mode,omitempty"`
}
