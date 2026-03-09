package types

import "encoding/json"

// ToolDefinition describes a tool available to the LLM.
type ToolDefinition struct {
	Name        string          `json:"name" yaml:"name"`
	Description string          `json:"description" yaml:"description"`
	Parameters  json.RawMessage `json:"parameters" yaml:"parameters"`
}

// ToolCall represents an LLM's request to invoke a tool.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolResult holds the output of a tool invocation.
type ToolResult struct {
	CallID  string `json:"call_id"`
	Content string `json:"content"`
	IsError bool   `json:"is_error,omitempty"`
}
