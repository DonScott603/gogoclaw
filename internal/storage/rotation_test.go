package storage

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newRotationTestDB creates a SQLite DB with schema at version 2
// (conversations + messages + encrypted column) without using NewStore.
func newRotationTestDB(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "rotation_test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatalf("set WAL: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE conversations (
			id         TEXT PRIMARY KEY,
			title      TEXT NOT NULL DEFAULT '',
			agent      TEXT NOT NULL DEFAULT 'base',
			created_at DATETIME NOT NULL DEFAULT (datetime('now')),
			updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
		);
		CREATE TABLE messages (
			id              TEXT PRIMARY KEY,
			conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
			role            TEXT NOT NULL,
			content         TEXT NOT NULL DEFAULT '',
			tool_calls      TEXT,
			tool_call_id    TEXT DEFAULT '',
			token_count     INTEGER NOT NULL DEFAULT 0,
			created_at      DATETIME NOT NULL DEFAULT (datetime('now')),
			encrypted       BOOLEAN NOT NULL DEFAULT 0
		);
		CREATE INDEX idx_messages_conversation ON messages(conversation_id, created_at);
		CREATE TABLE schema_version (version INTEGER NOT NULL);
		INSERT INTO schema_version (version) VALUES (2);
		INSERT INTO conversations (id, title, agent) VALUES ('conv-1', 'Test', 'base');
	`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return dbPath
}

// addTestMessage inserts a message directly via SQL (bypassing Store).
// If enc is non-nil, encrypts content/tool_calls and sets encrypted=1.
func addTestMessage(t *testing.T, dbPath string, id, content, toolCalls string, enc *Encryptor, createdAt time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	convID := "conv-1"
	role := "user"
	encrypted := 0

	storeContent := content
	storeToolCalls := toolCalls

	if enc != nil {
		aad := BuildMessageAAD(id, convID, role)
		ct, err := enc.Encrypt([]byte(content), aad)
		if err != nil {
			t.Fatalf("encrypt content: %v", err)
		}
		storeContent = base64.StdEncoding.EncodeToString(ct)

		if toolCalls != "" {
			tcCt, err := enc.Encrypt([]byte(toolCalls), aad)
			if err != nil {
				t.Fatalf("encrypt tool_calls: %v", err)
			}
			storeToolCalls = base64.StdEncoding.EncodeToString(tcCt)
		}
		encrypted = 1
	}

	_, err = db.Exec(
		`INSERT INTO messages (id, conversation_id, role, content, tool_calls, tool_call_id, token_count, created_at, encrypted)
		 VALUES (?, ?, ?, ?, ?, '', 0, ?, ?)`,
		id, convID, role, storeContent, storeToolCalls, createdAt.Format("2006-01-02 15:04:05"), encrypted,
	)
	if err != nil {
		t.Fatalf("insert message %s: %v", id, err)
	}
}

// readMessageContent reads the raw content and encrypted flag for a message.
func readMessageContent(t *testing.T, dbPath, id string) (content, toolCalls string, encrypted int) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	err = db.QueryRow(`SELECT content, tool_calls, encrypted FROM messages WHERE id = ?`, id).
		Scan(&content, &toolCalls, &encrypted)
	if err != nil {
		t.Fatalf("read message %s: %v", id, err)
	}
	return
}

// decryptMessageContent decrypts the stored content of a message using the given encryptor.
func decryptMessageContent(t *testing.T, dbPath, id string, enc *Encryptor) string {
	t.Helper()
	content, _, encrypted := readMessageContent(t, dbPath, id)
	if encrypted != 1 {
		t.Fatalf("message %s is not encrypted", id)
	}
	ct, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		t.Fatalf("base64 decode content for %s: %v", id, err)
	}
	aad := BuildMessageAAD(id, "conv-1", "user")
	plain, err := enc.Decrypt(ct, aad)
	if err != nil {
		t.Fatalf("decrypt content for %s: %v", id, err)
	}
	return string(plain)
}

func TestRotateKeysMessages(t *testing.T) {
	dbPath := newRotationTestDB(t)
	keyA := newTestEncryptor(t)
	keyB := newTestEncryptor(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	for i := 0; i < 5; i++ {
		addTestMessage(t, dbPath, fmt.Sprintf("msg-%d", i),
			fmt.Sprintf("content-%d", i), "", keyA,
			now.Add(time.Duration(i)*time.Second))
	}

	result, err := RotateKeys(ctx, RotateConfig{
		OldEncryptor: keyA,
		NewEncryptor: keyB,
		DBPath:       dbPath,
	})
	if err != nil {
		t.Fatalf("RotateKeys: %v", err)
	}
	if result.MessagesRotated != 5 {
		t.Errorf("MessagesRotated = %d, want 5", result.MessagesRotated)
	}

	// Verify all readable with key B.
	for i := 0; i < 5; i++ {
		got := decryptMessageContent(t, dbPath, fmt.Sprintf("msg-%d", i), keyB)
		want := fmt.Sprintf("content-%d", i)
		if got != want {
			t.Errorf("msg-%d: got %q, want %q", i, got, want)
		}
	}

	// Verify none readable with key A.
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("msg-%d", i)
		content, _, _ := readMessageContent(t, dbPath, id)
		ct, _ := base64.StdEncoding.DecodeString(content)
		aad := BuildMessageAAD(id, "conv-1", "user")
		_, err := keyA.Decrypt(ct, aad)
		if err == nil {
			t.Errorf("msg-%d: should NOT decrypt with old key", i)
		}
	}
}

func TestRotateKeysWithAAD(t *testing.T) {
	dbPath := newRotationTestDB(t)
	keyA := newTestEncryptor(t)
	keyB := newTestEncryptor(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	addTestMessage(t, dbPath, "msg-aad", "aad-test-content", "", keyA, now)

	_, err := RotateKeys(ctx, RotateConfig{
		OldEncryptor: keyA,
		NewEncryptor: keyB,
		DBPath:       dbPath,
	})
	if err != nil {
		t.Fatalf("RotateKeys: %v", err)
	}

	// Correct AAD succeeds.
	got := decryptMessageContent(t, dbPath, "msg-aad", keyB)
	if got != "aad-test-content" {
		t.Errorf("content = %q, want %q", got, "aad-test-content")
	}

	// Wrong AAD fails.
	content, _, _ := readMessageContent(t, dbPath, "msg-aad")
	ct, _ := base64.StdEncoding.DecodeString(content)
	wrongAAD := BuildMessageAAD("msg-aad", "conv-WRONG", "user")
	_, err = keyB.Decrypt(ct, wrongAAD)
	if err == nil {
		t.Error("decrypt with wrong AAD should fail")
	}
}

func TestRotateKeysWithToolCalls(t *testing.T) {
	dbPath := newRotationTestDB(t)
	keyA := newTestEncryptor(t)
	keyB := newTestEncryptor(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	originalTC := `[{"id":"call_1","name":"file_read","arguments":"{}"}]`
	addTestMessage(t, dbPath, "msg-tc", "tool content", originalTC, keyA, now)

	result, err := RotateKeys(ctx, RotateConfig{
		OldEncryptor: keyA,
		NewEncryptor: keyB,
		DBPath:       dbPath,
	})
	if err != nil {
		t.Fatalf("RotateKeys: %v", err)
	}
	if result.MessagesRotated != 1 {
		t.Errorf("MessagesRotated = %d, want 1", result.MessagesRotated)
	}

	// Verify tool_calls exact byte match after rotation.
	_, tcB64, enc := readMessageContent(t, dbPath, "msg-tc")
	if enc != 1 {
		t.Fatal("message should be encrypted")
	}
	tcCt, err := base64.StdEncoding.DecodeString(tcB64)
	if err != nil {
		t.Fatalf("base64 decode tool_calls: %v", err)
	}
	aad := BuildMessageAAD("msg-tc", "conv-1", "user")
	tcPlain, err := keyB.Decrypt(tcCt, aad)
	if err != nil {
		t.Fatalf("decrypt tool_calls with key B: %v", err)
	}
	if string(tcPlain) != originalTC {
		t.Errorf("tool_calls = %q, want %q", string(tcPlain), originalTC)
	}
}

func TestRotateKeysMixedState(t *testing.T) {
	dbPath := newRotationTestDB(t)
	keyA := newTestEncryptor(t)
	keyB := newTestEncryptor(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// 3 encrypted rows.
	for i := 0; i < 3; i++ {
		addTestMessage(t, dbPath, fmt.Sprintf("enc-%d", i),
			fmt.Sprintf("encrypted-%d", i), "", keyA,
			now.Add(time.Duration(i)*time.Second))
	}
	// 2 plaintext rows.
	for i := 0; i < 2; i++ {
		addTestMessage(t, dbPath, fmt.Sprintf("plain-%d", i),
			fmt.Sprintf("plaintext-%d", i), "", nil,
			now.Add(time.Duration(3+i)*time.Second))
	}

	result, err := RotateKeys(ctx, RotateConfig{
		OldEncryptor: keyA,
		NewEncryptor: keyB,
		DBPath:       dbPath,
	})
	if err != nil {
		t.Fatalf("RotateKeys: %v", err)
	}
	if result.MessagesRotated != 3 {
		t.Errorf("MessagesRotated = %d, want 3", result.MessagesRotated)
	}
	if result.PlaintextEncrypted != 2 {
		t.Errorf("PlaintextEncrypted = %d, want 2", result.PlaintextEncrypted)
	}

	// All 5 should be encrypted with key B.
	for i := 0; i < 3; i++ {
		got := decryptMessageContent(t, dbPath, fmt.Sprintf("enc-%d", i), keyB)
		if got != fmt.Sprintf("encrypted-%d", i) {
			t.Errorf("enc-%d: got %q", i, got)
		}
	}
	for i := 0; i < 2; i++ {
		got := decryptMessageContent(t, dbPath, fmt.Sprintf("plain-%d", i), keyB)
		if got != fmt.Sprintf("plaintext-%d", i) {
			t.Errorf("plain-%d: got %q", i, got)
		}
	}
}

func TestRotateKeysResumable(t *testing.T) {
	dbPath := newRotationTestDB(t)
	keyA := newTestEncryptor(t)
	keyB := newTestEncryptor(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// Encrypt 5 messages with key A.
	for i := 0; i < 5; i++ {
		addTestMessage(t, dbPath, fmt.Sprintf("msg-%d", i),
			fmt.Sprintf("content-%d", i), "", keyA,
			now.Add(time.Duration(i)*time.Second))
	}

	// Manually re-encrypt first 2 with key B (simulating partial prior run).
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		id := fmt.Sprintf("msg-%d", i)
		aad := BuildMessageAAD(id, "conv-1", "user")
		// Read current encrypted content.
		var contentB64 string
		db.QueryRow(`SELECT content FROM messages WHERE id = ?`, id).Scan(&contentB64)
		ct, _ := base64.StdEncoding.DecodeString(contentB64)
		plain, _ := keyA.Decrypt(ct, aad)
		// Re-encrypt with key B.
		newCt, _ := keyB.Encrypt(plain, aad)
		newB64 := base64.StdEncoding.EncodeToString(newCt)
		db.Exec(`UPDATE messages SET content = ? WHERE id = ?`, newB64, id)
	}
	db.Close()

	result, err := RotateKeys(ctx, RotateConfig{
		OldEncryptor: keyA,
		NewEncryptor: keyB,
		DBPath:       dbPath,
	})
	if err != nil {
		t.Fatalf("RotateKeys: %v", err)
	}
	if result.MessagesSkipped != 2 {
		t.Errorf("MessagesSkipped = %d, want 2", result.MessagesSkipped)
	}
	if result.MessagesRotated != 3 {
		t.Errorf("MessagesRotated = %d, want 3", result.MessagesRotated)
	}

	// All 5 readable with key B.
	for i := 0; i < 5; i++ {
		got := decryptMessageContent(t, dbPath, fmt.Sprintf("msg-%d", i), keyB)
		want := fmt.Sprintf("content-%d", i)
		if got != want {
			t.Errorf("msg-%d: got %q, want %q", i, got, want)
		}
	}
}

func TestRotateKeysBothKeysFail(t *testing.T) {
	dbPath := newRotationTestDB(t)
	keyA := newTestEncryptor(t)
	keyB := newTestEncryptor(t)
	keyC := newTestEncryptor(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// Encrypt with key C (neither A nor B).
	addTestMessage(t, dbPath, "msg-corrupt", "secret", "", keyC, now)

	_, err := RotateKeys(ctx, RotateConfig{
		OldEncryptor: keyA,
		NewEncryptor: keyB,
		DBPath:       dbPath,
	})
	if err == nil {
		t.Fatal("expected error when neither key can decrypt")
	}
	if !strings.Contains(err.Error(), "msg-corrupt") {
		t.Errorf("error should contain message ID, got: %v", err)
	}
	if !strings.Contains(err.Error(), "neither old nor new key") {
		t.Errorf("error should mention both keys failed, got: %v", err)
	}
}

func TestRotateKeysEmptyDatabase(t *testing.T) {
	dbPath := newRotationTestDB(t)
	keyA := newTestEncryptor(t)
	keyB := newTestEncryptor(t)
	ctx := context.Background()

	_, err := RotateKeys(ctx, RotateConfig{
		OldEncryptor: keyA,
		NewEncryptor: keyB,
		DBPath:       dbPath,
	})
	if err == nil {
		t.Fatal("expected error for empty database")
	}
	if !strings.Contains(err.Error(), "no messages to rotate") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRotateKeysSameKey(t *testing.T) {
	dbPath := newRotationTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	enc1, _ := NewEncryptorFromKey(key)
	enc2, _ := NewEncryptorFromKey(key)

	addTestMessage(t, dbPath, "msg-same", "content", "", enc1, now)

	_, err = RotateKeys(ctx, RotateConfig{
		OldEncryptor: enc1,
		NewEncryptor: enc2,
		DBPath:       dbPath,
	})
	if err == nil {
		t.Fatal("expected error for identical keys")
	}
	if !strings.Contains(err.Error(), "old and new keys are identical") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRotateKeysContentToolCallsConsistency(t *testing.T) {
	dbPath := newRotationTestDB(t)
	keyA := newTestEncryptor(t)
	keyB := newTestEncryptor(t)
	ctx := context.Background()

	// Manually create a row where content is encrypted with key A
	// but tool_calls is encrypted with key B (inconsistent state).
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}

	id := "msg-inconsistent"
	convID := "conv-1"
	role := "user"
	aad := BuildMessageAAD(id, convID, role)

	contentCt, _ := keyA.Encrypt([]byte("content"), aad)
	contentB64 := base64.StdEncoding.EncodeToString(contentCt)

	tcCt, _ := keyB.Encrypt([]byte(`[{"id":"call_1"}]`), aad)
	tcB64 := base64.StdEncoding.EncodeToString(tcCt)

	_, err = db.Exec(
		`INSERT INTO messages (id, conversation_id, role, content, tool_calls, tool_call_id, token_count, created_at, encrypted)
		 VALUES (?, ?, ?, ?, ?, '', 0, datetime('now'), 1)`,
		id, convID, role, contentB64, tcB64,
	)
	if err != nil {
		t.Fatalf("insert inconsistent message: %v", err)
	}
	db.Close()

	// Rotate with old=A, new=C. Content decrypts with A (old), but tool_calls
	// doesn't decrypt with A (wrong key) — tool_calls was encrypted with B.
	// However, tool_calls also won't decrypt with C (new key).
	// So this should fail with "neither old nor new key" for tool_calls.
	// But let's test with old=A, new=B so content=old, tool_calls=new → inconsistency.
	_, err = RotateKeys(ctx, RotateConfig{
		OldEncryptor: keyA,
		NewEncryptor: keyB,
		DBPath:       dbPath,
	})
	if err == nil {
		t.Fatal("expected error for inconsistent row")
	}
	if !strings.Contains(err.Error(), id) {
		t.Errorf("error should contain message ID %q, got: %v", id, err)
	}
	if !strings.Contains(err.Error(), "inconsistent") {
		t.Errorf("error should mention inconsistency, got: %v", err)
	}
}

func TestRotateKeysInvalidBase64Content(t *testing.T) {
	dbPath := newRotationTestDB(t)
	keyA := newTestEncryptor(t)
	keyB := newTestEncryptor(t)
	ctx := context.Background()

	// Insert a row with encrypted=1 but content that's not valid base64.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(
		`INSERT INTO messages (id, conversation_id, role, content, tool_calls, tool_call_id, token_count, created_at, encrypted)
		 VALUES ('msg-badb64', 'conv-1', 'user', '!!!not-base64!!!', '', '', 0, datetime('now'), 1)`,
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	db.Close()

	_, err = RotateKeys(ctx, RotateConfig{
		OldEncryptor: keyA,
		NewEncryptor: keyB,
		DBPath:       dbPath,
	})
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
	if !strings.Contains(err.Error(), "msg-badb64") {
		t.Errorf("error should contain message ID, got: %v", err)
	}
	if !strings.Contains(err.Error(), "invalid base64") {
		t.Errorf("error should mention base64, got: %v", err)
	}
}

// --- Audit log tests ---

func writeAuditFile(t *testing.T, path string, lines []string) {
	t.Helper()
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write audit file: %v", err)
	}
}

func encryptAuditLine(t *testing.T, plainJSON string, enc *Encryptor) string {
	t.Helper()
	ct, err := enc.Encrypt([]byte(plainJSON), nil)
	if err != nil {
		t.Fatalf("encrypt audit line: %v", err)
	}
	return "enc:v1:" + base64.StdEncoding.EncodeToString(ct)
}

func readAuditLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open audit file: %v", err)
	}
	defer f.Close()
	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan audit file: %v", err)
	}
	return lines
}

func TestRotateKeysAuditLog(t *testing.T) {
	dbPath := newRotationTestDB(t)
	keyA := newTestEncryptor(t)
	keyB := newTestEncryptor(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// Need at least one message for DB rotation to succeed.
	addTestMessage(t, dbPath, "msg-audit", "content", "", keyA, now)

	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")

	plainLine1 := `{"ts":"2026-01-01T00:00:00Z","event":"tool_call","fields":{"tool":"test"}}`
	plainLine2 := `{"ts":"2026-01-01T00:01:00Z","event":"llm_request","fields":{}}`
	encLine1 := encryptAuditLine(t, `{"ts":"2026-01-02T00:00:00Z","event":"enc1"}`, keyA)
	encLine2 := encryptAuditLine(t, `{"ts":"2026-01-02T00:01:00Z","event":"enc2"}`, keyA)
	encLine3 := encryptAuditLine(t, `{"ts":"2026-01-02T00:02:00Z","event":"enc3"}`, keyA)

	writeAuditFile(t, auditPath, []string{plainLine1, encLine1, plainLine2, encLine2, encLine3})

	result, err := RotateKeys(ctx, RotateConfig{
		OldEncryptor: keyA,
		NewEncryptor: keyB,
		DBPath:       dbPath,
		AuditPath:    auditPath,
	})
	if err != nil {
		t.Fatalf("RotateKeys: %v", err)
	}
	if result.AuditLinesRotated != 3 {
		t.Errorf("AuditLinesRotated = %d, want 3", result.AuditLinesRotated)
	}
	if result.AuditLinesPassedThru != 2 {
		t.Errorf("AuditLinesPassedThru = %d, want 2", result.AuditLinesPassedThru)
	}

	// Verify rotated file.
	lines := readAuditLines(t, auditPath)
	if len(lines) != 5 {
		t.Fatalf("got %d lines, want 5", len(lines))
	}

	// Plaintext lines should be unchanged.
	if lines[0] != plainLine1 {
		t.Errorf("line 0: got %q, want %q", lines[0], plainLine1)
	}
	if lines[2] != plainLine2 {
		t.Errorf("line 2: got %q, want %q", lines[2], plainLine2)
	}

	// Encrypted lines should be re-encrypted — decryptable with key B.
	for _, idx := range []int{1, 3, 4} {
		line := lines[idx]
		if !strings.HasPrefix(line, "enc:v1:") {
			t.Errorf("line %d: missing enc:v1: prefix", idx)
			continue
		}
		encoded := line[len("enc:v1:"):]
		ct, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Errorf("line %d: base64 decode: %v", idx, err)
			continue
		}
		_, err = keyB.Decrypt(ct, nil)
		if err != nil {
			t.Errorf("line %d: decrypt with new key: %v", idx, err)
		}
		// Should NOT decrypt with old key.
		_, err = keyA.Decrypt(ct, nil)
		if err == nil {
			t.Errorf("line %d: should NOT decrypt with old key", idx)
		}
	}
}

func TestRotateKeysAuditLogAtomicOnFailure(t *testing.T) {
	dbPath := newRotationTestDB(t)
	keyA := newTestEncryptor(t)
	keyB := newTestEncryptor(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	addTestMessage(t, dbPath, "msg-af", "content", "", keyA, now)

	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")

	// Write a valid encrypted line followed by a corrupt one.
	validLine := encryptAuditLine(t, `{"event":"valid"}`, keyA)
	corruptLine := "enc:v1:!!!garbage-not-base64!!!"
	writeAuditFile(t, auditPath, []string{validLine, corruptLine})

	// Save original file contents.
	originalBytes, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}

	_, err = RotateKeys(ctx, RotateConfig{
		OldEncryptor: keyA,
		NewEncryptor: keyB,
		DBPath:       dbPath,
		AuditPath:    auditPath,
	})
	if err == nil {
		t.Fatal("expected error for corrupt audit line")
	}

	// Verify original file is byte-for-byte unchanged.
	afterBytes, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterBytes) != string(originalBytes) {
		t.Error("original audit file was modified after failed rotation")
	}

	// Verify temp file was cleaned up.
	tmpPath := auditPath + ".rotating"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("temp file should have been removed after failure")
	}
}

func TestRotateKeysAuditLogMissing(t *testing.T) {
	dbPath := newRotationTestDB(t)
	keyA := newTestEncryptor(t)
	keyB := newTestEncryptor(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	addTestMessage(t, dbPath, "msg-am", "content", "", keyA, now)

	result, err := RotateKeys(ctx, RotateConfig{
		OldEncryptor: keyA,
		NewEncryptor: keyB,
		DBPath:       dbPath,
		AuditPath:    filepath.Join(t.TempDir(), "nonexistent.jsonl"),
	})
	if err != nil {
		t.Fatalf("RotateKeys: %v", err)
	}
	if result.AuditLinesRotated != 0 {
		t.Errorf("AuditLinesRotated = %d, want 0", result.AuditLinesRotated)
	}
	if result.AuditLinesPassedThru != 0 {
		t.Errorf("AuditLinesPassedThru = %d, want 0", result.AuditLinesPassedThru)
	}
	if result.MessagesRotated != 1 {
		t.Errorf("MessagesRotated = %d, want 1", result.MessagesRotated)
	}
}

func TestRotateKeysAuditLogNoPath(t *testing.T) {
	dbPath := newRotationTestDB(t)
	keyA := newTestEncryptor(t)
	keyB := newTestEncryptor(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	addTestMessage(t, dbPath, "msg-np", "content", "", keyA, now)

	result, err := RotateKeys(ctx, RotateConfig{
		OldEncryptor: keyA,
		NewEncryptor: keyB,
		DBPath:       dbPath,
		AuditPath:    "",
	})
	if err != nil {
		t.Fatalf("RotateKeys: %v", err)
	}
	if result.AuditLinesRotated != 0 || result.AuditLinesPassedThru != 0 {
		t.Error("audit counts should be 0 when path is empty")
	}
	if result.MessagesRotated != 1 {
		t.Errorf("MessagesRotated = %d, want 1", result.MessagesRotated)
	}
}

func TestGetRotationStats(t *testing.T) {
	dbPath := newRotationTestDB(t)
	keyA := newTestEncryptor(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// 3 encrypted + 2 plaintext messages.
	for i := 0; i < 3; i++ {
		addTestMessage(t, dbPath, fmt.Sprintf("enc-%d", i),
			fmt.Sprintf("encrypted-%d", i), "", keyA,
			now.Add(time.Duration(i)*time.Second))
	}
	for i := 0; i < 2; i++ {
		addTestMessage(t, dbPath, fmt.Sprintf("plain-%d", i),
			fmt.Sprintf("plaintext-%d", i), "", nil,
			now.Add(time.Duration(3+i)*time.Second))
	}

	// Audit file: 2 plaintext + 1 encrypted.
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	encLine := encryptAuditLine(t, `{"event":"test"}`, keyA)
	writeAuditFile(t, auditPath, []string{
		`{"event":"plain1"}`,
		encLine,
		`{"event":"plain2"}`,
	})

	stats, err := GetRotationStats(ctx, dbPath, auditPath)
	if err != nil {
		t.Fatalf("GetRotationStats: %v", err)
	}
	if stats.TotalMessages != 5 {
		t.Errorf("TotalMessages = %d, want 5", stats.TotalMessages)
	}
	if stats.EncryptedMessages != 3 {
		t.Errorf("EncryptedMessages = %d, want 3", stats.EncryptedMessages)
	}
	if stats.PlaintextMessages != 2 {
		t.Errorf("PlaintextMessages = %d, want 2", stats.PlaintextMessages)
	}
	if stats.AuditLines != 3 {
		t.Errorf("AuditLines = %d, want 3", stats.AuditLines)
	}
	if stats.EncryptedAuditLines != 1 {
		t.Errorf("EncryptedAuditLines = %d, want 1", stats.EncryptedAuditLines)
	}
}
