package config

import (
	"os"
	"path/filepath"
	"testing"
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

	pc, ok := cfg.Providers["test-provider"]
	if !ok {
		t.Fatal("provider 'test-provider' not found in config")
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
