package engine

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DonScott603/gogoclaw/internal/memory"
	"github.com/DonScott603/gogoclaw/internal/provider"
)

// PendingSummaryResult holds a completed background summarization result
// along with the history length at the time the snapshot was taken,
// enabling reconciliation with messages added since.
type PendingSummaryResult struct {
	Result      *memory.SummarizeResult
	SnapshotLen int // len(history) when the summarization goroutine started
}

// Session holds per-conversation state. Each conversation gets its own
// Session, ensuring no cross-conversation state bleed.
//
// INVARIANT: Session.History is append-only between snapshot capture (when
// maybeStartSummarization copies history) and pending-summary application
// (when applyPendingSummary reconciles). Do not introduce non-append mutations
// (reordering, deletion, replacement) to history without revisiting the
// SnapshotLen-based reconciliation logic in reconcileHistory.
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

	// Async summarization fields
	Summarizing    atomic.Bool                // prevents concurrent summarizations
	PendingSummary chan *PendingSummaryResult  // buffered(1), completed results land here

	mu sync.Mutex // protects History and lifecycle fields
}

// InitAsync initializes the async summarization channel.
// Must be called before using the session with an engine that does async summarization.
// Safe to call multiple times (idempotent).
func (s *Session) InitAsync() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.PendingSummary == nil {
		s.PendingSummary = make(chan *PendingSummaryResult, 1)
	}
}

// HistoryLen returns the current history length under lock.
func (s *Session) HistoryLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.History)
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

// WaitForSummarization waits for any in-flight async summarization to finish.
// Returns true if the in-flight flag cleared before timeout, false if it
// timed out.
//
// This method does NOT consume PendingSummary. Callers should apply pending
// summaries through the normal engine-owned applyPendingSummary path to
// preserve single-consumer ownership of the pending result channel.
func (s *Session) WaitForSummarization(timeout time.Duration) bool {
	if !s.Summarizing.Load() {
		return true
	}

	deadline := time.Now().Add(timeout)
	for s.Summarizing.Load() {
		if time.Now().After(deadline) {
			log.Printf("engine: WaitForSummarization timed out after %v", timeout)
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
	return true
}

// TouchActivity updates LastActivityAt to the current time.
func (s *Session) TouchActivity() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastActivityAt = time.Now()
}
