package engine

import (
	"github.com/DonScott603/gogoclaw/internal/provider"
)

// ContextAssembler manages token budget calculation and history truncation.
type ContextAssembler struct {
	maxContextTokens int
	counter          provider.Provider // used for CountTokens
}

// NewContextAssembler creates a ContextAssembler with the given token limits.
func NewContextAssembler(maxContextTokens int, counter provider.Provider) *ContextAssembler {
	if maxContextTokens <= 0 {
		maxContextTokens = 8192 // sensible default
	}
	return &ContextAssembler{
		maxContextTokens: maxContextTokens,
		counter:          counter,
	}
}

// Assemble builds the final message list that fits within the token budget.
// It includes the system prompt, tool definitions overhead, and as much recent
// history as possible (newest first).
func (ca *ContextAssembler) Assemble(
	systemPrompt string,
	history []provider.Message,
	toolTokenOverhead int,
) []provider.Message {
	budget := ca.maxContextTokens

	// Reserve tokens for the system prompt.
	sysTokens := ca.estimateTokens(systemPrompt)
	budget -= sysTokens

	// Reserve tokens for tool definitions.
	budget -= toolTokenOverhead

	// Reserve some tokens for the model's response.
	responseReserve := 1024
	if budget > responseReserve {
		budget -= responseReserve
	}

	// Build history from newest to oldest until budget is exhausted.
	var included []provider.Message
	for i := len(history) - 1; i >= 0 && budget > 0; i-- {
		msg := history[i]
		tokens := ca.estimateTokens(msg.Content)
		if tokens > budget {
			break
		}
		budget -= tokens
		included = append([]provider.Message{msg}, included...)
	}

	// Assemble: system + truncated history.
	msgs := make([]provider.Message, 0, len(included)+1)
	if systemPrompt != "" {
		msgs = append(msgs, provider.Message{Role: "system", Content: systemPrompt})
	}
	msgs = append(msgs, included...)
	return msgs
}

// TokenBudgetInfo returns the current budget breakdown for display purposes.
func (ca *ContextAssembler) TokenBudgetInfo(systemPrompt string, historyTokens int, toolOverhead int) (total, used, available int) {
	total = ca.maxContextTokens
	sysTokens := ca.estimateTokens(systemPrompt)
	used = sysTokens + historyTokens + toolOverhead
	available = total - used
	if available < 0 {
		available = 0
	}
	return
}

func (ca *ContextAssembler) estimateTokens(content string) int {
	if ca.counter != nil {
		n, err := ca.counter.CountTokens(content)
		if err == nil {
			return n
		}
	}
	// Fallback: ~4 chars per token.
	return len(content) / 4
}
