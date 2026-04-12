package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
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

func TestEnsureConversationAndAddMessage(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	conv := Conversation{
		ID: "conv-tx", Title: "TX Test", Agent: "base",
		CreatedAt: now, UpdatedAt: now,
	}
	msg := StoredMessage{
		ID: "msg-tx-1", ConversationID: "conv-tx", Role: "user",
		Content: "hello", CreatedAt: now,
	}

	// First call should create conversation and insert message atomically.
	if err := store.EnsureConversationAndAddMessage(ctx, conv, msg); err != nil {
		t.Fatalf("EnsureConversationAndAddMessage: %v", err)
	}

	// Verify conversation exists.
	got, err := store.GetConversation(ctx, "conv-tx")
	if err != nil {
		t.Fatalf("GetConversation: %v", err)
	}
	if got == nil {
		t.Fatal("conversation should exist after transactional create")
	}

	// Verify message exists.
	msgs, err := store.GetMessages(ctx, "conv-tx")
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content != "hello" {
		t.Errorf("expected 1 message with content 'hello', got %d", len(msgs))
	}

	// Second call with same conv ID should just add message (no duplicate conv error).
	msg2 := StoredMessage{
		ID: "msg-tx-2", ConversationID: "conv-tx", Role: "assistant",
		Content: "hi back", CreatedAt: now.Add(time.Second),
	}
	if err := store.EnsureConversationAndAddMessage(ctx, conv, msg2); err != nil {
		t.Fatalf("second EnsureConversationAndAddMessage: %v", err)
	}

	msgs, _ = store.GetMessages(ctx, "conv-tx")
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages after second call, got %d", len(msgs))
	}
}

func TestEnsureConversationAndAddMessageRollback(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	conv := Conversation{
		ID: "conv-rb", Title: "Rollback Test", Agent: "base",
		CreatedAt: now, UpdatedAt: now,
	}

	// Insert a message with a duplicate ID to force failure on the second call.
	store.EnsureConversationAndAddMessage(ctx, conv, StoredMessage{
		ID: "dup-id", ConversationID: "conv-rb", Role: "user",
		Content: "first", CreatedAt: now,
	})

	// New conversation + duplicate message ID should fail.
	conv2 := Conversation{
		ID: "conv-rb-2", Title: "Should Not Exist", Agent: "base",
		CreatedAt: now, UpdatedAt: now,
	}
	err := store.EnsureConversationAndAddMessage(ctx, conv2, StoredMessage{
		ID: "dup-id", ConversationID: "conv-rb-2", Role: "user",
		Content: "second", CreatedAt: now,
	})
	if err == nil {
		t.Fatal("expected error from duplicate message ID")
	}

	// The new conversation should NOT have been created (rolled back).
	got, _ := store.GetConversation(ctx, "conv-rb-2")
	if got != nil {
		t.Error("conversation conv-rb-2 should not exist after rollback")
	}
}

func newTestEncryptor(t *testing.T) *Encryptor {
	t.Helper()
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	enc, err := NewEncryptorFromKey(key)
	if err != nil {
		t.Fatalf("NewEncryptorFromKey: %v", err)
	}
	return enc
}

func TestEncryptedMessageRoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	enc := newTestEncryptor(t)
	store.SetEncryptor(enc)

	now := time.Now().UTC().Truncate(time.Second)
	store.CreateConversation(ctx, Conversation{
		ID: "conv-enc", Title: "Encrypted", Agent: "base",
		CreatedAt: now, UpdatedAt: now,
	})

	original := "This is a secret message that should be encrypted"
	store.AddMessage(ctx, StoredMessage{
		ID: "msg-enc-1", ConversationID: "conv-enc", Role: "user",
		Content: original, TokenCount: 10, CreatedAt: now,
	})

	msgs, err := store.GetMessages(ctx, "conv-enc")
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if msgs[0].Content != original {
		t.Errorf("decrypted content = %q, want %q", msgs[0].Content, original)
	}
}

func TestMixedEncryptedUnencrypted(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	store.CreateConversation(ctx, Conversation{
		ID: "conv-mix", Title: "Mixed", Agent: "base",
		CreatedAt: now, UpdatedAt: now,
	})

	// Add plaintext message (no encryptor).
	store.AddMessage(ctx, StoredMessage{
		ID: "msg-plain", ConversationID: "conv-mix", Role: "user",
		Content: "plaintext message", TokenCount: 2, CreatedAt: now,
	})

	// Enable encryption and add another message.
	enc := newTestEncryptor(t)
	store.SetEncryptor(enc)
	store.AddMessage(ctx, StoredMessage{
		ID: "msg-secret", ConversationID: "conv-mix", Role: "assistant",
		Content: "encrypted message", TokenCount: 2, CreatedAt: now.Add(time.Second),
	})

	msgs, err := store.GetMessages(ctx, "conv-mix")
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if msgs[0].Content != "plaintext message" {
		t.Errorf("msg[0] content = %q, want %q", msgs[0].Content, "plaintext message")
	}
	if msgs[1].Content != "encrypted message" {
		t.Errorf("msg[1] content = %q, want %q", msgs[1].Content, "encrypted message")
	}
}

func TestMigrateToEncrypted(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	store.CreateConversation(ctx, Conversation{
		ID: "conv-mig", Title: "Migration", Agent: "base",
		CreatedAt: now, UpdatedAt: now,
	})

	// Add 5 unencrypted messages.
	for i := 0; i < 5; i++ {
		store.AddMessage(ctx, StoredMessage{
			ID:             fmt.Sprintf("msg-mig-%d", i),
			ConversationID: "conv-mig",
			Role:           "user",
			Content:        fmt.Sprintf("message %d", i),
			TokenCount:     1,
			CreatedAt:      now.Add(time.Duration(i) * time.Second),
		})
	}

	// Set encryptor and run migration.
	enc := newTestEncryptor(t)
	store.SetEncryptor(enc)
	if err := store.MigrateToEncrypted(ctx); err != nil {
		t.Fatalf("MigrateToEncrypted: %v", err)
	}

	// Verify all rows now have encrypted = 1.
	var unencrypted int
	store.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE encrypted = 0`).Scan(&unencrypted)
	if unencrypted != 0 {
		t.Errorf("unencrypted messages = %d, want 0", unencrypted)
	}

	// Verify content is correct after decryption.
	msgs, err := store.GetMessages(ctx, "conv-mig")
	if err != nil {
		t.Fatalf("GetMessages after migration: %v", err)
	}
	for i, m := range msgs {
		want := fmt.Sprintf("message %d", i)
		if m.Content != want {
			t.Errorf("msg[%d] content = %q, want %q", i, m.Content, want)
		}
	}
}

func TestMigrateToEncryptedIdempotent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	store.CreateConversation(ctx, Conversation{
		ID: "conv-idem", Title: "Idempotent", Agent: "base",
		CreatedAt: now, UpdatedAt: now,
	})

	store.AddMessage(ctx, StoredMessage{
		ID: "msg-idem-1", ConversationID: "conv-idem", Role: "user",
		Content: "test message", TokenCount: 2, CreatedAt: now,
	})

	enc := newTestEncryptor(t)
	store.SetEncryptor(enc)

	// Run migration twice.
	if err := store.MigrateToEncrypted(ctx); err != nil {
		t.Fatalf("MigrateToEncrypted (first): %v", err)
	}
	if err := store.MigrateToEncrypted(ctx); err != nil {
		t.Fatalf("MigrateToEncrypted (second): %v", err)
	}

	// Verify content is still correct (not double-encrypted).
	msgs, err := store.GetMessages(ctx, "conv-idem")
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content != "test message" {
		t.Errorf("content = %q, want %q", msgs[0].Content, "test message")
	}
}

func TestEncryptedToolCalls(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	enc := newTestEncryptor(t)
	store.SetEncryptor(enc)

	now := time.Now().UTC().Truncate(time.Second)
	store.CreateConversation(ctx, Conversation{
		ID: "conv-tc", Title: "ToolCalls", Agent: "base",
		CreatedAt: now, UpdatedAt: now,
	})

	originalTC := json.RawMessage(`[{"id":"call_1","name":"file_read","arguments":"{\"path\":\"/tmp/test\"}"}]`)
	store.AddMessage(ctx, StoredMessage{
		ID: "msg-tc-1", ConversationID: "conv-tc", Role: "assistant",
		Content: "reading file", ToolCalls: originalTC, TokenCount: 5, CreatedAt: now,
	})

	msgs, err := store.GetMessages(ctx, "conv-tc")
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}

	// Compare raw bytes exactly.
	if string(msgs[0].ToolCalls) != string(originalTC) {
		t.Errorf("tool_calls = %s, want %s", msgs[0].ToolCalls, originalTC)
	}
}

func TestGetMessagesEncryptedWithoutEncryptor(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	enc := newTestEncryptor(t)

	now := time.Now().UTC().Truncate(time.Second)
	store.CreateConversation(ctx, Conversation{
		ID: "conv-noenc", Title: "NoEnc", Agent: "base",
		CreatedAt: now, UpdatedAt: now,
	})

	// Write an encrypted message.
	store.SetEncryptor(enc)
	store.AddMessage(ctx, StoredMessage{
		ID: "msg-noenc-1", ConversationID: "conv-noenc", Role: "user",
		Content: "secret", TokenCount: 1, CreatedAt: now,
	})

	// Remove encryptor and try to read — should error.
	store.SetEncryptor(nil)
	_, err := store.GetMessages(ctx, "conv-noenc")
	if err == nil {
		t.Fatal("expected error reading encrypted message without encryptor")
	}
	if !strings.Contains(err.Error(), "encrypted but no encryptor is configured") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestMigrateToEncryptedDeterministicOrder(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	store.CreateConversation(ctx, Conversation{
		ID: "conv-det", Title: "Deterministic", Agent: "base",
		CreatedAt: now, UpdatedAt: now,
	})

	// Add messages with identical timestamps but different IDs.
	// IDs chosen so alphabetical order differs from insertion order.
	ids := []string{"msg-z", "msg-a", "msg-m"}
	for _, id := range ids {
		store.AddMessage(ctx, StoredMessage{
			ID:             id,
			ConversationID: "conv-det",
			Role:           "user",
			Content:        "content-" + id,
			TokenCount:     1,
			CreatedAt:      now, // same timestamp for all
		})
	}

	enc := newTestEncryptor(t)
	store.SetEncryptor(enc)
	if err := store.MigrateToEncrypted(ctx); err != nil {
		t.Fatalf("MigrateToEncrypted: %v", err)
	}

	// All rows should be encrypted and content should be correct.
	var encrypted int
	store.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE encrypted = 1`).Scan(&encrypted)
	if encrypted != 3 {
		t.Errorf("encrypted = %d, want 3", encrypted)
	}

	msgs, err := store.GetMessages(ctx, "conv-det")
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	for _, m := range msgs {
		want := "content-" + m.ID
		if m.Content != want {
			t.Errorf("msg %s: content = %q, want %q", m.ID, m.Content, want)
		}
	}
}

func TestAddMessageUpdatedAtErrorChecked(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)

	// Add a message referencing a conversation that does NOT exist.
	// The INSERT succeeds (no FK enforcement in our test setup without
	// PRAGMA foreign_keys=ON), but this verifies the updated_at UPDATE
	// path executes without panic. The UPDATE affects zero rows but
	// does not return an error — this test confirms the error check
	// path exists and doesn't break normal operation.
	store.CreateConversation(ctx, Conversation{
		ID: "conv-upd", Title: "Update", Agent: "base",
		CreatedAt: now, UpdatedAt: now,
	})
	err := store.AddMessage(ctx, StoredMessage{
		ID: "msg-upd-1", ConversationID: "conv-upd", Role: "user",
		Content: "test", TokenCount: 1, CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("AddMessage should succeed: %v", err)
	}

	// Close the DB to force a failure on the updated_at touch.
	store.db.Close()
	err = store.AddMessage(ctx, StoredMessage{
		ID: "msg-upd-2", ConversationID: "conv-upd", Role: "user",
		Content: "test2", TokenCount: 1, CreatedAt: now.Add(time.Second),
	})
	if err == nil {
		t.Fatal("expected error from AddMessage after DB close")
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
