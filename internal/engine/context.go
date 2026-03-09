package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/DonScott603/gogoclaw/internal/memory"
	"github.com/DonScott603/gogoclaw/internal/provider"
)

// ContextAssembler manages token budget calculation, history truncation,
// and memory injection into the system prompt.
type ContextAssembler struct {
	maxContextTokens int
	counter          provider.Provider // used for CountTokens
	memoryStore      memory.VectorStore
	memoryTopK       int
	memoryOpts       memory.SearchOptions
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

// SetMemoryStore configures memory retrieval for context assembly.
func (ca *ContextAssembler) SetMemoryStore(store memory.VectorStore, topK int, opts memory.SearchOptions) {
	ca.memoryStore = store
	ca.memoryTopK = topK
	if ca.memoryTopK <= 0 {
		ca.memoryTopK = 5
	}
	ca.memoryOpts = opts
}

// Assemble builds the final message list that fits within the token budget.
// It includes the system prompt (with injected memories), tool definitions
// overhead, and as much recent history as possible (newest first).
func (ca *ContextAssembler) Assemble(
	systemPrompt string,
	history []provider.Message,
	toolTokenOverhead int,
) []provider.Message {
	// Try to inject relevant memories into the system prompt.
	enrichedPrompt := ca.enrichWithMemories(systemPrompt, history)

	budget := ca.maxContextTokens

	// Reserve tokens for the system prompt.
	sysTokens := ca.estimateTokens(enrichedPrompt)
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
	if enrichedPrompt != "" {
		msgs = append(msgs, provider.Message{Role: "system", Content: enrichedPrompt})
	}
	msgs = append(msgs, included...)
	return msgs
}

// enrichWithMemories retrieves relevant memories and appends them to the system prompt.
func (ca *ContextAssembler) enrichWithMemories(systemPrompt string, history []provider.Message) string {
	if ca.memoryStore == nil || len(history) == 0 {
		return systemPrompt
	}

	// Build a query from the last few user messages.
	query := ca.buildMemoryQuery(history)
	if query == "" {
		return systemPrompt
	}

	ctx := context.Background()
	results, err := ca.memoryStore.Search(ctx, query, ca.memoryTopK, ca.memoryOpts)
	if err != nil || len(results) == 0 {
		return systemPrompt
	}

	var b strings.Builder
	b.WriteString(systemPrompt)
	b.WriteString("\n\n## Relevant memories\n\n")
	for _, r := range results {
		b.WriteString(fmt.Sprintf("- %s (relevance: %.0f%%)\n", r.Document.Content, r.Score*100))
	}

	return b.String()
}

func (ca *ContextAssembler) buildMemoryQuery(history []provider.Message) string {
	// Use the last 2 user messages as the query.
	var parts []string
	count := 0
	for i := len(history) - 1; i >= 0 && count < 2; i-- {
		if history[i].Role == "user" {
			parts = append([]string{history[i].Content}, parts...)
			count++
		}
	}
	return strings.Join(parts, " ")
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
