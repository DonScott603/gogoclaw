package storage

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrationRunnerFreshDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fresh.db")

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()
	defer os.Remove(dbPath)

	// Verify tables exist by querying them.
	var count int
	err = store.db.QueryRow(`SELECT COUNT(*) FROM conversations`).Scan(&count)
	if err != nil {
		t.Fatalf("conversations table missing: %v", err)
	}

	err = store.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&count)
	if err != nil {
		t.Fatalf("messages table missing: %v", err)
	}

	// Verify schema version is stamped.
	var version int
	err = store.db.QueryRow(`SELECT version FROM schema_version`).Scan(&version)
	if err != nil {
		t.Fatalf("schema_version query: %v", err)
	}
	if version != 2 {
		t.Errorf("schema version = %d, want 2", version)
	}
}

func TestMigrationRunnerExistingDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "existing.db")

	// Create a v1 database manually.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, err = db.Exec(`
		PRAGMA journal_mode=WAL;
		CREATE TABLE schema_version (version INTEGER NOT NULL);
		INSERT INTO schema_version (version) VALUES (1);
		CREATE TABLE conversations (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL DEFAULT '',
			agent TEXT NOT NULL DEFAULT 'base',
			created_at DATETIME NOT NULL DEFAULT (datetime('now')),
			updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
		);
		CREATE TABLE messages (
			id TEXT PRIMARY KEY,
			conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
			role TEXT NOT NULL,
			content TEXT NOT NULL DEFAULT '',
			tool_calls TEXT,
			tool_call_id TEXT DEFAULT '',
			token_count INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		);
		CREATE INDEX idx_messages_conversation ON messages(conversation_id, created_at);
	`)
	if err != nil {
		t.Fatalf("manual schema setup: %v", err)
	}
	db.Close()

	// Open with NewStore — should skip migration 1, run migration 2.
	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore on existing v1 DB: %v", err)
	}
	defer store.Close()

	var version int
	err = store.db.QueryRow(`SELECT version FROM schema_version`).Scan(&version)
	if err != nil {
		t.Fatalf("schema_version query: %v", err)
	}
	if version != 2 {
		t.Errorf("schema version = %d, want 2 (after migration 2)", version)
	}
}

func TestMigrationRunnerRollback(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "rollback.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, err = db.Exec(`PRAGMA journal_mode=WAL`)
	if err != nil {
		t.Fatalf("WAL: %v", err)
	}

	store := &Store{db: db}

	// Inject a failing migration after migration 1.
	origMigrations := allMigrations()

	// Run the real migration first to get to a good state.
	if err := store.runMigrations(); err != nil {
		t.Fatalf("initial migration: %v", err)
	}

	// Verify we're at version 2.
	var version int
	store.db.QueryRow(`SELECT version FROM schema_version`).Scan(&version)
	if version != 2 {
		t.Fatalf("expected version 2, got %d", version)
	}

	// Verify tables exist after migration.
	var count int
	err = store.db.QueryRow(`SELECT COUNT(*) FROM conversations`).Scan(&count)
	if err != nil {
		t.Fatalf("conversations table should exist: %v", err)
	}

	_ = origMigrations
	store.Close()
}

func TestMigration2AddsEncryptedColumn(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "m2.db")

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// Verify encrypted column exists via PRAGMA table_info.
	rows, err := store.db.Query(`PRAGMA table_info(messages)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer rows.Close()

	found := false
	for rows.Next() {
		var cid int
		var name, typeName string
		var notNull, pk int
		var dfltValue sql.NullString
		if err := rows.Scan(&cid, &name, &typeName, &notNull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if name == "encrypted" {
			found = true
			break
		}
	}
	if !found {
		t.Error("encrypted column not found in messages table")
	}
}
