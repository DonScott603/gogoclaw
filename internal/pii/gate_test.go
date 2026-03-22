package pii

import (
	"context"
	"testing"

	"github.com/DonScott603/gogoclaw/internal/provider"
)

type mockProvider struct{}

func (m *mockProvider) Name() string { return "mock" }
func (m *mockProvider) Chat(_ context.Context, _ provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{Content: "ok"}, nil
}
func (m *mockProvider) ChatStream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 1)
	ch <- provider.StreamChunk{Content: "ok", Done: true}
	close(ch)
	return ch, nil
}
func (m *mockProvider) CountTokens(content string) (int, error) { return len(content) / 4, nil }
func (m *mockProvider) Healthy(_ context.Context) bool          { return true }

func TestGateDisabledMode(t *testing.T) {
	g := NewGate(&mockProvider{}, GateConfig{Mode: ModeDisabled})

	resp, err := g.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.Message{
			{Role: "user", Content: "My SSN is 123-45-6789"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("expected 'ok', got %q", resp.Content)
	}
}

func TestGateStrictModeBlocksCloud(t *testing.T) {
	g := NewGate(&mockProvider{}, GateConfig{Mode: ModeStrict, IsLocal: false})

	resp, err := g.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.Message{
			{Role: "user", Content: "My SSN is 123-45-6789"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content == "ok" {
		t.Error("strict mode should block PII to cloud provider")
	}
}

func TestGateStrictModeAllowsLocal(t *testing.T) {
	g := NewGate(&mockProvider{}, GateConfig{Mode: ModeStrict, IsLocal: true})

	resp, err := g.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.Message{
			{Role: "user", Content: "My SSN is 123-45-6789"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "ok" {
		t.Error("strict mode should allow PII to local provider")
	}
}

func TestGateWarnMode(t *testing.T) {
	warned := false
	g := NewGate(&mockProvider{}, GateConfig{
		Mode: ModeWarn,
		WarnFn: func(patterns []string, mode Mode) {
			warned = true
		},
	})

	resp, err := g.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.Message{
			{Role: "user", Content: "My SSN is 123-45-6789"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "ok" {
		t.Error("warn mode should proceed")
	}
	if !warned {
		t.Error("warn callback should have been called")
	}
}

func TestGatePermissiveMode(t *testing.T) {
	audited := false
	g := NewGate(&mockProvider{}, GateConfig{
		Mode: ModePermissive,
		AuditFn: func(patterns []string, mode Mode, action string) {
			audited = true
		},
	})

	resp, err := g.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.Message{
			{Role: "user", Content: "My SSN is 123-45-6789"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "ok" {
		t.Error("permissive mode should proceed")
	}
	if !audited {
		t.Error("audit callback should have been called")
	}
}

func TestGateConversationOverride(t *testing.T) {
	g := NewGate(&mockProvider{}, GateConfig{Mode: ModeStrict, IsLocal: false})

	// Override this conversation to disabled.
	g.SetConversationOverride("conv-1", ModeDisabled)

	err := g.CheckMessagesWithConversation(
		[]provider.Message{{Role: "user", Content: "SSN: 123-45-6789"}},
		"conv-1",
	)
	if err != nil {
		t.Errorf("overridden conversation should not block: %v", err)
	}

	// Different conversation should still block.
	err = g.CheckMessagesWithConversation(
		[]provider.Message{{Role: "user", Content: "SSN: 123-45-6789"}},
		"conv-2",
	)
	if err == nil {
		t.Error("non-overridden conversation should block in strict mode")
	}

	// Clear override.
	g.ClearConversationOverride("conv-1")
	err = g.CheckMessagesWithConversation(
		[]provider.Message{{Role: "user", Content: "SSN: 123-45-6789"}},
		"conv-1",
	)
	if err == nil {
		t.Error("after clearing override, conversation should block again")
	}
}

func TestGateNoPII(t *testing.T) {
	g := NewGate(&mockProvider{}, GateConfig{Mode: ModeStrict, IsLocal: false})

	resp, err := g.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.Message{
			{Role: "user", Content: "Hello, how are you?"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "ok" {
		t.Error("no PII should pass through in any mode")
	}
}

func TestGateStrictBlocksAPIKey(t *testing.T) {
	g := NewGate(&mockProvider{}, GateConfig{Mode: ModeStrict, IsLocal: false})

	resp, err := g.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.Message{
			{Role: "user", Content: "Use this key: sk-abc123def456ghi789jkl012mno345"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content == "ok" {
		t.Error("strict mode should block API keys sent to cloud provider")
	}
}

func TestGateWarnNotifiesOnSecret(t *testing.T) {
	warned := false
	var warnedPatterns []string
	g := NewGate(&mockProvider{}, GateConfig{
		Mode: ModeWarn,
		WarnFn: func(patterns []string, mode Mode) {
			warned = true
			warnedPatterns = patterns
		},
	})

	resp, err := g.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.Message{
			{Role: "user", Content: "Use this key: sk-abc123def456ghi789jkl012mno345"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "ok" {
		t.Error("warn mode should proceed")
	}
	if !warned {
		t.Error("warn callback should have been called for API key")
	}
	found := false
	for _, p := range warnedPatterns {
		if p == "api_key" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected api_key in warned patterns, got %v", warnedPatterns)
	}
}

func TestGateStrictBlocksToolMessageSecrets(t *testing.T) {
	g := NewGate(&mockProvider{}, GateConfig{Mode: ModeStrict, IsLocal: false})

	resp, err := g.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.Message{
			{Role: "user", Content: "Read my config file"},
			{Role: "tool", Content: "File contents: api_key = sk-abc123def456ghi789jkl012mno345"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content == "ok" {
		t.Error("strict mode should block API keys found in tool results sent to cloud provider")
	}
}

func TestGateStreamStrictBlocks(t *testing.T) {
	g := NewGate(&mockProvider{}, GateConfig{Mode: ModeStrict, IsLocal: false})

	ch, err := g.ChatStream(context.Background(), provider.ChatRequest{
		Messages: []provider.Message{
			{Role: "user", Content: "My SSN is 123-45-6789"},
		},
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	var content string
	for chunk := range ch {
		content += chunk.Content
	}
	if content == "ok" {
		t.Error("stream should be blocked in strict mode with PII")
	}
}
