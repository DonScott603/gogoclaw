// Package engine implements the core agent loop that orchestrates
// LLM communication, tool dispatch, and context assembly.
package engine

import (
	"context"
	"fmt"
	"sync"

	"github.com/DonScott603/gogoclaw/internal/provider"
)

// Engine is the core agent orchestrator. It manages conversation history
// and routes messages through the LLM provider.
type Engine struct {
	provider     provider.Provider
	systemPrompt string
	mu           sync.Mutex
	history      []provider.Message
}

// New creates an Engine with the given provider and system prompt.
func New(p provider.Provider, systemPrompt string) *Engine {
	return &Engine{
		provider:     p,
		systemPrompt: systemPrompt,
	}
}

// Send sends a user message and returns the assistant's response (non-streaming).
func (e *Engine) Send(ctx context.Context, userMessage string) (string, error) {
	e.mu.Lock()
	e.history = append(e.history, provider.Message{Role: "user", Content: userMessage})
	messages := e.buildMessages()
	e.mu.Unlock()

	resp, err := e.provider.Chat(ctx, provider.ChatRequest{Messages: messages})
	if err != nil {
		return "", fmt.Errorf("engine: chat: %w", err)
	}

	e.mu.Lock()
	e.history = append(e.history, provider.Message{Role: "assistant", Content: resp.Content})
	e.mu.Unlock()

	return resp.Content, nil
}

// SendStream sends a user message and returns a channel of streaming chunks.
func (e *Engine) SendStream(ctx context.Context, userMessage string) (<-chan provider.StreamChunk, error) {
	e.mu.Lock()
	e.history = append(e.history, provider.Message{Role: "user", Content: userMessage})
	messages := e.buildMessages()
	e.mu.Unlock()

	ch, err := e.provider.ChatStream(ctx, provider.ChatRequest{Messages: messages})
	if err != nil {
		return nil, fmt.Errorf("engine: stream: %w", err)
	}

	// Wrap to capture the full response into history.
	out := make(chan provider.StreamChunk, 64)
	go func() {
		defer close(out)
		var full string
		for chunk := range ch {
			full += chunk.Content
			out <- chunk
		}
		e.mu.Lock()
		e.history = append(e.history, provider.Message{Role: "assistant", Content: full})
		e.mu.Unlock()
	}()
	return out, nil
}

// ProviderName returns the active provider's name.
func (e *Engine) ProviderName() string {
	return e.provider.Name()
}

// buildMessages prepends the system prompt to the conversation history.
// Must be called with e.mu held.
func (e *Engine) buildMessages() []provider.Message {
	msgs := make([]provider.Message, 0, len(e.history)+1)
	if e.systemPrompt != "" {
		msgs = append(msgs, provider.Message{Role: "system", Content: e.systemPrompt})
	}
	msgs = append(msgs, e.history...)
	return msgs
}
