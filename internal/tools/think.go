package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// RegisterThinkTool registers the think tool (reasoning scratchpad, no side effects).
func RegisterThinkTool(d *Dispatcher) {
	d.Register(ToolDef{
		Name:        "think",
		Description: "A reasoning scratchpad. Use this to think through a problem step by step. No side effects — the content is returned as-is.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"thought": {"type": "string", "description": "Your reasoning or analysis"}
			},
			"required": ["thought"]
		}`),
		Fn: thinkFn(),
	})
}

type thinkArgs struct {
	Thought string `json:"thought"`
}

func thinkFn() ToolFunc {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		var a thinkArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("think: parse args: %w", err)
		}
		return a.Thought, nil
	}
}
