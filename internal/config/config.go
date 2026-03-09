// Package config handles multi-file YAML configuration loading,
// environment variable resolution, validation, and live reload.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// Loader loads and merges configuration from a base directory.
type Loader struct {
	baseDir string
	mu      sync.RWMutex
	cfg     *Config
}

// NewLoader creates a Loader that reads config from baseDir.
func NewLoader(baseDir string) *Loader {
	return &Loader{baseDir: baseDir}
}

// Load reads all config files and returns the merged Config.
func (l *Loader) Load() (*Config, error) {
	cfg := DefaultConfig()

	if err := l.loadCoreConfig(cfg); err != nil {
		return nil, err
	}
	if err := l.loadProviders(cfg); err != nil {
		return nil, err
	}
	if err := l.loadAgents(cfg); err != nil {
		return nil, err
	}
	if err := l.loadChannels(cfg); err != nil {
		return nil, err
	}
	if err := l.loadNetwork(cfg); err != nil {
		return nil, err
	}

	if err := Validate(cfg); err != nil {
		return nil, fmt.Errorf("config: validation: %w", err)
	}

	l.mu.Lock()
	l.cfg = cfg
	l.mu.Unlock()

	return cfg, nil
}

// Current returns the most recently loaded config.
func (l *Loader) Current() *Config {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.cfg
}

// loadCoreConfig reads the top-level config.yaml.
func (l *Loader) loadCoreConfig(cfg *Config) error {
	return l.loadYAML("config.yaml", cfg)
}

// loadProviders scans the providers/ directory.
func (l *Loader) loadProviders(cfg *Config) error {
	dir := filepath.Join(l.baseDir, "providers")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("config: read providers dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		var pc ProviderConfig
		if err := l.loadYAMLFile(filepath.Join(dir, e.Name()), &pc); err != nil {
			return fmt.Errorf("config: load provider %s: %w", e.Name(), err)
		}
		if pc.Name == "" {
			pc.Name = e.Name()[:len(e.Name())-5] // strip .yaml
		}
		cfg.Providers[pc.Name] = pc
	}
	return nil
}

// loadAgents scans the agents/ directory.
func (l *Loader) loadAgents(cfg *Config) error {
	dir := filepath.Join(l.baseDir, "agents")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("config: read agents dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		var ac AgentConfig
		if err := l.loadYAMLFile(filepath.Join(dir, e.Name()), &ac); err != nil {
			return fmt.Errorf("config: load agent %s: %w", e.Name(), err)
		}
		if ac.Name == "" {
			ac.Name = e.Name()[:len(e.Name())-5]
		}
		cfg.Agents[ac.Name] = ac
	}
	return nil
}

// loadChannels scans the channels/ directory.
func (l *Loader) loadChannels(cfg *Config) error {
	dir := filepath.Join(l.baseDir, "channels")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("config: read channels dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		var cc ChannelConfig
		if err := l.loadYAMLFile(filepath.Join(dir, e.Name()), &cc); err != nil {
			return fmt.Errorf("config: load channel %s: %w", e.Name(), err)
		}
		if cc.Name == "" {
			cc.Name = e.Name()[:len(e.Name())-5]
		}
		cfg.Channels[cc.Name] = cc
	}
	return nil
}

// loadNetwork reads the network.yaml file.
func (l *Loader) loadNetwork(cfg *Config) error {
	return l.loadYAML("network.yaml", &cfg.Network)
}

// loadYAML reads a YAML file relative to baseDir into dest.
func (l *Loader) loadYAML(name string, dest interface{}) error {
	return l.loadYAMLFile(filepath.Join(l.baseDir, name), dest)
}

// loadYAMLFile reads a YAML file, resolves env vars, and unmarshals into dest.
func (l *Loader) loadYAMLFile(path string, dest interface{}) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("config: read %s: %w", path, err)
	}
	data = ResolveEnvVars(data)
	if err := yaml.Unmarshal(data, dest); err != nil {
		return fmt.Errorf("config: parse %s: %w", path, err)
	}
	return nil
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Workspace: WorkspaceConfig{
			Base:      "~/.gogoclaw/workspace",
			Inbox:     "~/.gogoclaw/workspace/inbox",
			Outbox:    "~/.gogoclaw/workspace/outbox",
			Scratch:   "~/.gogoclaw/workspace/scratch",
			Documents: "~/.gogoclaw/workspace/documents",
		},
		Logging: LoggingConfig{
			Level: "info",
			Audit: AuditConfig{
				Enabled: true,
				Path:    "~/.gogoclaw/audit/gogoclaw.jsonl",
			},
		},
		Storage: StorageConfig{
			Conversations: ConversationStorageConfig{
				Path:    "~/.gogoclaw/data/conversations.db",
				Encrypt: true,
			},
		},
		Providers: make(map[string]ProviderConfig),
		Agents:    make(map[string]AgentConfig),
		Channels:  make(map[string]ChannelConfig),
		Network: NetworkConfig{
			Allowlist:     []string{"localhost", "127.0.0.1"},
			DenyAllOthers: true,
			LogBlocked:    true,
		},
	}
}

// Validate checks the config for required fields and consistency.
func Validate(cfg *Config) error {
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	switch cfg.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("config: validate: invalid log level %q", cfg.Logging.Level)
	}
	for name, pc := range cfg.Providers {
		if pc.BaseURL == "" {
			return fmt.Errorf("config: validate: provider %q missing base_url", name)
		}
		if pc.Type == "" {
			return fmt.Errorf("config: validate: provider %q missing type", name)
		}
	}
	return nil
}
