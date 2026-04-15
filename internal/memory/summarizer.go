package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
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
	Summary          string             // the summary text
	RemainingHistory []provider.Message // messages that were kept (not summarized)
	FactsExtracted   []string           // facts extracted and saved to memory
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
		switch {
		case msg.Role == "tool":
			continue // tool results captured via condensed assistant tool calls
		case msg.Role == "assistant" && len(msg.ToolCalls) > 0:
			condensed := condensedToolCalls(msg.ToolCalls)
			if condensed == "" {
				// All tool calls condensed to empty — format as normal assistant message.
				b.WriteString(fmt.Sprintf("assistant: %s\n", msg.Content))
			} else if msg.Content != "" {
				b.WriteString(fmt.Sprintf("assistant: %s [Tools: %s]\n", msg.Content, condensed))
			} else {
				b.WriteString(fmt.Sprintf("assistant: [Tools: %s]\n", condensed))
			}
		default:
			b.WriteString(fmt.Sprintf("%s: %s\n", msg.Role, msg.Content))
		}
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

	// Key term overlap quality check — log missing technical terms.
	if missing := checkKeyTermOverlap(older, summary); len(missing) > 0 {
		if len(missing) > 5 {
			missing = missing[:5]
		}
		log.Printf("memory: summarization quality warning — missing key terms: %v", missing)
	}

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

// condensedToolCalls builds a compact string of tool calls for summarization.
// Example: "file_read(/etc/config.yaml); shell_exec(go test ./...)"
func condensedToolCalls(toolCalls []provider.ToolCall) string {
	if len(toolCalls) == 0 {
		return ""
	}
	var parts []string
	for _, tc := range toolCalls {
		desc := condenseToolCall(tc)
		if desc != "" {
			parts = append(parts, desc)
		}
	}
	return strings.Join(parts, "; ")
}

// condenseToolCall extracts tool name and key argument into a one-liner.
// Example: "file_read(/etc/config.yaml)" or "shell_exec(go test ./...)"
func condenseToolCall(tc provider.ToolCall) string {
	name := tc.Name
	if name == "" {
		return ""
	}
	keyArg := extractKeyArg(tc.Name, tc.Arguments)
	if keyArg != "" {
		if len(keyArg) > 80 {
			keyArg = keyArg[:77] + "..."
		}
		return fmt.Sprintf("%s(%s)", name, keyArg)
	}
	return name + "()"
}

// extractKeyArg pulls the most informative argument from a tool call's JSON.
// Uses a deterministic priority list per tool name, then a global fallback
// list for unknown tools. Never uses random map iteration.
func extractKeyArg(toolName string, args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(args, &parsed); err != nil {
		return ""
	}

	// Per-tool priority keys.
	priorityKeys := map[string][]string{
		"file_read":     {"path"},
		"file_write":    {"path"},
		"shell_exec":    {"command"},
		"memory_save":   {"content"},
		"memory_search": {"query"},
		"web_fetch":     {"url"},
		"think":         {"thought"},
	}

	if keys, ok := priorityKeys[toolName]; ok {
		for _, key := range keys {
			if val, exists := parsed[key]; exists {
				if s, ok := val.(string); ok && s != "" {
					return s
				}
			}
		}
	}

	// Deterministic fallback for unknown tools — fixed key order.
	fallbackKeys := []string{"path", "file", "command", "query", "url", "name", "content", "text"}
	for _, key := range fallbackKeys {
		if val, ok := parsed[key]; ok {
			if s, ok := val.(string); ok && s != "" {
				return s
			}
		}
	}

	return ""
}

// checkKeyTermOverlap extracts technical terms from the original conversation
// segment and checks that they appear in the summary. Returns missing terms
// in sorted order. This is a heuristic quality warning, not a gate.
func checkKeyTermOverlap(messages []provider.Message, summary string) []string {
	terms := extractKeyTerms(messages)
	if len(terms) == 0 {
		return nil
	}

	summaryLower := strings.ToLower(summary)
	var missing []string
	for _, term := range terms {
		if !strings.Contains(summaryLower, strings.ToLower(term)) {
			missing = append(missing, term)
		}
	}
	return missing
}

// extractKeyTerms identifies technical tokens from messages.
// Focuses on high-signal patterns: paths, dotted names, underscored
// identifiers, hyphenated names, and CamelCase words. Returns a
// sorted, deduplicated slice.
func extractKeyTerms(messages []provider.Message) []string {
	seen := make(map[string]int) // term -> occurrence count

	for _, msg := range messages {
		if msg.Content == "" {
			continue
		}

		words := strings.Fields(msg.Content)
		for _, word := range words {
			// Strip common punctuation from edges.
			clean := strings.Trim(word, ".,;:!?\"'`()[]{}—–")
			if clean == "" || len(clean) < 4 {
				continue
			}

			// Technical terms: contain /, ., _, or - (paths, packages, identifiers).
			if strings.ContainsAny(clean, "/._-") {
				seen[clean]++
				continue
			}

			// CamelCase: mixed case, not all-upper.
			if isCamelCase(clean) {
				seen[clean]++
			}
		}
	}

	// Keep terms appearing 2+ times, or 1+ for path-like terms (contain /).
	var terms []string
	for term, count := range seen {
		if count >= 2 || strings.Contains(term, "/") {
			terms = append(terms, term)
		}
	}
	sort.Strings(terms)
	return terms
}

// isCamelCase returns true if the word has mixed case (not all-upper, not
// all-lower) and starts with an uppercase letter.
func isCamelCase(s string) bool {
	if len(s) < 3 {
		return false
	}
	if s[0] < 'A' || s[0] > 'Z' {
		return false
	}
	hasLower := false
	hasUpper := false
	for _, r := range s[1:] {
		if r >= 'a' && r <= 'z' {
			hasLower = true
		}
		if r >= 'A' && r <= 'Z' {
			hasUpper = true
		}
	}
	return hasLower && hasUpper
}
