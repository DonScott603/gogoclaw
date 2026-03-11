package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DonScott603/gogoclaw/internal/config"
)

func TestLoadProfileSimple(t *testing.T) {
	agents := map[string]config.AgentConfig{
		"base": {
			Name:             "Base Agent",
			SystemPromptFile: "base.md",
			PII:              config.PIIConfig{Mode: "disabled"},
			Context:          config.AgentContextConfig{MaxHistoryTokens: 4096},
		},
	}

	profile, err := LoadProfile(agents, "base")
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if profile.Name != "Base Agent" {
		t.Errorf("Name = %q, want %q", profile.Name, "Base Agent")
	}
	if profile.PII.Mode != "disabled" {
		t.Errorf("PII.Mode = %q, want %q", profile.PII.Mode, "disabled")
	}
}

func TestLoadProfileInheritance(t *testing.T) {
	agents := map[string]config.AgentConfig{
		"base": {
			Name:             "Base Agent",
			SystemPromptFile: "base.md",
			PII:              config.PIIConfig{Mode: "disabled"},
			Context:          config.AgentContextConfig{MaxHistoryTokens: 4096},
			Shell:            config.ShellConfig{Confirmation: "always"},
			MemoryConfig:     config.AgentMemoryConfig{Enabled: true, TopK: 10},
		},
		"secure": {
			Name:     "Secure Agent",
			Inherits: "base",
			PII:      config.PIIConfig{Mode: "strict"},
			Shell:    config.ShellConfig{Confirmation: "never"},
		},
	}

	profile, err := LoadProfile(agents, "secure")
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	// Child overrides.
	if profile.Name != "Secure Agent" {
		t.Errorf("Name = %q, want %q", profile.Name, "Secure Agent")
	}
	if profile.PII.Mode != "strict" {
		t.Errorf("PII.Mode = %q, want %q", profile.PII.Mode, "strict")
	}
	if profile.Shell.Confirmation != "never" {
		t.Errorf("Shell.Confirmation = %q, want %q", profile.Shell.Confirmation, "never")
	}
	// Parent fallthrough for zero-value fields.
	if profile.SystemPromptFile != "base.md" {
		t.Errorf("SystemPromptFile = %q, want %q", profile.SystemPromptFile, "base.md")
	}
	if profile.Context.MaxHistoryTokens != 4096 {
		t.Errorf("MaxHistoryTokens = %d, want 4096", profile.Context.MaxHistoryTokens)
	}
	if !profile.MemoryConfig.Enabled {
		t.Error("MemoryConfig.Enabled should be true from parent")
	}
	if profile.MemoryConfig.TopK != 10 {
		t.Errorf("MemoryConfig.TopK = %d, want 10", profile.MemoryConfig.TopK)
	}
	// Inherits should be cleared after resolution.
	if profile.Inherits != "" {
		t.Errorf("Inherits = %q, want empty", profile.Inherits)
	}
}

func TestLoadProfileCircularDetection(t *testing.T) {
	agents := map[string]config.AgentConfig{
		"a": {Name: "A", Inherits: "b"},
		"b": {Name: "B", Inherits: "a"},
	}

	_, err := LoadProfile(agents, "a")
	if err == nil {
		t.Fatal("expected circular inheritance error")
	}
	if !contains(err.Error(), "circular") {
		t.Errorf("error = %q, want to contain 'circular'", err.Error())
	}
}

func TestLoadProfileNotFound(t *testing.T) {
	agents := map[string]config.AgentConfig{}

	_, err := LoadProfile(agents, "missing")
	if err == nil {
		t.Fatal("expected not found error")
	}
}

func TestLoadProfileZeroValueFallthrough(t *testing.T) {
	agents := map[string]config.AgentConfig{
		"base": {
			Name:    "Base",
			Context: config.AgentContextConfig{MaxHistoryTokens: 8192},
			Skills:  config.AgentSkillsConfig{AlwaysAvailable: true, AutoDiscover: true},
		},
		"child": {
			Inherits: "base",
			// All zero values — should inherit everything from base.
		},
	}

	profile, err := LoadProfile(agents, "child")
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if profile.Name != "Base" {
		t.Errorf("Name = %q, want %q (inherited)", profile.Name, "Base")
	}
	if profile.Context.MaxHistoryTokens != 8192 {
		t.Errorf("MaxHistoryTokens = %d, want 8192 (inherited)", profile.Context.MaxHistoryTokens)
	}
	if !profile.Skills.AlwaysAvailable {
		t.Error("Skills.AlwaysAvailable should be true (inherited)")
	}
}

func TestLoadProfileSliceReplace(t *testing.T) {
	agents := map[string]config.AgentConfig{
		"base": {
			Name:   "Base",
			Skills: config.AgentSkillsConfig{Allowed: []string{"tool_a", "tool_b"}},
		},
		"child": {
			Inherits: "base",
			Skills:   config.AgentSkillsConfig{Allowed: []string{"tool_c"}},
		},
	}

	profile, err := LoadProfile(agents, "child")
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if len(profile.Skills.Allowed) != 1 || profile.Skills.Allowed[0] != "tool_c" {
		t.Errorf("Skills.Allowed = %v, want [tool_c] (child replaces parent)", profile.Skills.Allowed)
	}
}

func TestResolveSystemPromptExists(t *testing.T) {
	dir := t.TempDir()
	content := "You are a test agent."
	os.WriteFile(filepath.Join(dir, "test.md"), []byte(content), 0644)

	profile := config.AgentConfig{SystemPromptFile: "test.md"}
	prompt, err := ResolveSystemPrompt(dir, profile)
	if err != nil {
		t.Fatalf("ResolveSystemPrompt: %v", err)
	}
	if prompt != content {
		t.Errorf("prompt = %q, want %q", prompt, content)
	}
}

func TestResolveSystemPromptMissing(t *testing.T) {
	dir := t.TempDir()

	profile := config.AgentConfig{SystemPromptFile: "nonexistent.md"}
	prompt, err := ResolveSystemPrompt(dir, profile)
	if err != nil {
		t.Fatalf("ResolveSystemPrompt: %v", err)
	}
	if prompt != "" {
		t.Errorf("prompt = %q, want empty for missing file", prompt)
	}
}

func TestResolveSystemPromptEmpty(t *testing.T) {
	dir := t.TempDir()

	profile := config.AgentConfig{SystemPromptFile: ""}
	prompt, err := ResolveSystemPrompt(dir, profile)
	if err != nil {
		t.Fatalf("ResolveSystemPrompt: %v", err)
	}
	if prompt != "" {
		t.Errorf("prompt = %q, want empty", prompt)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsAt(s, substr)
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
