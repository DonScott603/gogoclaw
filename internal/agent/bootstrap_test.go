package agent

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	// Create minimal template files that templateFiles references.
	os.MkdirAll(filepath.Join(templatesDir, "agents"), 0755)
	os.WriteFile(filepath.Join(templatesDir, "config.yaml"), []byte("logging:\n  level: info\n"), 0644)
	os.WriteFile(filepath.Join(templatesDir, "agents", "base.md"), []byte("You are a test agent."), 0644)

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

	// Verify base.md was copied.
	data, err = os.ReadFile(filepath.Join(configDir, "agents", "base.md"))
	if err != nil {
		t.Fatalf("agents/base.md not copied: %v", err)
	}
	if !strings.Contains(string(data), "test agent") {
		t.Error("base.md content mismatch")
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

func TestParseJSONSummaryFull(t *testing.T) {
	text := "Here is your config:\n```json\n" +
		`{"user_name":"Alice","agent_name":"Jarvis","personality":"casual","work_domain":"engineering","pii_mode":"warn","provider_type":"openai","provider_base_url":"https://api.openai.com/v1","provider_api_key_env":"OPENAI_API_KEY","provider_model":"gpt-4o","telegram_enabled":true,"telegram_token_env":"MY_TG_TOKEN","rest_enabled":true,"rest_port":9090,"rest_api_key_env":"MY_REST_KEY"}` +
		"\n```\nDone!"

	got := parseJSONSummary(text)
	if got == nil {
		t.Fatal("expected non-nil summary")
	}
	if got.UserName != "Alice" {
		t.Errorf("UserName = %q", got.UserName)
	}
	if got.AgentName != "Jarvis" {
		t.Errorf("AgentName = %q", got.AgentName)
	}
	if got.ProviderType != "openai" {
		t.Errorf("ProviderType = %q", got.ProviderType)
	}
	if got.ProviderBaseURL != "https://api.openai.com/v1" {
		t.Errorf("ProviderBaseURL = %q", got.ProviderBaseURL)
	}
	if got.ProviderModel != "gpt-4o" {
		t.Errorf("ProviderModel = %q", got.ProviderModel)
	}
	if !got.TelegramEnabled {
		t.Error("TelegramEnabled should be true")
	}
	if got.TelegramToken != "MY_TG_TOKEN" {
		t.Errorf("TelegramToken = %q", got.TelegramToken)
	}
	if got.RESTPort != 9090 {
		t.Errorf("RESTPort = %d", got.RESTPort)
	}
	if got.RESTKeyEnv != "MY_REST_KEY" {
		t.Errorf("RESTKeyEnv = %q", got.RESTKeyEnv)
	}
	// Single-provider fields should synthesize a Providers list.
	if len(got.Providers) != 1 {
		t.Fatalf("Providers len = %d, want 1", len(got.Providers))
	}
	if got.Providers[0].Name != "default" {
		t.Errorf("Providers[0].Name = %q", got.Providers[0].Name)
	}
}

func TestParseJSONSummaryMultiProvider(t *testing.T) {
	text := "```json\n" + `{
		"user_name": "Alice",
		"agent_name": "Atlas",
		"personality": "concise",
		"work_domain": "engineering",
		"pii_mode": "warn",
		"provider_type": "openai",
		"provider_base_url": "https://api.openai.com/v1",
		"provider_api_key_env": "OPENAI_API_KEY",
		"provider_model": "gpt-4o",
		"providers": [
			{"name": "default", "type": "openai", "base_url": "https://api.openai.com/v1", "api_key_env": "OPENAI_API_KEY", "model": "gpt-4o"},
			{"name": "fallback", "type": "ollama", "base_url": "http://localhost:11434/v1", "model": "llama3"}
		],
		"rest_enabled": true,
		"rest_port": 8080
	}` + "\n```"

	got := parseJSONSummary(text)
	if got == nil {
		t.Fatal("expected non-nil summary")
	}
	if len(got.Providers) != 2 {
		t.Fatalf("Providers len = %d, want 2", len(got.Providers))
	}
	if got.Providers[0].Name != "default" {
		t.Errorf("Providers[0].Name = %q", got.Providers[0].Name)
	}
	if got.Providers[1].Name != "fallback" {
		t.Errorf("Providers[1].Name = %q", got.Providers[1].Name)
	}
	if got.Providers[1].Type != "ollama" {
		t.Errorf("Providers[1].Type = %q", got.Providers[1].Type)
	}
}

func TestParseJSONSummaryBackwardCompat(t *testing.T) {
	// Old-style summary with only 4 fields — defaults should fill the rest.
	text := "```json\n{\"user_name\": \"Bob\", \"personality\": \"concise\", \"work_domain\": \"devops\", \"pii_mode\": \"strict\"}\n```"

	got := parseJSONSummary(text)
	if got == nil {
		t.Fatal("expected non-nil summary")
	}
	if got.UserName != "Bob" {
		t.Errorf("UserName = %q", got.UserName)
	}
	if got.AgentName != "GoGoClaw Assistant" {
		t.Errorf("AgentName = %q, want default", got.AgentName)
	}
	if got.ProviderType != "openai_compatible" {
		t.Errorf("ProviderType = %q, want default openai_compatible", got.ProviderType)
	}
	if got.ProviderModel != "gpt-4o-mini" {
		t.Errorf("ProviderModel = %q, want default", got.ProviderModel)
	}
	if got.RESTPort != 8080 {
		t.Errorf("RESTPort = %d, want default 8080", got.RESTPort)
	}
	if got.RESTKeyEnv != "GOGOCLAW_REST_API_KEY" {
		t.Errorf("RESTKeyEnv = %q, want default GOGOCLAW_REST_API_KEY", got.RESTKeyEnv)
	}
	// Should synthesize a single-provider list.
	if len(got.Providers) != 1 {
		t.Fatalf("Providers len = %d, want 1", len(got.Providers))
	}
}

func TestParseJSONSummaryNoBlock(t *testing.T) {
	got := parseJSONSummary("What is your name?")
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestParseJSONSummaryInvalidJSON(t *testing.T) {
	got := parseJSONSummary("```json\n{invalid}\n```")
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestParseJSONSummaryEmptyName(t *testing.T) {
	got := parseJSONSummary("```json\n{\"user_name\": \"\", \"pii_mode\": \"disabled\"}\n```")
	if got != nil {
		t.Errorf("expected nil for empty user_name, got %+v", got)
	}
}

func TestApplyDefaults(t *testing.T) {
	tests := []struct {
		name    string
		input   BootstrapSummary
		checkFn func(t *testing.T, s *BootstrapSummary)
	}{
		{
			name:  "openai_defaults",
			input: BootstrapSummary{UserName: "X", ProviderType: "openai"},
			checkFn: func(t *testing.T, s *BootstrapSummary) {
				if s.ProviderBaseURL != "https://api.openai.com/v1" {
					t.Errorf("ProviderBaseURL = %q", s.ProviderBaseURL)
				}
				if s.ProviderKeyEnv != "OPENAI_API_KEY" {
					t.Errorf("ProviderKeyEnv = %q", s.ProviderKeyEnv)
				}
				if s.ProviderModel != "gpt-4o-mini" {
					t.Errorf("ProviderModel = %q", s.ProviderModel)
				}
			},
		},
		{
			name:  "ollama_defaults",
			input: BootstrapSummary{UserName: "X", ProviderType: "ollama"},
			checkFn: func(t *testing.T, s *BootstrapSummary) {
				if s.ProviderBaseURL != "http://localhost:11434/v1" {
					t.Errorf("ProviderBaseURL = %q", s.ProviderBaseURL)
				}
				if s.ProviderKeyEnv != "" {
					t.Errorf("ProviderKeyEnv = %q, want empty for ollama", s.ProviderKeyEnv)
				}
				if s.ProviderModel != "llama3" {
					t.Errorf("ProviderModel = %q", s.ProviderModel)
				}
			},
		},
		{
			name:  "rest_port_default",
			input: BootstrapSummary{UserName: "X"},
			checkFn: func(t *testing.T, s *BootstrapSummary) {
				if s.RESTPort != 8080 {
					t.Errorf("RESTPort = %d, want 8080", s.RESTPort)
				}
			},
		},
		{
			name:  "rest_key_env_default",
			input: BootstrapSummary{UserName: "X"},
			checkFn: func(t *testing.T, s *BootstrapSummary) {
				if s.RESTKeyEnv != "GOGOCLAW_REST_API_KEY" {
					t.Errorf("RESTKeyEnv = %q, want GOGOCLAW_REST_API_KEY", s.RESTKeyEnv)
				}
			},
		},
		{
			name:  "no_overwrite_existing",
			input: BootstrapSummary{UserName: "X", ProviderType: "openai", ProviderBaseURL: "https://custom.example.com/v1", ProviderModel: "custom-model", RESTPort: 9999, RESTKeyEnv: "CUSTOM_KEY"},
			checkFn: func(t *testing.T, s *BootstrapSummary) {
				if s.ProviderBaseURL != "https://custom.example.com/v1" {
					t.Errorf("should not overwrite existing ProviderBaseURL")
				}
				if s.ProviderModel != "custom-model" {
					t.Errorf("should not overwrite existing ProviderModel")
				}
				if s.RESTPort != 9999 {
					t.Errorf("should not overwrite existing RESTPort")
				}
				if s.RESTKeyEnv != "CUSTOM_KEY" {
					t.Errorf("should not overwrite existing RESTKeyEnv")
				}
			},
		},
		{
			name: "synthesize_providers_from_single",
			input: BootstrapSummary{
				UserName:     "X",
				ProviderType: "openai",
			},
			checkFn: func(t *testing.T, s *BootstrapSummary) {
				if len(s.Providers) != 1 {
					t.Fatalf("Providers len = %d, want 1", len(s.Providers))
				}
				if s.Providers[0].Name != "default" {
					t.Errorf("Providers[0].Name = %q, want default", s.Providers[0].Name)
				}
				if s.Providers[0].Type != "openai" {
					t.Errorf("Providers[0].Type = %q, want openai", s.Providers[0].Type)
				}
			},
		},
		{
			name: "multi_provider_defaults",
			input: BootstrapSummary{
				UserName: "X",
				Providers: []ProviderSummary{
					{Name: "primary", Type: "openai"},
					{Name: "local", Type: "ollama"},
				},
			},
			checkFn: func(t *testing.T, s *BootstrapSummary) {
				if len(s.Providers) != 2 {
					t.Fatalf("Providers len = %d, want 2", len(s.Providers))
				}
				if s.Providers[0].BaseURL != "https://api.openai.com/v1" {
					t.Errorf("Providers[0].BaseURL = %q", s.Providers[0].BaseURL)
				}
				if s.Providers[0].KeyEnv != "OPENAI_API_KEY" {
					t.Errorf("Providers[0].KeyEnv = %q", s.Providers[0].KeyEnv)
				}
				if s.Providers[1].BaseURL != "http://localhost:11434/v1" {
					t.Errorf("Providers[1].BaseURL = %q", s.Providers[1].BaseURL)
				}
				if s.Providers[1].Model != "llama3" {
					t.Errorf("Providers[1].Model = %q", s.Providers[1].Model)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := tt.input
			s.applyDefaults()
			tt.checkFn(t, &s)
		})
	}
}

func TestBootstrapIdentityWithMockEngine(t *testing.T) {
	templatesDir := t.TempDir()

	// Write bootstrap template.
	os.WriteFile(filepath.Join(templatesDir, "bootstrap.md"), []byte("Ask setup questions."), 0644)

	jsonSummary := "Here is your config:\n```json\n" +
		`{"user_name":"Bob","agent_name":"Assistant","personality":"concise","work_domain":"devops","pii_mode":"strict","provider_type":"ollama","provider_base_url":"http://localhost:11434/v1","provider_model":"llama3","rest_enabled":true,"rest_port":8080}` +
		"\n```\nPlease set these env vars. Ready? (y/n)"

	sender := &mockSender{
		responses: []string{
			"Welcome! What is your name?",
			"Nice, Bob! What should your assistant be called?",
			"What tone?",
			"What's your domain?",
			"Which provider?",
			"Model preference?",
			"Any fallback?",
			"PII mode?",
			"Telegram?",
			"REST?",
			"REST API key env?",
			jsonSummary,
		},
	}

	// Last line "y" confirms the setup.
	scanner := bufio.NewScanner(strings.NewReader("Bob\nAssistant\nconcise\ndevops\nollama\nllama3\nno\nstrict\nno\nyes, 8080\ndefault\ny\n"))
	stdout := &bytes.Buffer{}

	summary, err := bootstrapIdentity(context.Background(), sender, templatesDir, scanner, stdout)
	if err != nil {
		t.Fatalf("bootstrapIdentity: %v", err)
	}

	if summary.UserName != "Bob" {
		t.Errorf("UserName = %q, want Bob", summary.UserName)
	}
	if summary.ProviderType != "ollama" {
		t.Errorf("ProviderType = %q, want ollama", summary.ProviderType)
	}
	if summary.PIIMode != "strict" {
		t.Errorf("PIIMode = %q, want strict", summary.PIIMode)
	}
}

func TestBootstrapIdentityCancelled(t *testing.T) {
	templatesDir := t.TempDir()
	os.WriteFile(filepath.Join(templatesDir, "bootstrap.md"), []byte("Ask setup questions."), 0644)

	jsonSummary := "```json\n" +
		`{"user_name":"Bob","agent_name":"Assistant","personality":"concise","work_domain":"devops","pii_mode":"strict","provider_type":"ollama","provider_base_url":"http://localhost:11434/v1","provider_model":"llama3","rest_enabled":true,"rest_port":8080}` +
		"\n```\nReady? (y/n)"

	sender := &mockSender{
		responses: []string{jsonSummary},
	}

	scanner := bufio.NewScanner(strings.NewReader("n\n"))
	stdout := &bytes.Buffer{}

	_, err := bootstrapIdentity(context.Background(), sender, templatesDir, scanner, stdout)
	if err == nil {
		t.Fatal("expected error for cancelled setup")
	}
	if err != ErrSetupCancelled {
		t.Errorf("expected ErrSetupCancelled, got: %v", err)
	}
	if !strings.Contains(stdout.String(), "Setup cancelled") {
		t.Errorf("expected cancellation message in stdout, got: %s", stdout.String())
	}
}

func TestWriteBootstrapResultsFull(t *testing.T) {
	configDir := t.TempDir()
	os.MkdirAll(filepath.Join(configDir, "agents"), 0755)
	os.MkdirAll(filepath.Join(configDir, "providers"), 0755)
	os.MkdirAll(filepath.Join(configDir, "channels"), 0755)

	summary := &BootstrapSummary{
		UserName:        "Alice",
		AgentName:       "Jarvis",
		Personality:     "professional",
		WorkDomain:      "security",
		PIIMode:         "strict",
		ProviderType:    "openai",
		ProviderBaseURL: "https://api.openai.com/v1",
		ProviderKeyEnv:  "OPENAI_API_KEY",
		ProviderModel:   "gpt-4o",
		TelegramEnabled: true,
		TelegramToken:   "MY_TG_TOKEN",
		RESTEnabled:     true,
		RESTPort:        9090,
		RESTKeyEnv:      "MY_REST_KEY",
		Providers: []ProviderSummary{
			{Name: "default", Type: "openai", BaseURL: "https://api.openai.com/v1", KeyEnv: "OPENAI_API_KEY", Model: "gpt-4o"},
		},
	}

	err := writeBootstrapResults(configDir, summary)
	if err != nil {
		t.Fatalf("writeBootstrapResults: %v", err)
	}

	// Check user.md.
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

	// Check provider yaml — now written as providers/default.yaml.
	data, err = os.ReadFile(filepath.Join(configDir, "providers", "default.yaml"))
	if err != nil {
		t.Fatalf("providers/default.yaml not created: %v", err)
	}
	provStr := string(data)
	if !strings.Contains(provStr, "openai_compatible") {
		t.Error("provider type should be openai_compatible")
	}
	if !strings.Contains(provStr, "api.openai.com") {
		t.Error("provider should contain openai base_url")
	}
	if !strings.Contains(provStr, "${OPENAI_API_KEY}") {
		t.Error("provider should reference env var with ${} syntax")
	}
	if !strings.Contains(provStr, "gpt-4o") {
		t.Error("provider should contain model name")
	}

	// Check agent base.yaml.
	data, err = os.ReadFile(filepath.Join(configDir, "agents", "base.yaml"))
	if err != nil {
		t.Fatalf("agents/base.yaml not created: %v", err)
	}
	agentStr := string(data)
	if !strings.Contains(agentStr, "Jarvis") {
		t.Error("agent name should be Jarvis")
	}
	if !strings.Contains(agentStr, `mode: "strict"`) {
		t.Errorf("agent should have pii mode strict, got: %s", agentStr)
	}
	if !strings.Contains(agentStr, "base.md") {
		t.Error("agent should reference base.md system prompt")
	}
	if !strings.Contains(agentStr, `provider: "default"`) {
		t.Error("agent should reference default provider")
	}

	// Check REST channel.
	data, err = os.ReadFile(filepath.Join(configDir, "channels", "rest.yaml"))
	if err != nil {
		t.Fatalf("channels/rest.yaml not created: %v", err)
	}
	restStr := string(data)
	if !strings.Contains(restStr, "enabled: true") {
		t.Error("REST should be enabled")
	}
	if !strings.Contains(restStr, "9090") {
		t.Error("REST should use port 9090")
	}
	if !strings.Contains(restStr, "MY_REST_KEY") {
		t.Errorf("REST should use custom api_key_env, got: %s", restStr)
	}

	// Check Telegram channel.
	data, err = os.ReadFile(filepath.Join(configDir, "channels", "telegram.yaml"))
	if err != nil {
		t.Fatalf("channels/telegram.yaml not created: %v", err)
	}
	tgStr := string(data)
	if !strings.Contains(tgStr, "enabled: true") {
		t.Error("Telegram should be enabled")
	}
	if !strings.Contains(tgStr, "MY_TG_TOKEN") {
		t.Error("Telegram should use custom token env")
	}

	// Check network.yaml.
	data, err = os.ReadFile(filepath.Join(configDir, "network.yaml"))
	if err != nil {
		t.Fatalf("network.yaml not created: %v", err)
	}
	netStr := string(data)
	if !strings.Contains(netStr, "localhost") {
		t.Error("network should include localhost")
	}
	if !strings.Contains(netStr, "api.openai.com") {
		t.Error("network should include provider domain")
	}
}

func TestWriteBootstrapResultsMultiProvider(t *testing.T) {
	configDir := t.TempDir()
	os.MkdirAll(filepath.Join(configDir, "agents"), 0755)
	os.MkdirAll(filepath.Join(configDir, "providers"), 0755)
	os.MkdirAll(filepath.Join(configDir, "channels"), 0755)

	summary := &BootstrapSummary{
		UserName:    "Alice",
		AgentName:   "Atlas",
		Personality: "concise",
		WorkDomain:  "engineering",
		PIIMode:     "warn",
		RESTEnabled: true,
		RESTPort:    8080,
		Providers: []ProviderSummary{
			{Name: "default", Type: "openai", BaseURL: "https://api.openai.com/v1", KeyEnv: "OPENAI_API_KEY", Model: "gpt-4o"},
			{Name: "fallback", Type: "ollama", BaseURL: "http://localhost:11434/v1", Model: "llama3"},
		},
	}
	summary.applyDefaults()

	err := writeBootstrapResults(configDir, summary)
	if err != nil {
		t.Fatalf("writeBootstrapResults: %v", err)
	}

	// Check both provider files exist.
	data, err := os.ReadFile(filepath.Join(configDir, "providers", "default.yaml"))
	if err != nil {
		t.Fatalf("providers/default.yaml not created: %v", err)
	}
	if !strings.Contains(string(data), "gpt-4o") {
		t.Error("default provider should have gpt-4o model")
	}

	data, err = os.ReadFile(filepath.Join(configDir, "providers", "fallback.yaml"))
	if err != nil {
		t.Fatalf("providers/fallback.yaml not created: %v", err)
	}
	fbStr := string(data)
	if !strings.Contains(fbStr, "llama3") {
		t.Error("fallback provider should have llama3 model")
	}
	if strings.Contains(fbStr, "api_key") {
		t.Error("ollama fallback provider should not have api_key line")
	}

	// Check base.yaml has both providers in chain.
	data, err = os.ReadFile(filepath.Join(configDir, "agents", "base.yaml"))
	if err != nil {
		t.Fatalf("agents/base.yaml not created: %v", err)
	}
	agentStr := string(data)
	if !strings.Contains(agentStr, `provider: "default"`) {
		t.Error("agent should reference default provider")
	}
	if !strings.Contains(agentStr, `provider: "fallback"`) {
		t.Error("agent should reference fallback provider")
	}

	// Check network.yaml includes both provider domains.
	data, err = os.ReadFile(filepath.Join(configDir, "network.yaml"))
	if err != nil {
		t.Fatalf("network.yaml not created: %v", err)
	}
	netStr := string(data)
	if !strings.Contains(netStr, "api.openai.com") {
		t.Error("network should include openai domain")
	}
	// localhost is already in default list, so it shouldn't be duplicated.
	count := strings.Count(netStr, "localhost")
	if count != 1 {
		t.Errorf("localhost appears %d times, want exactly 1", count)
	}
}

func TestWriteBootstrapResultsOllama(t *testing.T) {
	configDir := t.TempDir()
	os.MkdirAll(filepath.Join(configDir, "agents"), 0755)
	os.MkdirAll(filepath.Join(configDir, "providers"), 0755)
	os.MkdirAll(filepath.Join(configDir, "channels"), 0755)

	summary := &BootstrapSummary{
		UserName:        "Bob",
		AgentName:       "GoGoClaw Assistant",
		Personality:     "casual",
		WorkDomain:      "general",
		PIIMode:         "disabled",
		ProviderType:    "ollama",
		ProviderBaseURL: "http://localhost:11434/v1",
		ProviderModel:   "llama3",
		RESTEnabled:     true,
		RESTPort:        8080,
	}
	summary.applyDefaults()

	err := writeBootstrapResults(configDir, summary)
	if err != nil {
		t.Fatalf("writeBootstrapResults: %v", err)
	}

	// Provider should not have api_key line for ollama.
	data, _ := os.ReadFile(filepath.Join(configDir, "providers", "default.yaml"))
	provStr := string(data)
	if strings.Contains(provStr, "api_key") {
		t.Error("ollama provider should not have api_key line")
	}

	// Network should not duplicate localhost.
	data, _ = os.ReadFile(filepath.Join(configDir, "network.yaml"))
	netStr := string(data)
	count := strings.Count(netStr, "localhost")
	if count != 1 {
		t.Errorf("localhost appears %d times, want exactly 1", count)
	}

	// REST channel should use default key env.
	data, _ = os.ReadFile(filepath.Join(configDir, "channels", "rest.yaml"))
	if !strings.Contains(string(data), "GOGOCLAW_REST_API_KEY") {
		t.Error("REST channel should use default GOGOCLAW_REST_API_KEY")
	}
}

func TestExtractDomain(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://api.openai.com/v1", "api.openai.com"},
		{"http://localhost:11434/v1", "localhost"},
		{"http://192.168.1.100:8080/v1", "192.168.1.100"},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractDomain(tt.url)
		if got != tt.want {
			t.Errorf("extractDomain(%q) = %q, want %q", tt.url, got, tt.want)
		}
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

func TestApplyProviderDefaults(t *testing.T) {
	p := ProviderSummary{Type: "openai"}
	applyProviderDefaults(&p)

	if p.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("BaseURL = %q", p.BaseURL)
	}
	if p.KeyEnv != "OPENAI_API_KEY" {
		t.Errorf("KeyEnv = %q", p.KeyEnv)
	}
	if p.Model != "gpt-4o-mini" {
		t.Errorf("Model = %q", p.Model)
	}
	if p.Name != "default" {
		t.Errorf("Name = %q, want default", p.Name)
	}
}

func TestContainsStr(t *testing.T) {
	ss := []string{"a", "b", "c"}
	if !containsStr(ss, "b") {
		t.Error("should find b")
	}
	if containsStr(ss, "d") {
		t.Error("should not find d")
	}
}

func TestRequiredEnvVars(t *testing.T) {
	s := &BootstrapSummary{
		TelegramEnabled: true,
		TelegramToken:   "MY_TG_TOKEN",
		RESTEnabled:     true,
		RESTKeyEnv:      "MY_REST_KEY",
		Providers: []ProviderSummary{
			{Name: "default", Type: "openai", KeyEnv: "OPENAI_API_KEY"},
			{Name: "fallback", Type: "ollama"}, // no key
		},
	}

	vars := requiredEnvVars(s)
	if len(vars) != 3 {
		t.Fatalf("requiredEnvVars len = %d, want 3", len(vars))
	}

	names := make(map[string]bool)
	for _, v := range vars {
		names[v.Name] = true
	}
	for _, want := range []string{"OPENAI_API_KEY", "MY_TG_TOKEN", "MY_REST_KEY"} {
		if !names[want] {
			t.Errorf("missing env var %s", want)
		}
	}
}

func TestRequiredEnvVarsDedup(t *testing.T) {
	// Two providers with same key env should only appear once.
	s := &BootstrapSummary{
		Providers: []ProviderSummary{
			{Name: "a", KeyEnv: "SAME_KEY"},
			{Name: "b", KeyEnv: "SAME_KEY"},
		},
	}
	vars := requiredEnvVars(s)
	if len(vars) != 1 {
		t.Errorf("requiredEnvVars len = %d, want 1 (dedup)", len(vars))
	}
}

func TestRequiredEnvVarsEmpty(t *testing.T) {
	// Ollama with no telegram, no REST key — should have no vars.
	s := &BootstrapSummary{
		Providers: []ProviderSummary{
			{Name: "default", Type: "ollama"},
		},
	}
	vars := requiredEnvVars(s)
	if len(vars) != 0 {
		t.Errorf("requiredEnvVars len = %d, want 0", len(vars))
	}
}

func TestCollectAndSetEnvVarsProvideValues(t *testing.T) {
	// Use unique env var names to avoid test pollution.
	varName := "GOGOCLAW_TEST_COLLECT_" + fmt.Sprintf("%d", os.Getpid())
	defer os.Unsetenv(varName)

	summary := &BootstrapSummary{
		Providers: []ProviderSummary{
			{Name: "default", Type: "openai", KeyEnv: varName},
		},
	}

	scanner := bufio.NewScanner(strings.NewReader("test-secret-value\n"))
	stdout := &bytes.Buffer{}

	err := collectAndSetEnvVars(summary, scanner, stdout)
	if err != nil {
		t.Fatalf("collectAndSetEnvVars: %v", err)
	}

	// Verify env var was set in current process.
	got := os.Getenv(varName)
	if got != "test-secret-value" {
		t.Errorf("os.Getenv(%s) = %q, want %q", varName, got, "test-secret-value")
	}

	// Verify the actual value is NOT in stdout (security).
	if strings.Contains(stdout.String(), "test-secret-value") {
		t.Error("stdout should not contain the actual secret value")
	}

	// Verify confirmation message is in stdout.
	if !strings.Contains(stdout.String(), varName+" ✓") {
		t.Errorf("stdout should contain confirmation, got: %s", stdout.String())
	}
}

func TestCollectAndSetEnvVarsSkip(t *testing.T) {
	varName := "GOGOCLAW_TEST_SKIP_" + fmt.Sprintf("%d", os.Getpid())
	os.Unsetenv(varName) // ensure clean
	defer os.Unsetenv(varName)

	summary := &BootstrapSummary{
		Providers: []ProviderSummary{
			{Name: "default", Type: "openai", KeyEnv: varName},
		},
	}

	// Empty line = skip.
	scanner := bufio.NewScanner(strings.NewReader("\n"))
	stdout := &bytes.Buffer{}

	err := collectAndSetEnvVars(summary, scanner, stdout)
	if err != nil {
		t.Fatalf("collectAndSetEnvVars: %v", err)
	}

	// Env var should NOT be set.
	got := os.Getenv(varName)
	if got != "" {
		t.Errorf("os.Getenv(%s) = %q, want empty (skipped)", varName, got)
	}

	// Should see skip message.
	if !strings.Contains(stdout.String(), "Skipped "+varName) {
		t.Errorf("stdout should contain skip message, got: %s", stdout.String())
	}

	// Should see the "set them manually" reminder.
	if !strings.Contains(stdout.String(), "skipped any") {
		t.Errorf("stdout should contain manual-set reminder, got: %s", stdout.String())
	}
}

func TestCollectAndSetEnvVarsNoVars(t *testing.T) {
	// Summary with no env vars needed (ollama, no telegram, no REST).
	summary := &BootstrapSummary{
		Providers: []ProviderSummary{
			{Name: "default", Type: "ollama"},
		},
	}

	scanner := bufio.NewScanner(strings.NewReader(""))
	stdout := &bytes.Buffer{}

	err := collectAndSetEnvVars(summary, scanner, stdout)
	if err != nil {
		t.Fatalf("collectAndSetEnvVars: %v", err)
	}

	// Should produce no output when no vars needed.
	if stdout.Len() != 0 {
		t.Errorf("expected empty stdout for no env vars, got: %s", stdout.String())
	}
}

func TestAtomicWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	// Write new file.
	if err := atomicWriteFile(path, []byte("hello\n"), 0644); err != nil {
		t.Fatalf("atomicWriteFile (new): %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "hello\n" {
		t.Errorf("content = %q, want %q", string(data), "hello\n")
	}

	// Overwrite existing file.
	if err := atomicWriteFile(path, []byte("world\n"), 0644); err != nil {
		t.Fatalf("atomicWriteFile (overwrite): %v", err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "world\n" {
		t.Errorf("overwrite content = %q, want %q", string(data), "world\n")
	}
}

func TestWriteIdentityYAML(t *testing.T) {
	configDir := t.TempDir()
	os.MkdirAll(filepath.Join(configDir, "agents"), 0755)

	summary := &BootstrapSummary{
		UserName:    "Scott",
		AgentName:   "Jim",
		Personality: "casual",
		WorkDomain:  "financial services",
		PIIMode:     "permissive",
	}

	err := writeIdentityYAML(configDir, summary)
	if err != nil {
		t.Fatalf("writeIdentityYAML: %v", err)
	}

	id, err := LoadIdentity(configDir)
	if err != nil {
		t.Fatalf("LoadIdentity: %v", err)
	}
	if id == nil {
		t.Fatal("LoadIdentity returned nil")
	}
	if id.UserName != "Scott" {
		t.Errorf("UserName = %q, want Scott", id.UserName)
	}
	if id.AgentName != "Jim" {
		t.Errorf("AgentName = %q, want Jim", id.AgentName)
	}
	if id.Personality != "casual" {
		t.Errorf("Personality = %q, want casual", id.Personality)
	}
	if id.BootstrapVersion != 1 {
		t.Errorf("BootstrapVersion = %d, want 1", id.BootstrapVersion)
	}
	if id.BootstrappedAt == "" {
		t.Error("BootstrappedAt should not be empty")
	}
}

func TestLoadIdentityMissing(t *testing.T) {
	dir := t.TempDir()
	id, err := LoadIdentity(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != nil {
		t.Errorf("expected nil for missing identity.yaml, got %+v", id)
	}
}

func TestWriteBootstrapResultsIdentityYAML(t *testing.T) {
	configDir := t.TempDir()
	os.MkdirAll(filepath.Join(configDir, "agents"), 0755)
	os.MkdirAll(filepath.Join(configDir, "providers"), 0755)
	os.MkdirAll(filepath.Join(configDir, "channels"), 0755)

	summary := &BootstrapSummary{
		UserName:     "Alice",
		AgentName:    "Atlas",
		Personality:  "concise",
		WorkDomain:   "engineering",
		PIIMode:      "warn",
		ProviderType: "ollama",
		RESTEnabled:  true,
		RESTPort:     8080,
	}
	summary.applyDefaults()

	err := writeBootstrapResults(configDir, summary)
	if err != nil {
		t.Fatalf("writeBootstrapResults: %v", err)
	}

	// identity.yaml should exist alongside user.md.
	id, err := LoadIdentity(configDir)
	if err != nil {
		t.Fatalf("LoadIdentity: %v", err)
	}
	if id == nil {
		t.Fatal("identity.yaml not created")
	}
	if id.UserName != "Alice" {
		t.Errorf("identity UserName = %q", id.UserName)
	}
	if id.AgentName != "Atlas" {
		t.Errorf("identity AgentName = %q", id.AgentName)
	}
}

func TestLoadEnvFile(t *testing.T) {
	dir := t.TempDir()

	// Write an env file.
	envContent := "TEST_LOAD_A=hello\nTEST_LOAD_B=world\n"
	os.WriteFile(filepath.Join(dir, "env"), []byte(envContent), 0600)

	defer os.Unsetenv("TEST_LOAD_A")
	defer os.Unsetenv("TEST_LOAD_B")

	LoadEnvFile(dir)

	if got := os.Getenv("TEST_LOAD_A"); got != "hello" {
		t.Errorf("TEST_LOAD_A = %q, want hello", got)
	}
	if got := os.Getenv("TEST_LOAD_B"); got != "world" {
		t.Errorf("TEST_LOAD_B = %q, want world", got)
	}
}

func TestLoadEnvFileMissing(t *testing.T) {
	// Should not panic or error when file doesn't exist.
	LoadEnvFile(t.TempDir())
}

func TestBootstrapDirsIncludesMCP(t *testing.T) {
	found := false
	for _, d := range bootstrapDirs {
		if d == "mcp" {
			found = true
			break
		}
	}
	if !found {
		t.Error("bootstrapDirs should include 'mcp'")
	}
}

func TestRelevanceThreshold(t *testing.T) {
	configDir := t.TempDir()
	os.MkdirAll(filepath.Join(configDir, "agents"), 0755)
	os.MkdirAll(filepath.Join(configDir, "providers"), 0755)
	os.MkdirAll(filepath.Join(configDir, "channels"), 0755)

	summary := &BootstrapSummary{
		UserName:     "X",
		ProviderType: "ollama",
		RESTEnabled:  true,
		RESTPort:     8080,
	}
	summary.applyDefaults()

	writeBootstrapResults(configDir, summary)

	data, _ := os.ReadFile(filepath.Join(configDir, "agents", "base.yaml"))
	if !strings.Contains(string(data), "relevance_threshold: 0.3") {
		t.Errorf("base.yaml should have relevance_threshold 0.3, got:\n%s", string(data))
	}
}
