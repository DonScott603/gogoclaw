package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DonScott603/gogoclaw/internal/config"
)

// mockSender returns canned responses in sequence.
type mockSender struct {
	responses []string
	calls     int
}

func (m *mockSender) Send(_ context.Context, _ string) (string, error) {
	if m.calls >= len(m.responses) {
		return "", fmt.Errorf("no more canned responses")
	}
	resp := m.responses[m.calls]
	m.calls++
	return resp, nil
}

func TestBootstrapInfrastructure(t *testing.T) {
	configDir := t.TempDir()
	templatesDir := t.TempDir()

	// Create a minimal template file.
	os.MkdirAll(filepath.Join(templatesDir, "agents"), 0755)
	os.WriteFile(filepath.Join(templatesDir, "config.yaml"), []byte("logging:\n  level: info\n"), 0644)
	os.WriteFile(filepath.Join(templatesDir, "agents", "base.yaml"), []byte("name: test\n"), 0644)

	err := bootstrapInfrastructure(configDir, templatesDir)
	if err != nil {
		t.Fatalf("bootstrapInfrastructure: %v", err)
	}

	// Verify directories were created.
	for _, dir := range bootstrapDirs {
		path := filepath.Join(configDir, dir)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("directory %s not created: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", dir)
		}
	}

	// Verify template was copied.
	data, err := os.ReadFile(filepath.Join(configDir, "config.yaml"))
	if err != nil {
		t.Fatalf("config.yaml not copied: %v", err)
	}
	if !strings.Contains(string(data), "logging") {
		t.Error("config.yaml content mismatch")
	}
}

func TestBootstrapInfrastructureNoOverwrite(t *testing.T) {
	configDir := t.TempDir()
	templatesDir := t.TempDir()

	// Pre-create a config file with custom content.
	os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("custom content\n"), 0644)

	// Create template with different content.
	os.WriteFile(filepath.Join(templatesDir, "config.yaml"), []byte("template content\n"), 0644)

	err := bootstrapInfrastructure(configDir, templatesDir)
	if err != nil {
		t.Fatalf("bootstrapInfrastructure: %v", err)
	}

	// Existing file should not be overwritten.
	data, _ := os.ReadFile(filepath.Join(configDir, "config.yaml"))
	if string(data) != "custom content\n" {
		t.Errorf("existing config.yaml was overwritten: got %q", string(data))
	}
}

func TestParseJSONSummary(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		wantNil bool
		want    *BootstrapSummary
	}{
		{
			name: "valid_json_block",
			text: "Here is your summary:\n```json\n{\"user_name\": \"Alice\", \"personality\": \"casual\", \"work_domain\": \"engineering\", \"pii_mode\": \"warn\"}\n```\nDone!",
			want: &BootstrapSummary{UserName: "Alice", Personality: "casual", WorkDomain: "engineering", PIIMode: "warn"},
		},
		{
			name:    "no_json_block",
			text:    "What is your name?",
			wantNil: true,
		},
		{
			name:    "invalid_json",
			text:    "```json\n{invalid}\n```",
			wantNil: true,
		},
		{
			name:    "empty_user_name",
			text:    "```json\n{\"user_name\": \"\", \"pii_mode\": \"disabled\"}\n```",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseJSONSummary(tt.text)
			if tt.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil summary")
			}
			if got.UserName != tt.want.UserName {
				t.Errorf("UserName = %q, want %q", got.UserName, tt.want.UserName)
			}
			if got.Personality != tt.want.Personality {
				t.Errorf("Personality = %q, want %q", got.Personality, tt.want.Personality)
			}
			if got.WorkDomain != tt.want.WorkDomain {
				t.Errorf("WorkDomain = %q, want %q", got.WorkDomain, tt.want.WorkDomain)
			}
			if got.PIIMode != tt.want.PIIMode {
				t.Errorf("PIIMode = %q, want %q", got.PIIMode, tt.want.PIIMode)
			}
		})
	}
}

func TestBootstrapIdentityWithMockEngine(t *testing.T) {
	templatesDir := t.TempDir()

	// Write bootstrap template.
	os.WriteFile(filepath.Join(templatesDir, "bootstrap.md"), []byte("Ask setup questions."), 0644)

	jsonSummary := "Here is your config:\n```json\n{\"user_name\": \"Bob\", \"personality\": \"concise\", \"work_domain\": \"devops\", \"pii_mode\": \"strict\"}\n```"

	sender := &mockSender{
		responses: []string{
			"Welcome! What is your name?",
			"Nice, Bob! What personality?",
			"What's your domain?",
			jsonSummary,
		},
	}

	stdin := strings.NewReader("Bob\nconcise\ndevops\nstrict\n")
	stdout := &bytes.Buffer{}

	summary, err := bootstrapIdentity(context.Background(), sender, templatesDir, stdin, stdout)
	if err != nil {
		t.Fatalf("bootstrapIdentity: %v", err)
	}

	if summary.UserName != "Bob" {
		t.Errorf("UserName = %q, want Bob", summary.UserName)
	}
	if summary.PIIMode != "strict" {
		t.Errorf("PIIMode = %q, want strict", summary.PIIMode)
	}
}

func TestWriteBootstrapResults(t *testing.T) {
	configDir := t.TempDir()
	os.MkdirAll(filepath.Join(configDir, "agents"), 0755)

	// Write a base.yaml with pii mode.
	baseYAML := "name: \"Base\"\npii:\n  mode: \"disabled\"\n"
	os.WriteFile(filepath.Join(configDir, "agents", "base.yaml"), []byte(baseYAML), 0644)

	summary := &BootstrapSummary{
		UserName:    "Alice",
		Personality: "professional",
		WorkDomain:  "security",
		PIIMode:     "strict",
	}

	err := writeBootstrapResults(configDir, summary)
	if err != nil {
		t.Fatalf("writeBootstrapResults: %v", err)
	}

	// Check user.md was created.
	data, err := os.ReadFile(filepath.Join(configDir, "agents", "user.md"))
	if err != nil {
		t.Fatalf("user.md not created: %v", err)
	}
	if !strings.Contains(string(data), "Name: Alice") {
		t.Errorf("user.md missing name: %s", data)
	}
	if !strings.Contains(string(data), "Work Domain: security") {
		t.Errorf("user.md missing work domain: %s", data)
	}
}

func TestIsBootstrapped(t *testing.T) {
	dir := t.TempDir()

	if IsBootstrapped(dir) {
		t.Error("should not be bootstrapped initially")
	}

	os.WriteFile(filepath.Join(dir, "initialized"), []byte("ok\n"), 0644)

	if !IsBootstrapped(dir) {
		t.Error("should be bootstrapped after marker created")
	}
}

func TestValidateProvidersEmpty(t *testing.T) {
	cfg := &config.Config{
		Providers: map[string]config.ProviderConfig{},
	}
	err := validateProviders(context.Background(), cfg)
	if err == nil {
		t.Error("expected error for empty providers")
	}
}

func TestValidateProvidersPresent(t *testing.T) {
	cfg := &config.Config{
		Providers: map[string]config.ProviderConfig{
			"test": {Name: "test", Type: "ollama", BaseURL: "http://localhost:11434"},
		},
	}
	err := validateProviders(context.Background(), cfg)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
