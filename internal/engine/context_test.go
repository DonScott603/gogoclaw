package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/DonScott603/gogoclaw/internal/memory"
	"github.com/DonScott603/gogoclaw/internal/provider"
)

func TestContextAssemblerBasic(t *testing.T) {
	ca := NewContextAssembler(1000, nil)

	history := []provider.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
		{Role: "user", Content: "How are you?"},
		{Role: "assistant", Content: "I'm good, thanks!"},
	}

	msgs := ca.Assemble("You are a helpful assistant.", history, 0)

	// Should include system prompt + all history (small enough to fit).
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Errorf("first message role = %q, want %q", msgs[0].Role, "system")
	}
}

func TestContextAssemblerTruncation(t *testing.T) {
	// Very small budget to force truncation.
	ca := NewContextAssembler(100, nil)

	history := make([]provider.Message, 20)
	for i := range history {
		history[i] = provider.Message{
			Role:    "user",
			Content: "This is a reasonably long message that should consume tokens " + string(rune('A'+i)),
		}
	}

	msgs := ca.Assemble("System prompt", history, 0)

	// Should be truncated to fit budget — fewer messages than original.
	if len(msgs) >= 21 { // 20 history + 1 system
		t.Errorf("expected truncation, got %d messages", len(msgs))
	}
	// First message should still be system prompt.
	if len(msgs) > 0 && msgs[0].Role != "system" {
		t.Errorf("first message role = %q, want %q", msgs[0].Role, "system")
	}
}

func TestContextAssemblerEmptyHistory(t *testing.T) {
	ca := NewContextAssembler(8192, nil)
	msgs := ca.Assemble("System", nil, 0)
	if len(msgs) != 1 {
		t.Errorf("expected 1 message (system), got %d", len(msgs))
	}
}

func TestTokenBudgetInfo(t *testing.T) {
	ca := NewContextAssembler(8192, nil)
	total, used, available := ca.TokenBudgetInfo("System prompt here", 500, 200)
	if total != 8192 {
		t.Errorf("total = %d, want 8192", total)
	}
	if used <= 0 {
		t.Error("expected used > 0")
	}
	if available <= 0 {
		t.Error("expected available > 0")
	}
	if used+available != total {
		t.Errorf("used(%d) + available(%d) != total(%d)", used, available, total)
	}
}

// mockMemoryStore implements memory.VectorStore for context assembler tests.
type mockMemoryStore struct {
	docs []memory.MemoryDocument
}

func (m *mockMemoryStore) Store(_ context.Context, doc memory.MemoryDocument) error {
	m.docs = append(m.docs, doc)
	return nil
}

func (m *mockMemoryStore) Search(_ context.Context, _ string, topK int, _ memory.SearchOptions) ([]memory.MemoryResult, error) {
	var results []memory.MemoryResult
	for i, doc := range m.docs {
		if i >= topK {
			break
		}
		results = append(results, memory.MemoryResult{Document: doc, Similarity: 0.85, Score: 0.85})
	}
	return results, nil
}

func (m *mockMemoryStore) Delete(_ context.Context, _ string) error { return nil }
func (m *mockMemoryStore) Close() error                             { return nil }

func TestContextAssemblerMemoryInjection(t *testing.T) {
	store := &mockMemoryStore{
		docs: []memory.MemoryDocument{
			{ID: "m1", Content: "User prefers dark mode"},
			{ID: "m2", Content: "Project uses Go 1.22"},
		},
	}

	ca := NewContextAssembler(8192, nil)
	ca.SetMemoryStore(store, 5, memory.SearchOptions{MinSimilarity: 0.0})

	history := []provider.Message{
		{Role: "user", Content: "Tell me about the project settings"},
	}

	msgs := ca.Assemble("You are a helpful assistant.", history, 0)

	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Errorf("first message role = %q, want %q", msgs[0].Role, "system")
	}
	// The system prompt should contain the memory section.
	if !strings.Contains(msgs[0].Content, "Relevant memories") {
		t.Error("system prompt missing 'Relevant memories' section")
	}
	if !strings.Contains(msgs[0].Content, "dark mode") {
		t.Error("system prompt missing injected memory about dark mode")
	}
	if !strings.Contains(msgs[0].Content, "Go 1.22") {
		t.Error("system prompt missing injected memory about Go 1.22")
	}
}

func TestContextAssemblerNoMemoryStore(t *testing.T) {
	ca := NewContextAssembler(8192, nil)

	history := []provider.Message{
		{Role: "user", Content: "Hello"},
	}

	msgs := ca.Assemble("Base prompt.", history, 0)

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	// Should not have memory section when no store is configured.
	if strings.Contains(msgs[0].Content, "Relevant memories") {
		t.Error("system prompt should not have memories section when store is nil")
	}
}

func TestContextAssemblerEmptyHistoryNoMemoryQuery(t *testing.T) {
	store := &mockMemoryStore{
		docs: []memory.MemoryDocument{
			{ID: "m1", Content: "Some memory"},
		},
	}
	ca := NewContextAssembler(8192, nil)
	ca.SetMemoryStore(store, 5, memory.SearchOptions{MinSimilarity: 0.0})

	// Empty history — no user messages to build a query from.
	msgs := ca.Assemble("Base prompt.", nil, 0)

	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	// Should not inject memories when there's no history to query against.
	if strings.Contains(msgs[0].Content, "Relevant memories") {
		t.Error("should not inject memories with empty history")
	}
}
