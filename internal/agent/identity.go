package agent

import (
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Identity holds structured user/agent identity written during bootstrap.
type Identity struct {
	UserName         string `yaml:"user_name" json:"user_name"`
	AgentName        string `yaml:"agent_name" json:"agent_name"`
	Personality      string `yaml:"personality" json:"personality"`
	WorkDomain       string `yaml:"work_domain" json:"work_domain"`
	PIIMode          string `yaml:"pii_mode" json:"pii_mode"`
	BootstrapVersion int    `yaml:"bootstrap_version" json:"bootstrap_version"`
	BootstrappedAt   string `yaml:"bootstrapped_at" json:"bootstrapped_at"`
}

// LoadIdentity reads identity.yaml from the agents subdirectory of configDir.
// Returns nil if the file does not exist.
func LoadIdentity(configDir string) (*Identity, error) {
	path := filepath.Join(configDir, "agents", "identity.yaml")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var id Identity
	if err := yaml.Unmarshal(data, &id); err != nil {
		return nil, err
	}
	return &id, nil
}

// writeIdentityYAML writes identity.yaml to configDir/agents/.
func writeIdentityYAML(configDir string, s *BootstrapSummary) error {
	id := Identity{
		UserName:         s.UserName,
		AgentName:        s.AgentName,
		Personality:      s.Personality,
		WorkDomain:       s.WorkDomain,
		PIIMode:          s.PIIMode,
		BootstrapVersion: 1,
		BootstrappedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	data, err := yaml.Marshal(&id)
	if err != nil {
		return err
	}
	path := filepath.Join(configDir, "agents", "identity.yaml")
	return atomicWriteFile(path, data, 0600)
}
