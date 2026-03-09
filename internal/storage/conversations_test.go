package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestCreateAndGetConversation(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	c := Conversation{
		ID:        "conv-1",
		Title:     "Test Conversation",
		Agent:     "base",
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := store.CreateConversation(ctx, c); err != nil {
		t.Fatalf("CreateConversation error: %v", err)
	}

	got, err := store.GetConversation(ctx, "conv-1")
	if err != nil {
		t.Fatalf("GetConversation error: %v", err)
	}
	if got == nil {
		t.Fatal("GetConversation returned nil")
	}
	if got.Title != "Test Conversation" {
		t.Errorf("title = %q, want %q", got.Title, "Test Conversation")
	}
}

func TestListConversations(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < 3; i++ {
		store.CreateConversation(ctx, Conversation{
			ID:        fmt.Sprintf("conv-%d", i),
			Title:     fmt.Sprintf("Conv %d", i),
			Agent:     "base",
			CreatedAt: now.Add(time.Duration(i) * time.Second),
			UpdatedAt: now.Add(time.Duration(i) * time.Second),
		})
	}

	convos, err := store.ListConversations(ctx)
	if err != nil {
		t.Fatalf("ListConversations error: %v", err)
	}
	if len(convos) != 3 {
		t.Errorf("got %d conversations, want 3", len(convos))
	}
	// Should be ordered newest first.
	if convos[0].ID != "conv-2" {
		t.Errorf("first conversation = %q, want %q", convos[0].ID, "conv-2")
	}
}

func TestAddAndGetMessages(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	store.CreateConversation(ctx, Conversation{
		ID: "conv-1", Title: "Test", Agent: "base",
		CreatedAt: now, UpdatedAt: now,
	})

	msgs := []StoredMessage{
		{
			ID: "msg-1", ConversationID: "conv-1", Role: "user",
			Content: "Hello", TokenCount: 2, CreatedAt: now,
		},
		{
			ID: "msg-2", ConversationID: "conv-1", Role: "assistant",
			Content: "Hi there!", TokenCount: 3, CreatedAt: now.Add(time.Second),
		},
		{
			ID: "msg-3", ConversationID: "conv-1", Role: "assistant",
			Content: "", TokenCount: 5,
			ToolCalls: json.RawMessage(`[{"id":"call_1","name":"think","arguments":"{}"}]`),
			CreatedAt: now.Add(2 * time.Second),
		},
	}

	for _, m := range msgs {
		if err := store.AddMessage(ctx, m); err != nil {
			t.Fatalf("AddMessage error: %v", err)
		}
	}

	got, err := store.GetMessages(ctx, "conv-1")
	if err != nil {
		t.Fatalf("GetMessages error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d messages, want 3", len(got))
	}
	if got[0].Role != "user" {
		t.Errorf("first message role = %q, want %q", got[0].Role, "user")
	}
	if got[2].ToolCalls == nil {
		t.Error("expected tool_calls on third message")
	}
}

func TestMessageCount(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	store.CreateConversation(ctx, Conversation{
		ID: "conv-1", Title: "Test", Agent: "base",
		CreatedAt: now, UpdatedAt: now,
	})

	store.AddMessage(ctx, StoredMessage{
		ID: "msg-1", ConversationID: "conv-1", Role: "user",
		Content: "Hello", CreatedAt: now,
	})

	count, err := store.MessageCount(ctx, "conv-1")
	if err != nil {
		t.Fatalf("MessageCount error: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestDeleteConversation(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	store.CreateConversation(ctx, Conversation{
		ID: "conv-1", Title: "Test", Agent: "base",
		CreatedAt: now, UpdatedAt: now,
	})

	if err := store.DeleteConversation(ctx, "conv-1"); err != nil {
		t.Fatalf("DeleteConversation error: %v", err)
	}

	got, err := store.GetConversation(ctx, "conv-1")
	if err != nil {
		t.Fatalf("GetConversation error: %v", err)
	}
	if got != nil {
		t.Error("expected nil after delete")
	}
}
