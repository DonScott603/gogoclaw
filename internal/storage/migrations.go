package storage

import (
	"database/sql"
	"fmt"
)

// migration represents a single schema migration step.
type migration struct {
	version     int
	description string
	fn          func(tx *sql.Tx) error
}

// allMigrations returns the ordered list of schema migrations.
func allMigrations() []migration {
	return []migration{
		{
			version:     1,
			description: "Create conversations and messages tables",
			fn: func(tx *sql.Tx) error {
				_, err := tx.Exec(`
					CREATE TABLE IF NOT EXISTS conversations (
						id         TEXT PRIMARY KEY,
						title      TEXT NOT NULL DEFAULT '',
						agent      TEXT NOT NULL DEFAULT 'base',
						created_at DATETIME NOT NULL DEFAULT (datetime('now')),
						updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
					);

					CREATE TABLE IF NOT EXISTS messages (
						id              TEXT PRIMARY KEY,
						conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
						role            TEXT NOT NULL,
						content         TEXT NOT NULL DEFAULT '',
						tool_calls      TEXT,
						tool_call_id    TEXT DEFAULT '',
						token_count     INTEGER NOT NULL DEFAULT 0,
						created_at      DATETIME NOT NULL DEFAULT (datetime('now'))
					);

					CREATE INDEX IF NOT EXISTS idx_messages_conversation
						ON messages(conversation_id, created_at);
				`)
				return err
			},
		},
		{
			version:     2,
			description: "Add encryption metadata to messages",
			fn: func(tx *sql.Tx) error {
				_, err := tx.Exec(`ALTER TABLE messages ADD COLUMN encrypted BOOLEAN NOT NULL DEFAULT 0;`)
				return err
			},
		},
	}
}

// runMigrations brings the database schema up to date by running any
// outstanding migrations in order. Each migration runs in a transaction
// and is rolled back on failure.
func (s *Store) runMigrations() error {
	// Ensure the schema_version table exists.
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("storage: create schema_version table: %w", err)
	}

	// Read current version.
	var current int
	row := s.db.QueryRow(`SELECT version FROM schema_version LIMIT 1`)
	if err := row.Scan(&current); err != nil {
		if err == sql.ErrNoRows {
			current = 0
		} else {
			return fmt.Errorf("storage: read schema version: %w", err)
		}
	}

	migrations := allMigrations()
	for _, m := range migrations {
		if m.version <= current {
			continue
		}

		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("storage: begin migration %d: %w", m.version, err)
		}

		if err := m.fn(tx); err != nil {
			tx.Rollback()
			return fmt.Errorf("storage: migration %d (%s) failed: %w", m.version, m.description, err)
		}

		// Update version stamp within the transaction.
		if current == 0 {
			if _, err := tx.Exec(`INSERT INTO schema_version (version) VALUES (?)`, m.version); err != nil {
				tx.Rollback()
				return fmt.Errorf("storage: stamp version %d: %w", m.version, err)
			}
		} else {
			if _, err := tx.Exec(`UPDATE schema_version SET version = ?`, m.version); err != nil {
				tx.Rollback()
				return fmt.Errorf("storage: stamp version %d: %w", m.version, err)
			}
		}
		current = m.version

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("storage: commit migration %d: %w", m.version, err)
		}
	}

	return nil
}
