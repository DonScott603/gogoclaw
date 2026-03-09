package engine

import (
	"testing"

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
