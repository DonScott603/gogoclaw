package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// RegisterDiscoverTool registers discover_tools as a stub.
// Full implementation comes in Phase 5 with the WASM skill runtime.
func RegisterDiscoverTool(d *Dispatcher) {
	d.Register(ToolDef{
		Name:        "discover_tools",
		Description: "Search for additional tools and skills that might help with the current task.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {"type": "string", "description": "Description of what you're trying to do"}
			},
			"required": ["query"],
			"additionalProperties": false
		}`),
		Fn: discoverToolsStub(),
	})
}

type discoverToolsArgs struct {
	Query string `json:"query"`
}

func discoverToolsStub() ToolFunc {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		var a discoverToolsArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("discover_tools: parse args: %w", err)
		}
		// TODO(phase5): search skill registry by description similarity
		return "No additional tools found (stub — skill runtime not yet implemented).", nil
	}
}
