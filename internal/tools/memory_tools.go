package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// RegisterMemoryTools registers memory_save and memory_search stubs.
// Full implementation comes in Phase 3 when the vector store is integrated.
func RegisterMemoryTools(d *Dispatcher) {
	d.Register(ToolDef{
		Name:        "memory_save",
		Description: "Save a fact or piece of information to long-term memory.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"content": {"type": "string", "description": "The fact or information to remember"},
				"tags": {"type": "array", "items": {"type": "string"}, "description": "Optional tags for categorization"}
			},
			"required": ["content"]
		}`),
		Fn: memorySaveStub(),
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
			"required": ["query"]
		}`),
		Fn: memorySearchStub(),
	})
}

type memorySaveArgs struct {
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
}

func memorySaveStub() ToolFunc {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		var a memorySaveArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("memory_save: parse args: %w", err)
		}
		// TODO(phase3): persist to vector store
		return fmt.Sprintf("Memory saved: %q (stub — persistence not yet implemented)", a.Content), nil
	}
}

type memorySearchArgs struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k"`
}

func memorySearchStub() ToolFunc {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		var a memorySearchArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("memory_search: parse args: %w", err)
		}
		// TODO(phase3): search vector store
		return "No memories found (stub — vector store not yet implemented).", nil
	}
}
