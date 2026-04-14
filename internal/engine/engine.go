// Package engine implements the core agent loop that orchestrates
// LLM communication, tool dispatch, and context assembly.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

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

// BoundaryType identifies which lifecycle layer triggered a conversation boundary.
type BoundaryType int

const (
	BoundaryExplicit BoundaryType = iota // user explicitly closed/switched conversation
	BoundarySoft                         // idle timeout or token budget threshold
	BoundaryDaily                        // daily safety net reset
)

// String returns a human-readable name for the boundary type.
func (bt BoundaryType) String() string {
	switch bt {
	case BoundaryExplicit:
		return "explicit"
	case BoundarySoft:
		return "soft"
	case BoundaryDaily:
		return "daily"
	default:
		return "unknown"
	}
}

// BoundarySummaryResult holds the output of a boundary summarization.
type BoundarySummaryResult struct {
	// CompactedHistory is the summarized/compacted history to replace the session's history.
	CompactedHistory []provider.Message

	// SessionSummary is a structured summary suitable for storage as a
	// retrievable vector document.
	SessionSummary string

	// FactsExtracted holds any facts extracted during boundary processing.
	FactsExtracted []string
}

// BoundarySummarizer produces a structured summary at conversation boundaries.
// It has a distinct prompt from mid-conversation summarization (the Summarizer
// interface) and produces both compacted history and a retrievable session
// summary document.
//
// Implementations are expected to:
// 1. Check session.Summarizing flag before running
// 2. Wait for in-flight mid-conversation summarization to complete (or cancel it)
// 3. Use a structured handoff prompt capturing: what the user was working on,
//    decisions made, open questions, commitments made
//
// Phase 8e implements the concrete boundary summarizer. This interface is
// defined here so the engine package owns the contract.
type BoundarySummarizer interface {
	SummarizeBoundary(ctx context.Context, session *Session, boundaryType BoundaryType) (*BoundarySummaryResult, error)
}

// NoOpBoundarySummarizer is a BoundarySummarizer that does nothing.
type NoOpBoundarySummarizer struct{}

func (NoOpBoundarySummarizer) SummarizeBoundary(context.Context, *Session, BoundaryType) (*BoundarySummaryResult, error) {
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
	provider           provider.Provider
	dispatcher         *tools.Dispatcher
	assembler          *ContextAssembler
	summarizer         Summarizer
	boundarySummarizer BoundarySummarizer
	systemPrompt       string
	promptMu           sync.RWMutex // protects systemPrompt
	persistence        PersistenceHook
}

// Config holds the parameters for creating a new Engine.
type Config struct {
	Provider           provider.Provider
	Dispatcher         *tools.Dispatcher
	SystemPrompt       string
	MaxContext          int
	Summarizer         Summarizer
	BoundarySummarizer BoundarySummarizer
	Persistence        PersistenceHook
}

// New creates an Engine with the given configuration.
func New(cfg Config) *Engine {
	s := cfg.Summarizer
	if s == nil {
		s = NoOpSummarizer{}
	}
	bs := cfg.BoundarySummarizer
	if bs == nil {
		bs = NoOpBoundarySummarizer{}
	}
	return &Engine{
		provider:           cfg.Provider,
		dispatcher:         cfg.Dispatcher,
		assembler:          NewContextAssembler(cfg.MaxContext, cfg.Provider),
		summarizer:         s,
		boundarySummarizer: bs,
		systemPrompt:       cfg.SystemPrompt,
		persistence:        cfg.Persistence,
	}
}

// BoundarySummarizer returns the configured boundary summarizer.
func (e *Engine) BoundarySummarizer() BoundarySummarizer {
	return e.boundarySummarizer
}

// Assembler returns the context assembler for external configuration.
func (e *Engine) Assembler() *ContextAssembler {
	return e.assembler
}

// persistAndAppendUser persists a user message first, then appends to session
// only on success. This prevents session/SQLite divergence.
func (e *Engine) persistAndAppendUser(ctx context.Context, session *Session, msg provider.Message) error {
	if e.persistence != nil {
		if err := e.persistence.OnUserMessage(ctx, session, msg); err != nil {
			return fmt.Errorf("engine: persist user message: %w", err)
		}
	}
	session.AppendMessage(msg)
	return nil
}

// persistAndAppendAssistant persists an assistant message first, then appends
// to session only on success.
func (e *Engine) persistAndAppendAssistant(ctx context.Context, session *Session, msg provider.Message) error {
	if e.persistence != nil {
		if err := e.persistence.OnAssistantMessageComplete(ctx, session, msg); err != nil {
			return fmt.Errorf("engine: persist assistant message: %w", err)
		}
	}
	session.AppendMessage(msg)
	return nil
}

// persistAndAppendTool persists a tool message first, then appends to session
// only on success.
func (e *Engine) persistAndAppendTool(ctx context.Context, session *Session, msg provider.Message) error {
	if e.persistence != nil {
		if err := e.persistence.OnToolMessage(ctx, session, msg); err != nil {
			return fmt.Errorf("engine: persist tool message: %w", err)
		}
	}
	session.AppendMessage(msg)
	return nil
}

// Send sends a user message, handles any tool call loops, and returns the
// final assistant text response (non-streaming).
func (e *Engine) Send(ctx context.Context, session *Session, userMessage string) (string, error) {
	// Apply any completed background summarization from a previous turn
	// BEFORE appending the new user message.
	e.applyPendingSummary(session)

	userMsg := provider.Message{Role: "user", Content: userMessage}
	if err := e.persistAndAppendUser(ctx, session, userMsg); err != nil {
		return "", err
	}

	result, err := e.runToolLoop(ctx, session)
	if err != nil {
		return "", err
	}

	// After the final assistant response, check if we should start
	// background summarization for next time.
	e.maybeStartSummarization(ctx, session)

	return result, nil
}

// SendStream sends a user message and returns a channel of streaming chunks.
// After streaming completes, if the response contains tool calls, it falls back
// to non-streaming mode for the tool loop and returns the final text on the channel.
func (e *Engine) SendStream(ctx context.Context, session *Session, userMessage string) (<-chan provider.StreamChunk, error) {
	// Apply any completed background summarization BEFORE the new turn.
	e.applyPendingSummary(session)

	userMsg := provider.Message{Role: "user", Content: userMessage}
	if err := e.persistAndAppendUser(ctx, session, userMsg); err != nil {
		return nil, err
	}

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
			if err := e.persistAndAppendAssistant(ctx, session, assistantMsg); err != nil {
				out <- provider.StreamChunk{Error: err, Done: true}
				return // error path — do NOT start summarization
			}

			if err := e.dispatchToolCalls(ctx, session, toolCalls); err != nil {
				out <- provider.StreamChunk{Error: err, Done: true}
				return // error path — do NOT start summarization
			}

			finalText, err := e.runToolLoop(ctx, session)
			if err != nil {
				out <- provider.StreamChunk{Error: err, Done: true}
				return // error path — do NOT start summarization
			}
			out <- provider.StreamChunk{Content: "\n" + finalText}
		} else {
			assistantMsg := provider.Message{Role: "assistant", Content: fullContent}
			if err := e.persistAndAppendAssistant(ctx, session, assistantMsg); err != nil {
				out <- provider.StreamChunk{Error: err, Done: true}
				return // error path — do NOT start summarization
			}
		}

		// After successful final assistant response, check if we should
		// start background summarization for next time.
		// NOT in a defer — must not run on error-return paths above.
		e.maybeStartSummarization(ctx, session)
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
		if err := e.persistAndAppendTool(ctx, session, toolMsg); err != nil {
			return err
		}
	}

	return nil
}

// runToolLoop calls the provider in a loop, dispatching any tool calls, until
// a final text response is produced or maxToolRounds is exceeded.
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
			if err := e.persistAndAppendAssistant(ctx, session, assistantMsg); err != nil {
				return "", err
			}
			return resp.Content, nil
		}

		assistantMsg := provider.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		}
		if err := e.persistAndAppendAssistant(ctx, session, assistantMsg); err != nil {
			return "", err
		}

		if err := e.dispatchToolCalls(ctx, session, resp.ToolCalls); err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("engine: exceeded maximum tool call rounds (%d)", maxToolRounds)
}

// applyPendingSummary checks for a completed background summarization and
// reconciles it with the current session history. Called at the top of
// Send() and SendStream() BEFORE appending the new user message.
func (e *Engine) applyPendingSummary(session *Session) {
	if session.PendingSummary == nil {
		return
	}

	// Non-blocking check.
	select {
	case pending := <-session.PendingSummary:
		if pending == nil || pending.Result == nil {
			return
		}
		e.reconcileHistory(session, pending)
	default:
		// No pending result — nothing to do.
	}
}

// reconcileHistory merges a completed summarization result with the current
// session history, preserving any messages added since the snapshot was taken.
//
// Relies on the append-only invariant: between snapshot capture and this call,
// history may only have had messages appended at the end.
func (e *Engine) reconcileHistory(session *Session, pending *PendingSummaryResult) {
	session.mu.Lock()
	defer session.mu.Unlock()

	currentLen := len(session.History)
	snapshotLen := pending.SnapshotLen

	if snapshotLen < 0 || snapshotLen > currentLen {
		log.Printf("engine: invalid pending summary snapshotLen=%d, currentLen=%d — discarding",
			snapshotLen, currentLen)
		return
	}

	if pending.Result.RemainingHistory == nil {
		log.Printf("engine: pending summary has nil RemainingHistory — discarding")
		return
	}

	// Messages appended after the snapshot was taken.
	var newMessages []provider.Message
	if currentLen > snapshotLen {
		newMessages = make([]provider.Message, currentLen-snapshotLen)
		copy(newMessages, session.History[snapshotLen:])
	}

	// RemainingHistory already has: [summary system msg] + [kept messages from the snapshot]
	// We append messages that arrived while summarization was in-flight.
	reconciled := make([]provider.Message, 0, len(pending.Result.RemainingHistory)+len(newMessages))
	reconciled = append(reconciled, pending.Result.RemainingHistory...)
	reconciled = append(reconciled, newMessages...)

	session.History = reconciled

	log.Printf("engine: applied pending summary — snapshot=%d, current=%d, reconciled=%d",
		snapshotLen, currentLen, len(reconciled))
}

// maybeStartSummarization launches a background summarization goroutine if
// the session history exceeds the token threshold and no summarization is
// already in-flight. Called AFTER the final assistant message is appended.
func (e *Engine) maybeStartSummarization(ctx context.Context, session *Session) {
	if e.summarizer == nil {
		return
	}

	if !session.Summarizing.CompareAndSwap(false, true) {
		return
	}

	// Defensive — normally done at session creation.
	session.InitAsync()

	// Take a snapshot of history under lock.
	history := session.GetHistory()
	snapshotLen := len(history)
	convID := session.ConversationID

	go func() {
		defer session.Summarizing.Store(false)

		bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
		defer cancel()

		result, err := e.summarizer.MaybeSummarize(bgCtx, history, convID)
		if err != nil {
			log.Printf("engine: background summarization failed: %v", err)
			return
		}
		if result == nil {
			return // under threshold, nothing to do
		}

		pending := &PendingSummaryResult{
			Result:      result,
			SnapshotLen: snapshotLen,
		}
		select {
		case session.PendingSummary <- pending:
		default:
			// Channel full — drain stale result and replace with newer one.
			select {
			case <-session.PendingSummary:
			default:
			}
			select {
			case session.PendingSummary <- pending:
			default:
				log.Printf("engine: warning — failed to deliver pending summary after drain")
			}
		}

		log.Printf("engine: background summarization complete — %d facts extracted", len(result.FactsExtracted))
	}()
}

// ToolDefinitionsJSON returns tool definitions as raw JSON for debug/display.
func (e *Engine) ToolDefinitionsJSON() ([]byte, error) {
	if e.dispatcher == nil {
		return json.Marshal([]interface{}{})
	}
	return json.Marshal(e.dispatcher.Definitions())
}
