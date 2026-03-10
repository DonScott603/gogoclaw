// Package pii implements PII detection and routing enforcement
// with configurable modes: strict, warn, permissive, disabled.
package pii

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/DonScott603/gogoclaw/internal/provider"
)

// Mode controls how PII detections are handled.
type Mode string

const (
	ModeStrict     Mode = "strict"     // Block and route to local only.
	ModeWarn       Mode = "warn"       // Flag but proceed.
	ModePermissive Mode = "permissive" // Log only.
	ModeDisabled   Mode = "disabled"   // No detection.
)

// WarnFunc is called when PII is detected in warn mode.
// It receives the detection details and should notify the user.
type WarnFunc func(patterns []string, mode Mode)

// AuditFunc is called for all PII detections for audit logging.
type AuditFunc func(patterns []string, mode Mode, action string)

// Gate wraps a provider.Provider and intercepts requests to detect PII.
type Gate struct {
	inner      provider.Provider
	classifier *Classifier
	mode       Mode
	mu         sync.RWMutex
	overrides  map[string]Mode // per-conversation overrides
	warnFn     WarnFunc
	auditFn    AuditFunc
	isLocal    bool // true if the inner provider is local (e.g., Ollama)
}

// GateConfig configures a PII gate.
type GateConfig struct {
	Mode    Mode
	IsLocal bool     // whether the provider is local (strict mode allows local)
	WarnFn  WarnFunc
	AuditFn AuditFunc
}

// NewGate creates a PII gate wrapping a provider.
func NewGate(inner provider.Provider, cfg GateConfig) *Gate {
	mode := cfg.Mode
	if mode == "" {
		mode = ModeDisabled
	}
	return &Gate{
		inner:      inner,
		classifier: NewClassifier(),
		mode:       mode,
		overrides:  make(map[string]Mode),
		warnFn:     cfg.WarnFn,
		auditFn:    cfg.AuditFn,
		isLocal:    cfg.IsLocal,
	}
}

// SetConversationOverride sets a per-conversation PII mode override.
func (g *Gate) SetConversationOverride(conversationID string, mode Mode) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.overrides[conversationID] = mode
}

// SetWarnFn sets or replaces the warn callback. This is useful when the
// callback depends on a component created after the gate (e.g. the TUI program).
func (g *Gate) SetWarnFn(fn WarnFunc) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.warnFn = fn
}

// ClearConversationOverride removes a per-conversation override.
func (g *Gate) ClearConversationOverride(conversationID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.overrides, conversationID)
}

// effectiveMode returns the mode to use, considering per-conversation overrides.
func (g *Gate) effectiveMode(conversationID string) Mode {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if m, ok := g.overrides[conversationID]; ok {
		return m
	}
	return g.mode
}

// Name delegates to the inner provider.
func (g *Gate) Name() string { return g.inner.Name() }

// CountTokens delegates to the inner provider.
func (g *Gate) CountTokens(content string) (int, error) { return g.inner.CountTokens(content) }

// Healthy delegates to the inner provider.
func (g *Gate) Healthy(ctx context.Context) bool { return g.inner.Healthy(ctx) }

// Chat intercepts the request to check for PII before forwarding.
func (g *Gate) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	if err := g.checkMessages(req.Messages, ""); err != nil {
		return &provider.ChatResponse{
			Content: err.Error(),
		}, nil
	}
	return g.inner.Chat(ctx, req)
}

// ChatStream intercepts the request to check for PII before forwarding.
func (g *Gate) ChatStream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	if err := g.checkMessages(req.Messages, ""); err != nil {
		ch := make(chan provider.StreamChunk, 1)
		ch <- provider.StreamChunk{Content: err.Error(), Done: true}
		close(ch)
		return ch, nil
	}
	return g.inner.ChatStream(ctx, req)
}

// CheckMessagesWithConversation checks messages using the effective mode for a conversation.
func (g *Gate) CheckMessagesWithConversation(messages []provider.Message, conversationID string) error {
	return g.checkMessages(messages, conversationID)
}

func (g *Gate) checkMessages(messages []provider.Message, conversationID string) error {
	mode := g.effectiveMode(conversationID)
	if mode == ModeDisabled {
		return nil
	}

	// Scan all user messages for PII.
	var allDetections []Detection
	for _, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		detections := g.classifier.Detect(msg.Content)
		allDetections = append(allDetections, detections...)
	}

	if len(allDetections) == 0 {
		return nil
	}

	patterns := PatternTypes(allDetections)

	switch mode {
	case ModeStrict:
		if g.auditFn != nil {
			g.auditFn(patterns, mode, "blocked")
		}
		if !g.isLocal {
			return fmt.Errorf("[PII BLOCKED] Detected sensitive data (%s) — request blocked from cloud provider. Use a local provider or adjust PII mode.",
				strings.Join(patterns, ", "))
		}
		// Local provider is allowed in strict mode.
		if g.auditFn != nil {
			g.auditFn(patterns, mode, "allowed_local")
		}

	case ModeWarn:
		if g.warnFn != nil {
			g.warnFn(patterns, mode)
		}
		if g.auditFn != nil {
			g.auditFn(patterns, mode, "warned")
		}

	case ModePermissive:
		if g.auditFn != nil {
			g.auditFn(patterns, mode, "logged")
		}
	}

	return nil
}
