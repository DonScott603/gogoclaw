// Package audit provides structured JSON Lines logging for
// security-relevant events (LLM requests, tool calls, network blocks, PII).
package audit

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// EventType identifies the kind of audit event.
type EventType string

const (
	EventLLMRequest    EventType = "llm_request"
	EventToolCall      EventType = "tool_call"
	EventNetworkBlock  EventType = "network_blocked"
	EventPIIDetected   EventType = "pii_detected"
	EventSkillLoaded    EventType = "skill_loaded"
	EventConfigChanged  EventType = "config_changed"
	EventSecretScrubbed EventType = "secret_scrubbed"
	EventRESTRequest    EventType = "rest_request"
)

// Event is a single audit log entry.
type Event struct {
	Timestamp time.Time         `json:"ts"`
	Type      EventType         `json:"event"`
	Fields    map[string]string `json:"fields,omitempty"`
}

// Scrubber is the interface for secret scrubbing used by the logger.
type Scrubber interface {
	Scrub(text string) string
	HasSecrets(text string) bool
}

// Logger writes structured audit events as JSON Lines.
type Logger struct {
	mu       sync.Mutex
	writer   io.Writer
	closer   io.Closer
	enabled  bool
	scrubber Scrubber
}

// LoggerConfig configures the audit logger.
type LoggerConfig struct {
	Enabled bool
	Path    string
}

// NewLogger creates an audit Logger. If path is empty, events are discarded.
func NewLogger(cfg LoggerConfig) (*Logger, error) {
	if !cfg.Enabled || cfg.Path == "" {
		return &Logger{enabled: false}, nil
	}

	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
		return nil, fmt.Errorf("audit: create dir: %w", err)
	}

	f, err := os.OpenFile(cfg.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("audit: open log: %w", err)
	}

	return &Logger{
		writer:  f,
		closer:  f,
		enabled: true,
	}, nil
}

// NewLoggerFromWriter creates a Logger that writes to the given writer (for testing).
func NewLoggerFromWriter(w io.Writer) *Logger {
	return &Logger{writer: w, enabled: true}
}

// SetScrubber attaches a secret scrubber that redacts field values before writing.
func (l *Logger) SetScrubber(s Scrubber) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.scrubber = s
}

// Log writes an audit event.
func (l *Logger) Log(eventType EventType, fields map[string]string) {
	if !l.enabled {
		return
	}

	// Scrub field values to prevent secrets from reaching the log.
	if l.scrubber != nil {
		for k, v := range fields {
			fields[k] = l.scrubber.Scrub(v)
		}
	}

	e := Event{
		Timestamp: time.Now(),
		Type:      eventType,
		Fields:    fields,
	}

	data, err := json.Marshal(e)
	if err != nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.writer.Write(data)
	l.writer.Write([]byte("\n"))
}

// LogLLMRequest logs an LLM request event.
func (l *Logger) LogLLMRequest(providerName, model string, tokensIn, tokensOut int, piiDetected bool, agent string) {
	pii := "false"
	if piiDetected {
		pii = "true"
	}
	l.Log(EventLLMRequest, map[string]string{
		"provider":     providerName,
		"model":        model,
		"tokens_in":    fmt.Sprintf("%d", tokensIn),
		"tokens_out":   fmt.Sprintf("%d", tokensOut),
		"pii_detected": pii,
		"agent":        agent,
	})
}

// LogToolCall logs a tool call event.
func (l *Logger) LogToolCall(toolName, skill, result string, durationMs int64) {
	l.Log(EventToolCall, map[string]string{
		"tool":        toolName,
		"skill":       skill,
		"result":      result,
		"duration_ms": fmt.Sprintf("%d", durationMs),
	})
}

// LogNetworkBlocked logs a blocked network request.
func (l *Logger) LogNetworkBlocked(domain, requester, reason string) {
	l.Log(EventNetworkBlock, map[string]string{
		"domain":    domain,
		"requester": requester,
		"reason":    reason,
	})
}

// LogPIIDetected logs a PII detection event.
func (l *Logger) LogPIIDetected(patterns []string, mode, action string) {
	l.Log(EventPIIDetected, map[string]string{
		"patterns": fmt.Sprintf("%v", patterns),
		"mode":     mode,
		"action":   action,
	})
}

// LogSecretScrubbed logs that secrets were detected and scrubbed from a component.
func (l *Logger) LogSecretScrubbed(component, context string) {
	l.Log(EventSecretScrubbed, map[string]string{
		"component": component,
		"context":   context,
	})
}

// Close closes the underlying file if applicable.
func (l *Logger) Close() error {
	if l.closer != nil {
		return l.closer.Close()
	}
	return nil
}
