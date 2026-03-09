package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/DonScott603/gogoclaw/internal/memory"
)

// RegisterMemoryTools registers memory_save and memory_search backed by a VectorStore.
// If store is nil, the tools return stub responses.
func RegisterMemoryTools(d *Dispatcher, store memory.VectorStore, searchOpts memory.SearchOptions) {
	d.Register(ToolDef{
		Name:        "memory_save",
		Description: "Save a fact or piece of information to long-term memory.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"content": {"type": "string", "description": "The fact or information to remember"},
				"tags": {"type": "array", "items": {"type": "string"}, "description": "Optional tags for categorization"}
			},
			"required": ["content"],
			"additionalProperties": false
		}`),
		Fn: memorySaveFn(store),
	})

	d.Register(ToolDef{
		Name:        "memory_search",
		Description: "Search long-term memory for relevant information.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {"type": "string", "description": "Search query"},
				"top_k": {"type": "integer", "description": "Number of results to return (default: 5)"}
			},
			"required": ["query"],
			"additionalProperties": false
		}`),
		Fn: memorySearchFn(store, searchOpts),
	})
}

type memorySaveArgs struct {
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
}

func memorySaveFn(store memory.VectorStore) ToolFunc {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		var a memorySaveArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("memory_save: parse args: %w", err)
		}

		if store == nil {
			return fmt.Sprintf("Memory noted: %q (memory system not configured)", a.Content), nil
		}

		doc := memory.MemoryDocument{
			ID:        fmt.Sprintf("manual-%d", time.Now().UnixNano()),
			Content:   a.Content,
			Tags:      a.Tags,
			Timestamp: time.Now(),
			Source:    "tool-call",
		}
		if err := store.Store(ctx, doc); err != nil {
			return "", fmt.Errorf("memory_save: store: %w", err)
		}
		return fmt.Sprintf("Memory saved: %q", a.Content), nil
	}
}

type memorySearchArgs struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k"`
}

func memorySearchFn(store memory.VectorStore, defaultOpts memory.SearchOptions) ToolFunc {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		var a memorySearchArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("memory_search: parse args: %w", err)
		}

		if store == nil {
			return "No memories found (memory system not configured).", nil
		}

		topK := a.TopK
		if topK <= 0 {
			topK = 5
		}

		results, err := store.Search(ctx, a.Query, topK, defaultOpts)
		if err != nil {
			return "", fmt.Errorf("memory_search: %w", err)
		}
		if len(results) == 0 {
			return "No relevant memories found.", nil
		}

		var b strings.Builder
		for i, r := range results {
			b.WriteString(fmt.Sprintf("%d. [score=%.2f] %s", i+1, r.Score, r.Document.Content))
			if len(r.Document.Tags) > 0 {
				b.WriteString(fmt.Sprintf(" (tags: %s)", strings.Join(r.Document.Tags, ", ")))
			}
			b.WriteString("\n")
		}
		return b.String(), nil
	}
}
