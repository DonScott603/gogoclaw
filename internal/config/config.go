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
	if err := l.loadMemory(cfg); err != nil {
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
// Map key is always the filename (without .yaml); Name field is for display only.
func (l *Loader) loadProviders(cfg *Config) error {
	return l.loadDir("providers", func(path, defaultName string) error {
		var pc ProviderConfig
		if err := l.loadYAMLFile(path, &pc); err != nil {
			return err
		}
		if pc.Name == "" {
			pc.Name = defaultName
		}
		cfg.Providers[defaultName] = pc
		return nil
	})
}

// loadAgents scans the agents/ directory.
// Map key is always the filename (without .yaml); Name field is for display only.
func (l *Loader) loadAgents(cfg *Config) error {
	return l.loadDir("agents", func(path, defaultName string) error {
		var ac AgentConfig
		if err := l.loadYAMLFile(path, &ac); err != nil {
			return err
		}
		if ac.Name == "" {
			ac.Name = defaultName
		}
		cfg.Agents[defaultName] = ac
		return nil
	})
}

// loadChannels scans the channels/ directory.
// Map key is always the filename (without .yaml); Name field is for display only.
func (l *Loader) loadChannels(cfg *Config) error {
	return l.loadDir("channels", func(path, defaultName string) error {
		var cc ChannelConfig
		if err := l.loadYAMLFile(path, &cc); err != nil {
			return err
		}
		if cc.Name == "" {
			cc.Name = defaultName
		}
		cfg.Channels[defaultName] = cc
		return nil
	})
}

// loadDir scans a subdirectory of baseDir for YAML files and calls fn for each.
// The fn receives the full file path and a default name (filename without .yaml).
func (l *Loader) loadDir(subdir string, fn func(path, defaultName string) error) error {
	dir := filepath.Join(l.baseDir, subdir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("config: read %s dir: %w", subdir, err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		defaultName := e.Name()[:len(e.Name())-5] // strip .yaml
		if err := fn(filepath.Join(dir, e.Name()), defaultName); err != nil {
			return fmt.Errorf("config: load %s %s: %w", subdir, e.Name(), err)
		}
	}
	return nil
}

// loadNetwork reads the network.yaml file.
func (l *Loader) loadNetwork(cfg *Config) error {
	return l.loadYAML("network.yaml", &cfg.Network)
}

// loadMemory reads memory/config.yaml if it exists.
// Memory config can also be specified inline in the root config.yaml under the "memory" key.
// The separate file takes precedence if present.
func (l *Loader) loadMemory(cfg *Config) error {
	return l.loadYAMLFile(filepath.Join(l.baseDir, "memory", "config.yaml"), &cfg.Memory)
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
				Encrypt: false,
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

	if agent, ok := cfg.Agents["base"]; ok && agent.ProviderRouting.Mode != "" {
		switch agent.ProviderRouting.Mode {
		case "cloud-only", "local-only", "hybrid", "tiered":
			// valid
		default:
			return fmt.Errorf("config: validate: unrecognized provider routing mode %q (valid: cloud-only, local-only, hybrid, tiered)", agent.ProviderRouting.Mode)
		}
	}

	return nil
}
