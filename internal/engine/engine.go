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

// PersistenceHook is called by the Engine at the right moments to persist
// messages. SessionManager implements this interface; Engine controls timing.
// Errors are propagated to callers so persistence failures are visible.
type PersistenceHook interface {
	OnUserMessage(ctx context.Context, session *Session, msg provider.Message) error
	OnAssistantMessageComplete(ctx context.Context, session *Session, msg provider.Message) error
	OnToolMessage(ctx context.Context, session *Session, msg provider.Message) error
}

// Engine is the core agent orchestrator. It is a stateless executor that
// receives a Session, reads/writes history via Session methods, and returns
// results. It does NOT hold any conversation state.
type Engine struct {
	provider     provider.Provider
	dispatcher   *tools.Dispatcher
	assembler    *ContextAssembler
	summarizer   Summarizer
	systemPrompt string
	promptMu     sync.RWMutex // protects systemPrompt
	persistence  PersistenceHook
}

// Config holds the parameters for creating a new Engine.
type Config struct {
	Provider     provider.Provider
	Dispatcher   *tools.Dispatcher
	SystemPrompt string
	MaxContext   int
	Summarizer   Summarizer
	Persistence  PersistenceHook
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
		persistence:  cfg.Persistence,
	}
}

// Assembler returns the context assembler for external configuration.
func (e *Engine) Assembler() *ContextAssembler {
	return e.assembler
}

// Send sends a user message, handles any tool call loops, and returns the
// final assistant text response (non-streaming).
func (e *Engine) Send(ctx context.Context, session *Session, userMessage string) (string, error) {
	userMsg := provider.Message{Role: "user", Content: userMessage}
	session.AppendMessage(userMsg)
	if e.persistence != nil {
		if err := e.persistence.OnUserMessage(ctx, session, userMsg); err != nil {
			return "", fmt.Errorf("engine: persist user message: %w", err)
		}
	}

	e.maybeSummarize(ctx, session)
	return e.runToolLoop(ctx, session)
}

// SendStream sends a user message and returns a channel of streaming chunks.
// After streaming completes, if the response contains tool calls, it falls back
// to non-streaming mode for the tool loop and returns the final text on the channel.
func (e *Engine) SendStream(ctx context.Context, session *Session, userMessage string) (<-chan provider.StreamChunk, error) {
	userMsg := provider.Message{Role: "user", Content: userMessage}
	session.AppendMessage(userMsg)
	if e.persistence != nil {
		if err := e.persistence.OnUserMessage(ctx, session, userMsg); err != nil {
			return nil, fmt.Errorf("engine: persist user message: %w", err)
		}
	}

	e.maybeSummarize(ctx, session)

	messages := e.buildMessages(session)
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
			assistantMsg := provider.Message{
				Role:      "assistant",
				Content:   fullContent,
				ToolCalls: toolCalls,
			}
			session.AppendMessage(assistantMsg)
			if e.persistence != nil {
				if err := e.persistence.OnAssistantMessageComplete(ctx, session, assistantMsg); err != nil {
					out <- provider.StreamChunk{Error: fmt.Errorf("engine: persist assistant message: %w", err), Done: true}
					return
				}
			}

			if err := e.dispatchToolCalls(ctx, session, toolCalls); err != nil {
				out <- provider.StreamChunk{Error: err, Done: true}
				return
			}

			// Continue with non-streaming tool loop.
			finalText, err := e.runToolLoop(ctx, session)
			if err != nil {
				out <- provider.StreamChunk{Error: err, Done: true}
				return
			}
			out <- provider.StreamChunk{Content: "\n" + finalText}
		} else {
			assistantMsg := provider.Message{Role: "assistant", Content: fullContent}
			session.AppendMessage(assistantMsg)
			if e.persistence != nil {
				if err := e.persistence.OnAssistantMessageComplete(ctx, session, assistantMsg); err != nil {
					out <- provider.StreamChunk{Error: fmt.Errorf("engine: persist assistant message: %w", err), Done: true}
					return
				}
			}
		}
	}()

	return out, nil
}

// ProviderName returns the active provider's name.
func (e *Engine) ProviderName() string {
	return e.provider.Name()
}

// SetSystemPrompt updates the engine's system prompt (thread-safe).
func (e *Engine) SetSystemPrompt(prompt string) {
	e.promptMu.Lock()
	defer e.promptMu.Unlock()
	e.systemPrompt = prompt
}

func (e *Engine) buildMessages(session *Session) []provider.Message {
	e.promptMu.RLock()
	prompt := e.systemPrompt
	e.promptMu.RUnlock()

	history := session.GetHistory()
	if e.assembler != nil {
		return e.assembler.Assemble(prompt, history, 500) // ~500 tokens for tool defs
	}
	msgs := make([]provider.Message, 0, len(history)+1)
	if prompt != "" {
		msgs = append(msgs, provider.Message{Role: "system", Content: prompt})
	}
	msgs = append(msgs, history...)
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

func (e *Engine) dispatchToolCalls(ctx context.Context, session *Session, toolCalls []provider.ToolCall) error {
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

	for _, r := range results {
		toolMsg := provider.Message{
			Role:       "tool",
			Content:    r.Content,
			ToolCallID: r.CallID,
		}
		session.AppendMessage(toolMsg)
		if e.persistence != nil {
			if err := e.persistence.OnToolMessage(ctx, session, toolMsg); err != nil {
				return fmt.Errorf("engine: persist tool message: %w", err)
			}
		}
	}

	return nil
}

// runToolLoop calls the provider in a loop, dispatching any tool calls, until
// a final text response is produced or maxToolRounds is exceeded. Both Send
// and SendStream delegate to this after appending the user message to history.
func (e *Engine) runToolLoop(ctx context.Context, session *Session) (string, error) {
	for round := 0; round < maxToolRounds; round++ {
		messages := e.buildMessages(session)
		req := e.buildRequest(messages)

		resp, err := e.provider.Chat(ctx, req)
		if err != nil {
			return "", fmt.Errorf("engine: chat: %w", err)
		}

		if len(resp.ToolCalls) == 0 {
			assistantMsg := provider.Message{Role: "assistant", Content: resp.Content}
			session.AppendMessage(assistantMsg)
			if e.persistence != nil {
				if err := e.persistence.OnAssistantMessageComplete(ctx, session, assistantMsg); err != nil {
					return "", fmt.Errorf("engine: persist assistant message: %w", err)
				}
			}
			return resp.Content, nil
		}

		assistantMsg := provider.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		}
		session.AppendMessage(assistantMsg)
		if e.persistence != nil {
			if err := e.persistence.OnAssistantMessageComplete(ctx, session, assistantMsg); err != nil {
				return "", fmt.Errorf("engine: persist assistant message: %w", err)
			}
		}

		if err := e.dispatchToolCalls(ctx, session, resp.ToolCalls); err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("engine: exceeded maximum tool call rounds (%d)", maxToolRounds)
}

// maybeSummarize runs rolling summarization if the history exceeds the threshold.
func (e *Engine) maybeSummarize(ctx context.Context, session *Session) {
	h := session.GetHistory()

	result, err := e.summarizer.MaybeSummarize(ctx, h, session.ConversationID)
	if err != nil || result == nil {
		return
	}

	session.SetHistory(result.RemainingHistory)
}

// ToolDefinitionsJSON returns tool definitions as raw JSON for debug/display.
func (e *Engine) ToolDefinitionsJSON() ([]byte, error) {
	if e.dispatcher == nil {
		return json.Marshal([]interface{}{})
	}
	return json.Marshal(e.dispatcher.Definitions())
}
