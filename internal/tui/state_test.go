package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DonScott603/gogoclaw/internal/engine"
	"github.com/DonScott603/gogoclaw/internal/provider"
	"github.com/DonScott603/gogoclaw/internal/storage"

	tea "github.com/charmbracelet/bubbletea"
)

type stubProvider struct{}

func (s *stubProvider) Name() string { return "stub" }
func (s *stubProvider) Chat(_ context.Context, _ provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{Content: "ok"}, nil
}
func (s *stubProvider) ChatStream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 1)
	ch <- provider.StreamChunk{Content: "ok", Done: true}
	close(ch)
	return ch, nil
}
func (s *stubProvider) CountTokens(content string) (int, error) { return 0, nil }
func (s *stubProvider) Healthy(_ context.Context) bool          { return true }

func newTestStore(t *testing.T) *storage.Store {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := storage.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close(); os.Remove(dbPath) })
	return store
}

func newTestModel(t *testing.T) model {
	t.Helper()
	store := newTestStore(t)
	ctx := context.Background()
	eng := engine.New(engine.Config{
		Provider:   &stubProvider{},
		MaxContext: 4096,
	})
	sm := engine.NewSessionManager(store)

	m := initialModel(ctx, eng, sm, store)
	// Seed some messages so we can verify they're preserved on failure.
	m.messages = []chatMessage{
		{role: "user", content: "existing msg"},
		{role: "assistant", content: "existing reply"},
	}
	return m
}

func TestTUICtrlNFailedCreatePreservesState(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	eng := engine.New(engine.Config{
		Provider:   &stubProvider{},
		MaxContext: 4096,
	})
	sm := engine.NewSessionManager(store)

	m := initialModel(ctx, eng, sm, store)
	origSession := m.currentSession
	m.messages = []chatMessage{
		{role: "user", content: "existing"},
	}
	origMsgCount := len(m.messages)
	origConvoCount := len(m.conversations)

	// Close the store so conversation creation will fail.
	store.Close()

	// Simulate Ctrl+N.
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	updated := result.(model)

	// State should be preserved: same session, same messages, same conversation count.
	if updated.currentSession != origSession {
		t.Error("currentSession changed after failed Ctrl+N")
	}
	if len(updated.messages) != origMsgCount {
		t.Errorf("messages count = %d, want %d", len(updated.messages), origMsgCount)
	}
	if len(updated.conversations) != origConvoCount {
		t.Errorf("conversations count = %d, want %d", len(updated.conversations), origConvoCount)
	}
}

func TestTUICtrlLFailedSwitchPreservesState(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	eng := engine.New(engine.Config{
		Provider:   &stubProvider{},
		MaxContext: 4096,
	})
	sm := engine.NewSessionManager(store)

	// Create two conversations in the store.
	now := time.Now()
	store.CreateConversation(ctx, storage.Conversation{
		ID: "conv-a", Title: "Conv A", Agent: "base",
		CreatedAt: now, UpdatedAt: now,
	})
	store.CreateConversation(ctx, storage.Conversation{
		ID: "conv-b", Title: "Conv B", Agent: "base",
		CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second),
	})

	m := initialModel(ctx, eng, sm, store)
	origSession := m.currentSession
	m.messages = []chatMessage{
		{role: "user", content: "original message"},
	}
	origMsgCount := len(m.messages)

	// Navigate to conversation list panel.
	m.activePanel = panelConversations

	// Point activeConvoIdx to a conversation that will fail to load.
	// We do this by adding a fake entry and closing the store.
	m.conversations = append(m.conversations, conversationEntry{id: "nonexistent-will-fail", title: "Bad"})
	m.activeConvoIdx = len(m.conversations) - 1

	// Close the store so GetOrCreate will fail during the DB load.
	store.Close()

	// Simulate Ctrl+L to switch back (triggers session load).
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	updated := result.(model)

	// State should be preserved on failure.
	if updated.currentSession != origSession {
		t.Error("currentSession changed after failed switch")
	}
	if len(updated.messages) != origMsgCount {
		t.Errorf("messages count = %d, want %d", len(updated.messages), origMsgCount)
	}
	if updated.messages[0].content != "original message" {
		t.Errorf("message content = %q, want %q", updated.messages[0].content, "original message")
	}
}

func TestTUICtrlNSuccessCreatesStoreConversation(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	eng := engine.New(engine.Config{
		Provider:   &stubProvider{},
		MaxContext: 4096,
	})
	sm := engine.NewSessionManager(store)

	m := initialModel(ctx, eng, sm, store)
	origConvoCount := len(m.conversations)

	// Simulate Ctrl+N.
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	updated := result.(model)

	// Should have one more conversation.
	if len(updated.conversations) != origConvoCount+1 {
		t.Errorf("conversations count = %d, want %d", len(updated.conversations), origConvoCount+1)
	}

	// The new conversation should exist in the store.
	newID := updated.conversations[updated.activeConvoIdx].id
	convos, _ := store.ListConversations(ctx)
	found := false
	for _, c := range convos {
		if c.ID == newID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("new conversation %s not found in store", newID)
	}

	// Messages should be empty for new conversation.
	if len(updated.messages) != 0 {
		t.Errorf("messages count = %d, want 0 for new conversation", len(updated.messages))
	}

	// Session should be set.
	if updated.currentSession == nil {
		t.Error("currentSession should not be nil after successful Ctrl+N")
	}

	_ = fmt.Sprintf("placeholder") // keep fmt imported
}
