package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"time"

	"github.com/DonScott603/gogoclaw/internal/provider"
	"github.com/DonScott603/gogoclaw/internal/storage"
)

// Ensure SessionManager implements PersistenceHook.
var _ PersistenceHook = (*SessionManager)(nil)

// OnUserMessage persists a user message to SQLite.
func (sm *SessionManager) OnUserMessage(ctx context.Context, session *Session, msg provider.Message) {
	sm.persistMessage(ctx, session, msg)
}

// OnAssistantMessageComplete persists a completed assistant message to SQLite.
func (sm *SessionManager) OnAssistantMessageComplete(ctx context.Context, session *Session, msg provider.Message) {
	sm.persistMessage(ctx, session, msg)
}

// OnToolMessage persists a tool result message to SQLite.
func (sm *SessionManager) OnToolMessage(ctx context.Context, session *Session, msg provider.Message) {
	sm.persistMessage(ctx, session, msg)
}

// generateMessageID returns a random hex string suitable for use as a
// unique message primary key. Using crypto/rand avoids collisions that
// would occur with timestamp-based IDs when tool calls are dispatched
// in parallel.
func generateMessageID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (sm *SessionManager) persistMessage(ctx context.Context, session *Session, msg provider.Message) {
	if sm.store == nil {
		return
	}

	stored := storage.StoredMessage{
		ID:             generateMessageID(),
		ConversationID: session.ConversationID,
		Role:           msg.Role,
		Content:        msg.Content,
		ToolCallID:     msg.ToolCallID,
		CreatedAt:      time.Now(),
	}

	if len(msg.ToolCalls) > 0 {
		if data, err := json.Marshal(msg.ToolCalls); err == nil {
			stored.ToolCalls = data
		}
	}

	// Ensure the conversation exists before adding messages.
	sm.ensureConversation(ctx, session)

	if err := sm.store.AddMessage(ctx, stored); err != nil {
		log.Printf("persistence: failed to write %s message for conversation %s: %v",
			msg.Role, session.ConversationID, err)
	}
}

func (sm *SessionManager) ensureConversation(ctx context.Context, session *Session) {
	if sm.store == nil {
		return
	}

	sm.mu.RLock()
	known := sm.knownConversations[session.ConversationID]
	sm.mu.RUnlock()
	if known {
		return
	}

	existing, err := sm.store.GetConversation(ctx, session.ConversationID)
	if err != nil {
		log.Printf("persistence: failed to check conversation %s: %v", session.ConversationID, err)
		return
	}

	if existing == nil {
		now := time.Now()
		if err := sm.store.CreateConversation(ctx, storage.Conversation{
			ID:        session.ConversationID,
			Title:     "Conversation",
			Agent:     session.AgentProfile,
			CreatedAt: now,
			UpdatedAt: now,
		}); err != nil {
			log.Printf("persistence: failed to create conversation %s: %v", session.ConversationID, err)
			return
		}
	}

	sm.mu.Lock()
	sm.knownConversations[session.ConversationID] = true
	sm.mu.Unlock()
}
