package engine

import (
	"sync"
	"time"

	"github.com/DonScott603/gogoclaw/internal/provider"
)

// Session holds per-conversation state. Each conversation gets its own
// Session, ensuring no cross-conversation state bleed.
type Session struct {
	ID             string // "channel:conversationID"
	ConversationID string // GoGoClaw conversation ID
	Channel        string // "tui", "rest", "telegram"

	History []provider.Message

	SystemPrompt string
	PIIMode      string // per-session override
	AgentProfile string // agent name for this session

	// Lifecycle fields (used by Phase 8e -- populate from day one)
	LastActivityAt      time.Time
	LastBoundaryAt      time.Time
	TokensSinceBoundary int

	mu sync.Mutex // protects History and lifecycle fields
}

// AppendMessage appends a message to the session history under lock.
func (s *Session) AppendMessage(msg provider.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.History = append(s.History, msg)
}

// GetHistory returns a copy of the session history under lock.
func (s *Session) GetHistory() []provider.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := make([]provider.Message, len(s.History))
	copy(h, s.History)
	return h
}

// SetHistory replaces the session history under lock.
func (s *Session) SetHistory(history []provider.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.History = history
}

// ClearHistory resets the session history to nil under lock.
func (s *Session) ClearHistory() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.History = nil
}

// TouchActivity updates LastActivityAt to the current time.
func (s *Session) TouchActivity() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastActivityAt = time.Now()
}
