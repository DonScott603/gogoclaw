package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLoaderDefaults(t *testing.T) {
	// Loading from a non-existent directory should return defaults.
	loader := NewLoader("/nonexistent/path")
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() with nonexistent dir: unexpected error: %v", err)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("default log level = %q, want %q", cfg.Logging.Level, "info")
	}
	if cfg.Network.DenyAllOthers != true {
		t.Error("default network.deny_all_others should be true")
	}
}

func TestLoaderProviders(t *testing.T) {
	dir := t.TempDir()

	// Create providers directory with a test provider.
	provDir := filepath.Join(dir, "providers")
	if err := os.MkdirAll(provDir, 0o755); err != nil {
		t.Fatal(err)
	}

	provYAML := `
name: "test-provider"
type: "openai_compatible"
base_url: "https://api.example.com/v1"
api_key: "test-key"
default_model: "gpt-4"
`
	if err := os.WriteFile(filepath.Join(provDir, "test.yaml"), []byte(provYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(dir)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	pc, ok := cfg.Providers["test"]
	if !ok {
		t.Fatal("provider 'test' not found in config (keyed by filename)")
	}
	// Name field is preserved for display.
	if pc.Name != "test-provider" {
		t.Errorf("provider Name = %q, want %q (display name from YAML)", pc.Name, "test-provider")
	}
	if pc.BaseURL != "https://api.example.com/v1" {
		t.Errorf("provider base_url = %q, want %q", pc.BaseURL, "https://api.example.com/v1")
	}
	if pc.DefaultModel != "gpt-4" {
		t.Errorf("provider default_model = %q, want %q", pc.DefaultModel, "gpt-4")
	}
}

func TestLoaderEnvResolution(t *testing.T) {
	dir := t.TempDir()

	provDir := filepath.Join(dir, "providers")
	if err := os.MkdirAll(provDir, 0o755); err != nil {
		t.Fatal(err)
	}

	os.Setenv("TEST_GOGOCLAW_KEY", "resolved-secret")
	defer os.Unsetenv("TEST_GOGOCLAW_KEY")

	provYAML := `
name: "envtest"
type: "openai_compatible"
base_url: "https://api.example.com/v1"
api_key: "${TEST_GOGOCLAW_KEY}"
default_model: "model"
`
	if err := os.WriteFile(filepath.Join(provDir, "envtest.yaml"), []byte(provYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(dir)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	pc, ok := cfg.Providers["envtest"]
	if !ok {
		t.Fatal("provider 'envtest' not found")
	}
	if pc.APIKey != "resolved-secret" {
		t.Errorf("api_key = %q, want %q", pc.APIKey, "resolved-secret")
	}
}

func TestValidateRejectsInvalidLogLevel(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Logging.Level = "invalid"
	if err := Validate(cfg); err == nil {
		t.Error("Validate() should reject invalid log level")
	}
}

func TestValidateRejectsMissingProviderURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Providers["bad"] = ProviderConfig{Name: "bad", Type: "openai_compatible"}
	if err := Validate(cfg); err == nil {
		t.Error("Validate() should reject provider with empty base_url")
	}
}

func TestLoaderMemoryConfig(t *testing.T) {
	dir := t.TempDir()

	memDir := filepath.Join(dir, "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}

	memYAML := `
enabled: true
embedding:
  provider: "ollama"
  model: "nomic-embed-text"
storage:
  backend: "chromem-go"
  path: "~/.gogoclaw/data/vectors"
retrieval:
  top_k: 10
  relevance_threshold: 0.7
  recency_weight: 0.2
`
	if err := os.WriteFile(filepath.Join(memDir, "config.yaml"), []byte(memYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(dir)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if !cfg.Memory.Enabled {
		t.Error("memory.enabled should be true")
	}
	if cfg.Memory.Embedding.Provider != "ollama" {
		t.Errorf("memory.embedding.provider = %q, want %q", cfg.Memory.Embedding.Provider, "ollama")
	}
	if cfg.Memory.Embedding.Model != "nomic-embed-text" {
		t.Errorf("memory.embedding.model = %q, want %q", cfg.Memory.Embedding.Model, "nomic-embed-text")
	}
	if cfg.Memory.Storage.Path != "~/.gogoclaw/data/vectors" {
		t.Errorf("memory.storage.path = %q, want %q", cfg.Memory.Storage.Path, "~/.gogoclaw/data/vectors")
	}
	if cfg.Memory.Retrieval.TopK != 10 {
		t.Errorf("memory.retrieval.top_k = %d, want 10", cfg.Memory.Retrieval.TopK)
	}
	if cfg.Memory.Retrieval.RelevanceThreshold != 0.7 {
		t.Errorf("memory.retrieval.relevance_threshold = %f, want 0.7", cfg.Memory.Retrieval.RelevanceThreshold)
	}
}

func TestLoaderMemoryInlineConfig(t *testing.T) {
	dir := t.TempDir()

	// Memory config inline in root config.yaml.
	configYAML := `
memory:
  enabled: true
  embedding:
    provider: "minimax"
    model: "embed-v1"
  retrieval:
    top_k: 5
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(configYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(dir)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if !cfg.Memory.Enabled {
		t.Error("memory.enabled should be true from inline config")
	}
	if cfg.Memory.Embedding.Provider != "minimax" {
		t.Errorf("memory.embedding.provider = %q, want %q", cfg.Memory.Embedding.Provider, "minimax")
	}
}

func TestLoaderMemorySeparateFileOverridesInline(t *testing.T) {
	dir := t.TempDir()

	// Inline config sets enabled=true with provider "inline-provider".
	configYAML := `
memory:
  enabled: true
  embedding:
    provider: "inline-provider"
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(configYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	// Separate file overrides with provider "file-provider".
	memDir := filepath.Join(dir, "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	memYAML := `
enabled: true
embedding:
  provider: "file-provider"
  model: "file-model"
`
	if err := os.WriteFile(filepath.Join(memDir, "config.yaml"), []byte(memYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(dir)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Separate file should win since it's loaded after the inline config.
	if cfg.Memory.Embedding.Provider != "file-provider" {
		t.Errorf("memory.embedding.provider = %q, want %q (separate file should override inline)", cfg.Memory.Embedding.Provider, "file-provider")
	}
}

func TestLoaderMCPConfig(t *testing.T) {
	dir := t.TempDir()

	mcpDir := filepath.Join(dir, "mcp")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		t.Fatal(err)
	}

	mcpYAML := `
name: "test-server"
transport: "stdio"
command: "node"
args: ["server.js", "--port", "3000"]
enabled: true
`
	if err := os.WriteFile(filepath.Join(mcpDir, "test-server.yaml"), []byte(mcpYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(dir)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	mc, ok := cfg.MCP["test-server"]
	if !ok {
		t.Fatal("MCP server 'test-server' not found in config")
	}
	if mc.Name != "test-server" {
		t.Errorf("name = %q, want %q", mc.Name, "test-server")
	}
	if mc.Transport != "stdio" {
		t.Errorf("transport = %q, want %q", mc.Transport, "stdio")
	}
	if mc.Command != "node" {
		t.Errorf("command = %q, want %q", mc.Command, "node")
	}
	if len(mc.Args) != 3 || mc.Args[0] != "server.js" {
		t.Errorf("args = %v, want [server.js --port 3000]", mc.Args)
	}
	if !mc.Enabled {
		t.Error("enabled should be true")
	}
}

func TestWebhookConfigParsing(t *testing.T) {
	yamlData := []byte(`
name: "telegram"
enabled: true
token_env: "GOGOCLAW_TELEGRAM_TOKEN"
allowed_users: ["alice"]
polling_timeout: 10s
webhook_url: "https://example.com/webhook"
webhook_listen: ":9443"
webhook_cert_file: "/path/cert.pem"
webhook_key_file: "/path/key.pem"
webhook_secret: "my-secret"
`)
	var cfg ChannelConfig
	if err := yaml.Unmarshal(yamlData, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.WebhookURL != "https://example.com/webhook" {
		t.Errorf("WebhookURL = %q", cfg.WebhookURL)
	}
	if cfg.WebhookListen != ":9443" {
		t.Errorf("WebhookListen = %q", cfg.WebhookListen)
	}
	if cfg.WebhookCertFile != "/path/cert.pem" {
		t.Errorf("WebhookCertFile = %q", cfg.WebhookCertFile)
	}
	if cfg.WebhookKeyFile != "/path/key.pem" {
		t.Errorf("WebhookKeyFile = %q", cfg.WebhookKeyFile)
	}
	if cfg.WebhookSecret != "my-secret" {
		t.Errorf("WebhookSecret = %q", cfg.WebhookSecret)
	}
}
