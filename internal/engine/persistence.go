package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/DonScott603/gogoclaw/internal/provider"
	"github.com/DonScott603/gogoclaw/internal/storage"
)

// Ensure SessionManager implements PersistenceHook.
var _ PersistenceHook = (*SessionManager)(nil)

// OnUserMessage persists a user message to SQLite.
func (sm *SessionManager) OnUserMessage(session *Session, msg provider.Message) {
	sm.persistMessage(session, msg)
}

// OnAssistantMessageComplete persists a completed assistant message to SQLite.
func (sm *SessionManager) OnAssistantMessageComplete(session *Session, msg provider.Message) {
	sm.persistMessage(session, msg)
}

// OnToolMessage persists a tool result message to SQLite.
func (sm *SessionManager) OnToolMessage(session *Session, msg provider.Message) {
	sm.persistMessage(session, msg)
}

func (sm *SessionManager) persistMessage(session *Session, msg provider.Message) {
	if sm.store == nil {
		return
	}

	stored := storage.StoredMessage{
		ID:             fmt.Sprintf("%s-%d", session.ConversationID, time.Now().UnixNano()),
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
	sm.ensureConversation(session)

	if err := sm.store.AddMessage(context.Background(), stored); err != nil {
		// Log but don't fail — persistence is best-effort in this path.
		// Future: add structured logging.
		_ = err
	}
}

func (sm *SessionManager) ensureConversation(session *Session) {
	if sm.store == nil {
		return
	}

	ctx := context.Background()
	existing, err := sm.store.GetConversation(ctx, session.ConversationID)
	if err != nil || existing != nil {
		return
	}

	now := time.Now()
	sm.store.CreateConversation(ctx, storage.Conversation{
		ID:        session.ConversationID,
		Title:     "Conversation",
		Agent:     session.AgentProfile,
		CreatedAt: now,
		UpdatedAt: now,
	})
}
