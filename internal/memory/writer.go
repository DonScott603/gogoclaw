package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DonScott603/gogoclaw/internal/provider"
)

// Writer extracts facts from conversations and persists them
// both as vector embeddings and as human-readable daily memory files.
type Writer struct {
	store      VectorStore
	dailyDir   string // e.g., ~/.gogoclaw/memory/daily/
}

// NewWriter creates a memory Writer.
func NewWriter(store VectorStore, dailyDir string) *Writer {
	return &Writer{store: store, dailyDir: dailyDir}
}

// ExtractAndSave parses a conversation for facts, saves them to the vector store,
// and appends them to the daily memory file.
func (w *Writer) ExtractAndSave(ctx context.Context, conversationID string, messages []provider.Message) error {
	facts := ExtractFacts(messages)
	if len(facts) == 0 {
		return nil
	}

	now := time.Now()
	var saved []string

	for i, fact := range facts {
		doc := MemoryDocument{
			ID:        fmt.Sprintf("%s-fact-%d-%d", conversationID, now.UnixMilli(), i),
			Content:   fact,
			Tags:      []string{"auto-extracted", "conversation"},
			Timestamp: now,
			Source:    conversationID,
		}
		if err := w.store.Store(ctx, doc); err != nil {
			continue // skip individual failures
		}
		saved = append(saved, fact)
	}

	if w.dailyDir != "" && len(saved) > 0 {
		if err := w.appendDailyFile(now, conversationID, saved); err != nil {
			return fmt.Errorf("memory: write daily file: %w", err)
		}
	}

	return nil
}

func (w *Writer) appendDailyFile(now time.Time, conversationID string, facts []string) error {
	if err := os.MkdirAll(w.dailyDir, 0o755); err != nil {
		return err
	}

	filename := filepath.Join(w.dailyDir, now.Format("2006-01-02")+".md")

	var b strings.Builder
	b.WriteString(fmt.Sprintf("\n## %s — Conversation %s\n\n", now.Format("15:04"), conversationID))
	for _, fact := range facts {
		b.WriteString(fmt.Sprintf("- %s\n", fact))
	}

	f, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	// Add header if file is new.
	info, _ := f.Stat()
	if info.Size() == 0 {
		f.WriteString(fmt.Sprintf("# GoGoClaw Memory — %s\n", now.Format("2006-01-02")))
	}

	_, err = f.WriteString(b.String())
	return err
}

// ExtractFacts is a simple heuristic fact extractor.
// It looks for declarative statements in assistant messages that contain
// user preferences, decisions, or factual information.
func ExtractFacts(messages []provider.Message) []string {
	var facts []string
	seen := make(map[string]bool)

	for _, msg := range messages {
		if msg.Role != "assistant" || msg.Content == "" {
			continue
		}

		lines := strings.Split(msg.Content, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			// Skip short lines, questions, and tool-related content.
			if len(line) < 20 || strings.HasSuffix(line, "?") {
				continue
			}
			if strings.HasPrefix(line, "[tool:") || strings.HasPrefix(line, "[result:") {
				continue
			}

			// Look for factual patterns.
			lower := strings.ToLower(line)
			isFactual := false
			factualPrefixes := []string{
				"you prefer", "you like", "you want", "you need",
				"your ", "i'll remember", "i've noted", "noted:",
				"the user", "important:", "key point:", "decision:",
				"preference:", "remember:", "fact:",
			}
			for _, prefix := range factualPrefixes {
				if strings.Contains(lower, prefix) {
					isFactual = true
					break
				}
			}
			if !isFactual {
				continue
			}

			// Remove markdown formatting.
			clean := strings.TrimLeft(line, "- *>#")
			clean = strings.TrimSpace(clean)
			if clean == "" || seen[clean] {
				continue
			}
			seen[clean] = true
			facts = append(facts, clean)
		}
	}

	return facts
}
