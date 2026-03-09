package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/DonScott603/gogoclaw/internal/provider"
)

const defaultSummarizePrompt = `Summarize the following conversation segment concisely. Preserve key facts, decisions, user preferences, and important context. Be brief but complete.

Conversation segment:
%s

Respond with ONLY the summary, no preamble.`

// Summarizer handles rolling summarization of conversation history.
type Summarizer struct {
	provider        provider.Provider
	thresholdTokens int
	store           VectorStore
}

// NewSummarizer creates a Summarizer.
func NewSummarizer(p provider.Provider, thresholdTokens int, store VectorStore) *Summarizer {
	if thresholdTokens <= 0 {
		thresholdTokens = 3072
	}
	return &Summarizer{
		provider:        p,
		thresholdTokens: thresholdTokens,
		store:           store,
	}
}

// SummarizeResult holds the result of a summarization.
type SummarizeResult struct {
	Summary         string             // the summary text
	RemainingHistory []provider.Message // messages that were kept (not summarized)
	FactsExtracted  []string           // facts extracted and saved to memory
}

// MaybeSummarize checks if history exceeds the threshold and summarizes if so.
// Returns nil if no summarization was needed.
func (s *Summarizer) MaybeSummarize(ctx context.Context, history []provider.Message, conversationID string) (*SummarizeResult, error) {
	totalTokens := s.estimateHistoryTokens(history)
	if totalTokens <= s.thresholdTokens {
		return nil, nil
	}

	// Find the split point: summarize the older half.
	splitIdx := s.findSplitPoint(history, totalTokens)
	if splitIdx <= 0 {
		return nil, nil
	}

	older := history[:splitIdx]
	newer := history[splitIdx:]

	// Build the conversation text for summarization.
	var b strings.Builder
	for _, msg := range older {
		if msg.Role == "tool" {
			continue // skip tool messages in summary input
		}
		b.WriteString(fmt.Sprintf("%s: %s\n", msg.Role, msg.Content))
	}

	summaryPrompt := fmt.Sprintf(defaultSummarizePrompt, b.String())
	resp, err := s.provider.Chat(ctx, provider.ChatRequest{
		Messages: []provider.Message{
			{Role: "user", Content: summaryPrompt},
		},
		MaxTokens: 500,
	})
	if err != nil {
		return nil, fmt.Errorf("memory: summarize: %w", err)
	}

	summary := strings.TrimSpace(resp.Content)

	// Extract facts from the summarized segment and save to memory.
	facts := ExtractFacts(older)
	if s.store != nil && len(facts) > 0 {
		writer := NewWriter(s.store, "")
		writer.ExtractAndSave(ctx, conversationID, older)
	}

	// Build result: summary system message + remaining history.
	remaining := make([]provider.Message, 0, len(newer)+1)
	remaining = append(remaining, provider.Message{
		Role:    "system",
		Content: fmt.Sprintf("[Previous conversation summary]: %s", summary),
	})
	remaining = append(remaining, newer...)

	return &SummarizeResult{
		Summary:          summary,
		RemainingHistory: remaining,
		FactsExtracted:   facts,
	}, nil
}

func (s *Summarizer) findSplitPoint(history []provider.Message, totalTokens int) int {
	// Summarize roughly the first half by token count.
	target := totalTokens / 2
	running := 0
	for i, msg := range history {
		running += len(msg.Content) / 4
		if running >= target {
			// Don't split in the middle of a tool call sequence.
			// Find the next user message boundary.
			for j := i + 1; j < len(history); j++ {
				if history[j].Role == "user" {
					return j
				}
			}
			return i + 1
		}
	}
	return len(history) / 2
}

func (s *Summarizer) estimateHistoryTokens(history []provider.Message) int {
	total := 0
	for _, msg := range history {
		if s.provider != nil {
			n, err := s.provider.CountTokens(msg.Content)
			if err == nil {
				total += n
				continue
			}
		}
		total += len(msg.Content) / 4
	}
	return total
}
