package skill

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Manifest describes a skill's metadata, tools, and permission requirements.
type Manifest struct {
	Name        string       `yaml:"name"`
	Version     string       `yaml:"version"`
	Description string       `yaml:"description"`
	Author      string       `yaml:"author"`
	Hash        string       `yaml:"hash,omitempty"` // SHA-256 of the .wasm binary
	Tools       []ToolSpec   `yaml:"tools"`
	Permissions Permissions  `yaml:"permissions"`
	CreatedAt   time.Time    `yaml:"created_at,omitempty"`
}

// ToolSpec describes a single tool exported by a skill.
type ToolSpec struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Parameters  string `yaml:"parameters"` // JSON Schema as a string
}

// Permissions declares the capabilities a skill requires.
type Permissions struct {
	Filesystem  FilesystemPerms `yaml:"filesystem"`
	Network     NetworkPerms    `yaml:"network"`
	EnvVars     []string        `yaml:"env_vars,omitempty"`
	MaxFileSize int64           `yaml:"max_file_size,omitempty"`  // bytes; 0 = default
	MaxExecTime int             `yaml:"max_execution_time,omitempty"` // seconds; 0 = default
}

// FilesystemPerms declares read/write path access for a skill.
type FilesystemPerms struct {
	ReadPaths  []string `yaml:"read_paths,omitempty"`
	WritePaths []string `yaml:"write_paths,omitempty"`
}

// NetworkPerms declares network access for a skill.
type NetworkPerms struct {
	Allowed bool     `yaml:"allowed"`
	Domains []string `yaml:"domains,omitempty"`
}

// ParseManifest reads and validates a manifest from a YAML file.
func ParseManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("skill: read manifest: %w", err)
	}
	return ParseManifestBytes(data)
}

// ParseManifestBytes parses and validates a manifest from YAML bytes.
func ParseManifestBytes(data []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("skill: parse manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate checks that required fields are present and well-formed.
func (m *Manifest) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("skill: manifest: name is required")
	}
	if m.Version == "" {
		return fmt.Errorf("skill: manifest: version is required")
	}
	if m.Description == "" {
		return fmt.Errorf("skill: manifest: description is required")
	}
	for i, t := range m.Tools {
		if t.Name == "" {
			return fmt.Errorf("skill: manifest: tools[%d]: name is required", i)
		}
		if t.Description == "" {
			return fmt.Errorf("skill: manifest: tools[%d]: description is required", i)
		}
	}
	return nil
}
