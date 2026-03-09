// Package tools implements the tool dispatcher and always-available
// core tools (file ops, shell exec, web fetch, think, memory, discover).
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// ToolFunc is the signature for a tool implementation.
// It receives the raw JSON arguments and returns a string result or error.
type ToolFunc func(ctx context.Context, args json.RawMessage) (string, error)

// ToolDef describes a registered tool.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Fn          ToolFunc        `json:"-"`
}

// ToolCallRequest is a single tool invocation from the LLM.
type ToolCallRequest struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolCallResult is the output of a single tool invocation.
type ToolCallResult struct {
	CallID  string `json:"call_id"`
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

// Dispatcher manages tool registration, validation, and parallel execution.
type Dispatcher struct {
	tools      map[string]ToolDef
	mu         sync.RWMutex
	timeout    time.Duration
	onToolCall func(name string, args json.RawMessage)            // optional callback for TUI
	onResult   func(name string, callID string, result string, isErr bool) // optional callback
}

// NewDispatcher creates a Dispatcher with the given default timeout per tool call.
func NewDispatcher(timeout time.Duration) *Dispatcher {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &Dispatcher{
		tools:   make(map[string]ToolDef),
		timeout: timeout,
	}
}

// Register adds a tool to the dispatcher.
func (d *Dispatcher) Register(tool ToolDef) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.tools[tool.Name] = tool
}

// SetCallbacks sets optional observer callbacks for tool execution events.
func (d *Dispatcher) SetCallbacks(
	onCall func(name string, args json.RawMessage),
	onResult func(name string, callID string, result string, isErr bool),
) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onToolCall = onCall
	d.onResult = onResult
}

// Definitions returns all registered tools as OpenAI-compatible tool definitions.
func (d *Dispatcher) Definitions() []ToolDefJSON {
	d.mu.RLock()
	defer d.mu.RUnlock()
	defs := make([]ToolDefJSON, 0, len(d.tools))
	for _, t := range d.tools {
		defs = append(defs, ToolDefJSON{
			Type: "function",
			Function: ToolFunctionJSON{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}
	return defs
}

// ToolDefJSON is the OpenAI-compatible tool definition format.
type ToolDefJSON struct {
	Type     string           `json:"type"`
	Function ToolFunctionJSON `json:"function"`
}

// ToolFunctionJSON is the function description within a tool definition.
type ToolFunctionJSON struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Dispatch executes one or more tool calls in parallel and returns results.
func (d *Dispatcher) Dispatch(ctx context.Context, calls []ToolCallRequest) []ToolCallResult {
	results := make([]ToolCallResult, len(calls))
	var wg sync.WaitGroup

	for i, call := range calls {
		wg.Add(1)
		go func(idx int, c ToolCallRequest) {
			defer wg.Done()
			results[idx] = d.execOne(ctx, c)
		}(i, call)
	}
	wg.Wait()
	return results
}

func (d *Dispatcher) execOne(ctx context.Context, call ToolCallRequest) ToolCallResult {
	d.mu.RLock()
	tool, ok := d.tools[call.Name]
	onCall := d.onToolCall
	onResult := d.onResult
	d.mu.RUnlock()

	if !ok {
		return ToolCallResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("unknown tool: %s", call.Name),
			IsError: true,
		}
	}

	if onCall != nil {
		onCall(call.Name, call.Arguments)
	}

	childCtx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()

	content, err := tool.Fn(childCtx, call.Arguments)
	isErr := err != nil
	if err != nil {
		content = err.Error()
	}

	if onResult != nil {
		onResult(call.Name, call.ID, content, isErr)
	}

	return ToolCallResult{
		CallID:  call.ID,
		Content: content,
		IsError: isErr,
	}
}
