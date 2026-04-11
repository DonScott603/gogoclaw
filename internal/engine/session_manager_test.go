package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/DonScott603/gogoclaw/internal/provider"
	"github.com/DonScott603/gogoclaw/internal/storage"
)

func newTestStore(t *testing.T) *storage.Store {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := storage.NewStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	t.Cleanup(func() { store.Close(); os.Remove(dbPath) })
	return store
}

func TestSessionManagerGetOrCreate(t *testing.T) {
	sm := NewSessionManager(nil)

	s1 := sm.GetOrCreate("tui", "conv1")
	if s1 == nil {
		t.Fatal("GetOrCreate returned nil")
	}
	if s1.Channel != "tui" || s1.ConversationID != "conv1" {
		t.Errorf("session = %+v, want channel=tui, convID=conv1", s1)
	}

	// Second call with same key returns same session.
	s2 := sm.GetOrCreate("tui", "conv1")
	if s1 != s2 {
		t.Error("GetOrCreate returned different session for same key")
	}

	// Different key returns different session.
	s3 := sm.GetOrCreate("rest", "conv1")
	if s1 == s3 {
		t.Error("GetOrCreate returned same session for different channel")
	}
}

func TestSessionManagerConcurrentGetOrCreate(t *testing.T) {
	sm := NewSessionManager(nil)

	const goroutines = 100
	sessions := make([]*Session, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			sessions[idx] = sm.GetOrCreate("tui", "shared")
		}(i)
	}
	wg.Wait()

	// All goroutines should have received the same session.
	for i := 1; i < goroutines; i++ {
		if sessions[i] != sessions[0] {
			t.Fatalf("goroutine %d got different session than goroutine 0", i)
		}
	}
}

func TestSessionManagerRemove(t *testing.T) {
	sm := NewSessionManager(nil)

	sm.GetOrCreate("tui", "conv1")
	sm.Remove(SessionKey{Channel: "tui", ConversationID: "conv1"})

	if s := sm.Get("tui", "conv1"); s != nil {
		t.Error("Get returned non-nil after Remove")
	}
}

func TestSessionManagerActiveSessions(t *testing.T) {
	sm := NewSessionManager(nil)

	sm.GetOrCreate("tui", "conv1")
	sm.GetOrCreate("rest", "conv2")

	active := sm.ActiveSessions()
	if len(active) != 2 {
		t.Errorf("ActiveSessions() length = %d, want 2", len(active))
	}
}

func TestSessionManagerLoadsFromStore(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create a conversation with messages in the store.
	now := time.Now()
	err := store.CreateConversation(ctx, storage.Conversation{
		ID: "conv1", Title: "Test", Agent: "base",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	toolCallsJSON, _ := json.Marshal([]provider.ToolCall{
		{ID: "call_1", Name: "think", Arguments: json.RawMessage(`{"thought":"test"}`)},
	})

	msgs := []storage.StoredMessage{
		{ID: "m1", ConversationID: "conv1", Role: "user", Content: "hello", CreatedAt: now},
		{ID: "m2", ConversationID: "conv1", Role: "assistant", Content: "hi", ToolCalls: toolCallsJSON, CreatedAt: now.Add(time.Second)},
		{ID: "m3", ConversationID: "conv1", Role: "tool", Content: "result", ToolCallID: "call_1", CreatedAt: now.Add(2 * time.Second)},
	}
	for _, m := range msgs {
		if err := store.AddMessage(ctx, m); err != nil {
			t.Fatalf("AddMessage: %v", err)
		}
	}

	sm := NewSessionManager(store)
	s := sm.GetOrCreate("tui", "conv1")

	h := s.GetHistory()
	if len(h) != 3 {
		t.Fatalf("history length = %d, want 3", len(h))
	}

	// Verify user message.
	if h[0].Role != "user" || h[0].Content != "hello" {
		t.Errorf("h[0] = %+v, want user/hello", h[0])
	}

	// Verify assistant message with tool calls preserved.
	if h[1].Role != "assistant" || len(h[1].ToolCalls) != 1 {
		t.Errorf("h[1] = %+v, want assistant with 1 tool call", h[1])
	}
	if h[1].ToolCalls[0].Name != "think" {
		t.Errorf("h[1].ToolCalls[0].Name = %q, want %q", h[1].ToolCalls[0].Name, "think")
	}

	// Verify tool message with ToolCallID preserved.
	if h[2].Role != "tool" || h[2].ToolCallID != "call_1" {
		t.Errorf("h[2] = %+v, want tool with ToolCallID=call_1", h[2])
	}
}
