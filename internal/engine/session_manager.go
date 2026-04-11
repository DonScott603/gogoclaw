package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/DonScott603/gogoclaw/internal/provider"
	"github.com/DonScott603/gogoclaw/internal/storage"
)

// SessionKey uniquely identifies a session by channel and conversation ID.
type SessionKey struct {
	Channel        string
	ConversationID string
}

// SessionManager manages per-conversation sessions. It is the only layer
// responsible for persistence — loading history from SQLite on session
// creation and writing messages via the PersistenceHook interface.
type SessionManager struct {
	sessions           map[SessionKey]*Session
	knownConversations map[string]bool // conversation IDs that exist in SQLite
	store              *storage.Store
	mu                 sync.RWMutex
}

// NewSessionManager creates a SessionManager backed by the given store.
func NewSessionManager(store *storage.Store) *SessionManager {
	return &SessionManager{
		sessions:           make(map[SessionKey]*Session),
		knownConversations: make(map[string]bool),
		store:              store,
	}
}

// GetOrCreate returns the existing session for the given key, or creates a
// new one and loads its history from SQLite. The double-check locking pattern
// guarantees exactly one Session per key even under concurrent access.
func (sm *SessionManager) GetOrCreate(channel, convID string) *Session {
	key := SessionKey{Channel: channel, ConversationID: convID}

	sm.mu.RLock()
	if s, ok := sm.sessions[key]; ok {
		sm.mu.RUnlock()
		return s
	}
	sm.mu.RUnlock()

	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Double-check after acquiring write lock.
	if s, ok := sm.sessions[key]; ok {
		return s
	}

	s := &Session{
		ID:             fmt.Sprintf("%s:%s", channel, convID),
		ConversationID: convID,
		Channel:        channel,
		LastActivityAt: time.Now(),
	}

	// Load history from SQLite if store is available.
	if sm.store != nil {
		msgs, err := sm.store.GetMessages(context.Background(), convID)
		if err == nil && len(msgs) > 0 {
			s.History = storedToProviderMessages(msgs)
			sm.knownConversations[convID] = true // already exists in DB
		}
	}

	sm.sessions[key] = s
	return s
}

// Get returns the session for the given key, or nil if not found.
func (sm *SessionManager) Get(channel, convID string) *Session {
	key := SessionKey{Channel: channel, ConversationID: convID}
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.sessions[key]
}

// Remove deletes a session from the manager.
func (sm *SessionManager) Remove(key SessionKey) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, key)
}

// ActiveSessions returns all active sessions (for Phase 8e boundary scanning).
func (sm *SessionManager) ActiveSessions() []*Session {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	sessions := make([]*Session, 0, len(sm.sessions))
	for _, s := range sm.sessions {
		sessions = append(sessions, s)
	}
	return sessions
}

// storedToProviderMessages converts StoredMessages from SQLite to provider.Messages,
// preserving ToolCalls and ToolCallID fields.
func storedToProviderMessages(stored []storage.StoredMessage) []provider.Message {
	msgs := make([]provider.Message, len(stored))
	for i, sm := range stored {
		msg := provider.Message{
			Role:       sm.Role,
			Content:    sm.Content,
			ToolCallID: sm.ToolCallID,
		}
		if len(sm.ToolCalls) > 0 {
			var toolCalls []provider.ToolCall
			if err := json.Unmarshal(sm.ToolCalls, &toolCalls); err == nil {
				msg.ToolCalls = toolCalls
			}
		}
		msgs[i] = msg
	}
	return msgs
}
