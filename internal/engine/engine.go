// Package engine implements the core agent loop that orchestrates
// LLM communication, tool dispatch, and context assembly.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/DonScott603/gogoclaw/internal/memory"
	"github.com/DonScott603/gogoclaw/internal/provider"
	"github.com/DonScott603/gogoclaw/internal/tools"
)

// maxToolRounds limits how many tool call round-trips before we stop.
const maxToolRounds = 10

// Summarizer abstracts rolling summarization of conversation history.
type Summarizer interface {
	MaybeSummarize(ctx context.Context, history []provider.Message, conversationID string) (*memory.SummarizeResult, error)
}

// NoOpSummarizer is a Summarizer that never summarizes.
type NoOpSummarizer struct{}

func (NoOpSummarizer) MaybeSummarize(context.Context, []provider.Message, string) (*memory.SummarizeResult, error) {
	return nil, nil
}

// Engine is the core agent orchestrator. It manages conversation history,
// routes messages through the LLM provider, and dispatches tool calls.
type Engine struct {
	provider     provider.Provider
	dispatcher   *tools.Dispatcher
	assembler    *ContextAssembler
	summarizer   Summarizer
	systemPrompt string
	convID       string // current conversation ID
	mu           sync.Mutex
	history      []provider.Message
}

// Config holds the parameters for creating a new Engine.
type Config struct {
	Provider     provider.Provider
	Dispatcher   *tools.Dispatcher
	SystemPrompt string
	MaxContext   int
	Summarizer   Summarizer
}

// New creates an Engine with the given configuration.
func New(cfg Config) *Engine {
	s := cfg.Summarizer
	if s == nil {
		s = NoOpSummarizer{}
	}
	return &Engine{
		provider:     cfg.Provider,
		dispatcher:   cfg.Dispatcher,
		assembler:    NewContextAssembler(cfg.MaxContext, cfg.Provider),
		summarizer:   s,
		systemPrompt: cfg.SystemPrompt,
	}
}

// SetConversationID sets the current conversation ID for memory attribution.
func (e *Engine) SetConversationID(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.convID = id
}

// Assembler returns the context assembler for external configuration.
func (e *Engine) Assembler() *ContextAssembler {
	return e.assembler
}

// Send sends a user message, handles any tool call loops, and returns the
// final assistant text response (non-streaming).
func (e *Engine) Send(ctx context.Context, userMessage string) (string, error) {
	e.mu.Lock()
	e.history = append(e.history, provider.Message{Role: "user", Content: userMessage})
	e.mu.Unlock()

	e.maybeSummarize(ctx)
	return e.runToolLoop(ctx)
}

// SendStream sends a user message and returns a channel of streaming chunks.
// After streaming completes, if the response contains tool calls, it falls back
// to non-streaming mode for the tool loop and returns the final text on the channel.
func (e *Engine) SendStream(ctx context.Context, userMessage string) (<-chan provider.StreamChunk, error) {
	e.mu.Lock()
	e.history = append(e.history, provider.Message{Role: "user", Content: userMessage})
	e.mu.Unlock()

	e.maybeSummarize(ctx)

	messages := e.buildMessages()
	req := e.buildRequest(messages)

	ch, err := e.provider.ChatStream(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("engine: stream: %w", err)
	}

	out := make(chan provider.StreamChunk, 64)
	go func() {
		defer close(out)
		var fullContent string
		var toolCalls []provider.ToolCall

		for chunk := range ch {
			fullContent += chunk.Content
			if len(chunk.ToolCalls) > 0 {
				toolCalls = append(toolCalls, chunk.ToolCalls...)
			}
			out <- chunk
		}

		// If tool calls were returned, handle the tool loop.
		if len(toolCalls) > 0 {
			e.mu.Lock()
			e.history = append(e.history, provider.Message{
				Role:      "assistant",
				Content:   fullContent,
				ToolCalls: toolCalls,
			})
			e.mu.Unlock()

			if err := e.dispatchToolCalls(ctx, toolCalls); err != nil {
				out <- provider.StreamChunk{Error: err, Done: true}
				return
			}

			// Continue with non-streaming tool loop.
			finalText, err := e.runToolLoop(ctx)
			if err != nil {
				out <- provider.StreamChunk{Error: err, Done: true}
				return
			}
			out <- provider.StreamChunk{Content: "\n" + finalText}
		} else {
			e.mu.Lock()
			e.history = append(e.history, provider.Message{Role: "assistant", Content: fullContent})
			e.mu.Unlock()
		}
	}()

	return out, nil
}

// ProviderName returns the active provider's name.
func (e *Engine) ProviderName() string {
	return e.provider.Name()
}

// History returns a copy of the current conversation history.
func (e *Engine) History() []provider.Message {
	e.mu.Lock()
	defer e.mu.Unlock()
	h := make([]provider.Message, len(e.history))
	copy(h, e.history)
	return h
}

// SetHistory replaces the conversation history (used when loading from storage).
func (e *Engine) SetHistory(history []provider.Message) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.history = history
}

// SetSystemPrompt updates the engine's system prompt (thread-safe).
func (e *Engine) SetSystemPrompt(prompt string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.systemPrompt = prompt
}

// ClearHistory resets the conversation history.
func (e *Engine) ClearHistory() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.history = nil
}

func (e *Engine) buildMessages() []provider.Message {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.assembler != nil {
		return e.assembler.Assemble(e.systemPrompt, e.history, 500) // ~500 tokens for tool defs
	}
	msgs := make([]provider.Message, 0, len(e.history)+1)
	if e.systemPrompt != "" {
		msgs = append(msgs, provider.Message{Role: "system", Content: e.systemPrompt})
	}
	msgs = append(msgs, e.history...)
	return msgs
}

func (e *Engine) buildRequest(messages []provider.Message) provider.ChatRequest {
	req := provider.ChatRequest{Messages: messages}
	if e.dispatcher != nil {
		defs := e.dispatcher.Definitions()
		for _, d := range defs {
			req.Tools = append(req.Tools, provider.Tool{
				Type: d.Type,
				Function: provider.ToolFunction{
					Name:        d.Function.Name,
					Description: d.Function.Description,
					Parameters:  d.Function.Parameters,
				},
			})
		}
	}
	return req
}

func (e *Engine) dispatchToolCalls(ctx context.Context, toolCalls []provider.ToolCall) error {
	if e.dispatcher == nil {
		return fmt.Errorf("engine: tool calls received but no dispatcher configured")
	}

	calls := make([]tools.ToolCallRequest, len(toolCalls))
	for i, tc := range toolCalls {
		calls[i] = tools.ToolCallRequest{
			ID:        tc.ID,
			Name:      tc.Name,
			Arguments: tc.Arguments,
		}
	}

	results := e.dispatcher.Dispatch(ctx, calls)

	e.mu.Lock()
	for _, r := range results {
		e.history = append(e.history, provider.Message{
			Role:       "tool",
			Content:    r.Content,
			ToolCallID: r.CallID,
		})
	}
	e.mu.Unlock()

	return nil
}

// runToolLoop calls the provider in a loop, dispatching any tool calls, until
// a final text response is produced or maxToolRounds is exceeded. Both Send
// and SendStream delegate to this after appending the user message to history.
func (e *Engine) runToolLoop(ctx context.Context) (string, error) {
	for round := 0; round < maxToolRounds; round++ {
		messages := e.buildMessages()
		req := e.buildRequest(messages)

		resp, err := e.provider.Chat(ctx, req)
		if err != nil {
			return "", fmt.Errorf("engine: chat: %w", err)
		}

		if len(resp.ToolCalls) == 0 {
			e.mu.Lock()
			e.history = append(e.history, provider.Message{Role: "assistant", Content: resp.Content})
			e.mu.Unlock()
			return resp.Content, nil
		}

		e.mu.Lock()
		e.history = append(e.history, provider.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})
		e.mu.Unlock()

		if err := e.dispatchToolCalls(ctx, resp.ToolCalls); err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("engine: exceeded maximum tool call rounds (%d)", maxToolRounds)
}

// maybeSummarize runs rolling summarization if the history exceeds the threshold.
func (e *Engine) maybeSummarize(ctx context.Context) {
	e.mu.Lock()
	h := make([]provider.Message, len(e.history))
	copy(h, e.history)
	convID := e.convID
	e.mu.Unlock()

	result, err := e.summarizer.MaybeSummarize(ctx, h, convID)
	if err != nil || result == nil {
		return
	}

	e.mu.Lock()
	e.history = result.RemainingHistory
	e.mu.Unlock()
}

// ToolDefinitionsJSON returns tool definitions as raw JSON for debug/display.
func (e *Engine) ToolDefinitionsJSON() ([]byte, error) {
	if e.dispatcher == nil {
		return json.Marshal([]interface{}{})
	}
	return json.Marshal(e.dispatcher.Definitions())
}
