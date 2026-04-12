// Package storage implements SQLite-backed conversation persistence.
package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"time"

	_ "modernc.org/sqlite"
)

// Scrubber scrubs secrets from text before persistence.
type Scrubber interface {
	Scrub(text string) string
	HasSecrets(text string) bool
}

// ScrubNotifyFn is called when secrets are scrubbed, for audit logging.
type ScrubNotifyFn func(component, context string)

// Store provides conversation and message persistence via SQLite.
type Store struct {
	db        *sql.DB
	scrubber  Scrubber
	onScrub   ScrubNotifyFn
	encryptor *Encryptor
}

// SetEncryptor attaches an encryptor for transparent at-rest encryption
// of message content and tool_calls.
func (s *Store) SetEncryptor(enc *Encryptor) {
	s.encryptor = enc
}

// Conversation represents a stored conversation.
type Conversation struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Agent     string    `json:"agent"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// StoredMessage represents a persisted message with metadata.
type StoredMessage struct {
	ID             string          `json:"id"`
	ConversationID string          `json:"conversation_id"`
	Role           string          `json:"role"`
	Content        string          `json:"content"`
	ToolCalls      json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID     string          `json:"tool_call_id,omitempty"`
	TokenCount     int             `json:"token_count"`
	CreatedAt      time.Time       `json:"created_at"`
}

// NewStore opens or creates a SQLite database at the given path.
func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("storage: open db: %w", err)
	}
	// Enable WAL mode for better concurrent read performance.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("storage: set WAL mode: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// SetScrubber attaches a secret scrubber and notification callback.
// When set, message content is scrubbed before being persisted to SQLite.
func (s *Store) SetScrubber(scrubber Scrubber, onScrub ScrubNotifyFn) {
	s.scrubber = scrubber
	s.onScrub = onScrub
}

func (s *Store) migrate() error {
	return s.runMigrations()
}

// CreateConversation inserts a new conversation.
func (s *Store) CreateConversation(ctx context.Context, c Conversation) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO conversations (id, title, agent, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		c.ID, c.Title, c.Agent, c.CreatedAt, c.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("storage: create conversation: %w", err)
	}
	return nil
}

// ListConversations returns all conversations ordered by most recent.
func (s *Store) ListConversations(ctx context.Context) ([]Conversation, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, title, agent, created_at, updated_at FROM conversations ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("storage: list conversations: %w", err)
	}
	defer rows.Close()

	var convos []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.Title, &c.Agent, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("storage: scan conversation: %w", err)
		}
		convos = append(convos, c)
	}
	return convos, rows.Err()
}

// ListConversationsPaged returns conversations with limit/offset pagination.
func (s *Store) ListConversationsPaged(ctx context.Context, limit, offset int) ([]Conversation, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, title, agent, created_at, updated_at FROM conversations ORDER BY updated_at DESC LIMIT ? OFFSET ?`,
		limit, offset)
	if err != nil {
		return nil, fmt.Errorf("storage: list conversations paged: %w", err)
	}
	defer rows.Close()

	var convos []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.Title, &c.Agent, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("storage: scan conversation: %w", err)
		}
		convos = append(convos, c)
	}
	return convos, rows.Err()
}

// GetConversation retrieves a single conversation by ID.
func (s *Store) GetConversation(ctx context.Context, id string) (*Conversation, error) {
	var c Conversation
	err := s.db.QueryRowContext(ctx,
		`SELECT id, title, agent, created_at, updated_at FROM conversations WHERE id = ?`, id,
	).Scan(&c.ID, &c.Title, &c.Agent, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("storage: get conversation: %w", err)
	}
	return &c, nil
}

// UpdateConversationTitle updates the title and updated_at timestamp.
func (s *Store) UpdateConversationTitle(ctx context.Context, id, title string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE conversations SET title = ?, updated_at = datetime('now') WHERE id = ?`,
		title, id,
	)
	if err != nil {
		return fmt.Errorf("storage: update conversation title: %w", err)
	}
	return nil
}

// DeleteConversation removes a conversation and all its messages.
func (s *Store) DeleteConversation(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM conversations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("storage: delete conversation: %w", err)
	}
	return nil
}

// AddMessage inserts a message into a conversation.
// If a scrubber is configured, message content is scrubbed before persistence.
// If an encryptor is set, content and tool_calls are encrypted transparently.
func (s *Store) AddMessage(ctx context.Context, m StoredMessage) error {
	// Scrub secrets from message content before persisting.
	if s.scrubber != nil && s.scrubber.HasSecrets(m.Content) {
		m.Content = s.scrubber.Scrub(m.Content)
		if s.onScrub != nil {
			s.onScrub("storage", "secrets scrubbed before SQLite persistence")
		}
	}

	content := m.Content
	toolCallsJSON := ""
	if m.ToolCalls != nil {
		toolCallsJSON = string(m.ToolCalls)
	}
	encrypted := 0

	if s.encryptor != nil {
		aad := BuildMessageAAD(m.ID, m.ConversationID, m.Role)

		ct, err := s.encryptor.Encrypt([]byte(content), aad)
		if err != nil {
			return fmt.Errorf("storage: encrypt content: %w", err)
		}
		content = base64.StdEncoding.EncodeToString(ct)

		if len(m.ToolCalls) > 0 {
			tcCt, err := s.encryptor.Encrypt(m.ToolCalls, aad)
			if err != nil {
				return fmt.Errorf("storage: encrypt tool_calls: %w", err)
			}
			toolCallsJSON = base64.StdEncoding.EncodeToString(tcCt)
		}
		encrypted = 1
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (id, conversation_id, role, content, tool_calls, tool_call_id, token_count, created_at, encrypted)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.ConversationID, m.Role, content, toolCallsJSON, m.ToolCallID, m.TokenCount, m.CreatedAt, encrypted,
	)
	if err != nil {
		return fmt.Errorf("storage: add message: %w", err)
	}
	// Touch conversation updated_at.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE conversations SET updated_at = datetime('now') WHERE id = ?`, m.ConversationID,
	); err != nil {
		return fmt.Errorf("storage: update conversation timestamp: %w", err)
	}
	return nil
}

// GetMessages retrieves all messages for a conversation in chronological order.
// Encrypted messages are decrypted transparently when an encryptor is set.
func (s *Store) GetMessages(ctx context.Context, conversationID string) ([]StoredMessage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, conversation_id, role, content, tool_calls, tool_call_id, token_count, created_at, encrypted
		 FROM messages WHERE conversation_id = ? ORDER BY created_at ASC`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("storage: get messages: %w", err)
	}
	defer rows.Close()

	var msgs []StoredMessage
	for rows.Next() {
		var m StoredMessage
		var toolCalls string
		var encryptedFlag int
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &toolCalls, &m.ToolCallID, &m.TokenCount, &m.CreatedAt, &encryptedFlag); err != nil {
			return nil, fmt.Errorf("storage: scan message: %w", err)
		}

		if encryptedFlag == 1 && s.encryptor == nil {
			return nil, fmt.Errorf("storage: message %s in conversation %s is encrypted but no encryptor is configured", m.ID, m.ConversationID)
		}

		if encryptedFlag == 1 {
			aad := BuildMessageAAD(m.ID, m.ConversationID, m.Role)

			contentCt, err := base64.StdEncoding.DecodeString(m.Content)
			if err != nil {
				return nil, fmt.Errorf("storage: decode encrypted content for message %s: %w", m.ID, err)
			}
			plainContent, err := s.encryptor.Decrypt(contentCt, aad)
			if err != nil {
				return nil, fmt.Errorf("storage: decrypt content for message %s: %w", m.ID, err)
			}
			m.Content = string(plainContent)

			if toolCalls != "" {
				tcCt, err := base64.StdEncoding.DecodeString(toolCalls)
				if err != nil {
					return nil, fmt.Errorf("storage: decode encrypted tool_calls for message %s: %w", m.ID, err)
				}
				plainTC, err := s.encryptor.Decrypt(tcCt, aad)
				if err != nil {
					return nil, fmt.Errorf("storage: decrypt tool_calls for message %s: %w", m.ID, err)
				}
				m.ToolCalls = json.RawMessage(plainTC)
			}
		} else {
			if toolCalls != "" {
				m.ToolCalls = json.RawMessage(toolCalls)
			}
		}

		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// EnsureConversationAndAddMessage atomically ensures the conversation exists
// and inserts a message within a single transaction. If the conversation
// already exists, only the message is inserted. If either step fails the
// entire transaction is rolled back.
func (s *Store) EnsureConversationAndAddMessage(ctx context.Context, conv Conversation, m StoredMessage) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: begin tx: %w", err)
	}
	defer tx.Rollback()

	// Check if conversation already exists.
	var exists int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM conversations WHERE id = ?`, conv.ID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("storage: check conversation: %w", err)
	}

	if exists == 0 {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO conversations (id, title, agent, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			conv.ID, conv.Title, conv.Agent, conv.CreatedAt, conv.UpdatedAt,
		); err != nil {
			return fmt.Errorf("storage: create conversation: %w", err)
		}
	}

	// Scrub secrets before persisting.
	if s.scrubber != nil && s.scrubber.HasSecrets(m.Content) {
		m.Content = s.scrubber.Scrub(m.Content)
		if s.onScrub != nil {
			s.onScrub("storage", "secrets scrubbed before SQLite persistence")
		}
	}

	content := m.Content
	toolCallsJSON := ""
	if m.ToolCalls != nil {
		toolCallsJSON = string(m.ToolCalls)
	}
	encrypted := 0

	if s.encryptor != nil {
		aad := BuildMessageAAD(m.ID, m.ConversationID, m.Role)

		ct, encErr := s.encryptor.Encrypt([]byte(content), aad)
		if encErr != nil {
			return fmt.Errorf("storage: encrypt content: %w", encErr)
		}
		content = base64.StdEncoding.EncodeToString(ct)

		if len(m.ToolCalls) > 0 {
			tcCt, encErr := s.encryptor.Encrypt(m.ToolCalls, aad)
			if encErr != nil {
				return fmt.Errorf("storage: encrypt tool_calls: %w", encErr)
			}
			toolCallsJSON = base64.StdEncoding.EncodeToString(tcCt)
		}
		encrypted = 1
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO messages (id, conversation_id, role, content, tool_calls, tool_call_id, token_count, created_at, encrypted)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.ConversationID, m.Role, content, toolCallsJSON, m.ToolCallID, m.TokenCount, m.CreatedAt, encrypted,
	); err != nil {
		return fmt.Errorf("storage: add message: %w", err)
	}

	// Touch conversation updated_at.
	if _, err := tx.ExecContext(ctx,
		`UPDATE conversations SET updated_at = datetime('now') WHERE id = ?`, m.ConversationID,
	); err != nil {
		return fmt.Errorf("storage: update conversation timestamp: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: commit tx: %w", err)
	}
	return nil
}

// MessageCount returns the number of messages in a conversation.
func (s *Store) MessageCount(ctx context.Context, conversationID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE conversation_id = ?`, conversationID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("storage: count messages: %w", err)
	}
	return count, nil
}

// MigrateToEncrypted encrypts all plaintext messages (encrypted = 0) in place.
// It processes rows in batches of 100 ordered by created_at ASC. Each batch
// runs in its own transaction. The migration is idempotent — already-encrypted
// rows are skipped. Stops on first failure.
func (s *Store) MigrateToEncrypted(ctx context.Context) error {
	if s.encryptor == nil {
		return fmt.Errorf("storage: migrate to encrypted: no encryptor set")
	}

	const batchSize = 100
	totalMigrated := 0

	for {
		// Count remaining unencrypted rows.
		var remaining int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM messages WHERE encrypted = 0`,
		).Scan(&remaining); err != nil {
			return fmt.Errorf("storage: count unencrypted messages: %w", err)
		}
		if remaining == 0 {
			break
		}

		// Fetch a batch of unencrypted messages.
		rows, err := s.db.QueryContext(ctx,
			`SELECT id, conversation_id, role, content, tool_calls
			 FROM messages WHERE encrypted = 0
			 ORDER BY created_at ASC, id ASC LIMIT ?`, batchSize)
		if err != nil {
			return fmt.Errorf("storage: query unencrypted messages: %w", err)
		}

		type rowData struct {
			id, convID, role, content, toolCalls string
		}
		var batch []rowData
		for rows.Next() {
			var r rowData
			if err := rows.Scan(&r.id, &r.convID, &r.role, &r.content, &r.toolCalls); err != nil {
				rows.Close()
				return fmt.Errorf("storage: scan unencrypted message: %w", err)
			}
			batch = append(batch, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("storage: iterate unencrypted messages: %w", err)
		}

		if len(batch) == 0 {
			break
		}

		// Encrypt the batch in a transaction.
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("storage: begin encrypt migration tx: %w", err)
		}

		for _, r := range batch {
			aad := BuildMessageAAD(r.id, r.convID, r.role)

			contentCt, err := s.encryptor.Encrypt([]byte(r.content), aad)
			if err != nil {
				tx.Rollback()
				return fmt.Errorf("storage: encrypt migration: message %s: content: %w", r.id, err)
			}
			encContent := base64.StdEncoding.EncodeToString(contentCt)

			encToolCalls := ""
			if r.toolCalls != "" {
				tcCt, err := s.encryptor.Encrypt([]byte(r.toolCalls), aad)
				if err != nil {
					tx.Rollback()
					return fmt.Errorf("storage: encrypt migration: message %s: tool_calls: %w", r.id, err)
				}
				encToolCalls = base64.StdEncoding.EncodeToString(tcCt)
			}

			if _, err := tx.ExecContext(ctx,
				`UPDATE messages SET content = ?, tool_calls = ?, encrypted = 1 WHERE id = ? AND encrypted = 0`,
				encContent, encToolCalls, r.id,
			); err != nil {
				tx.Rollback()
				return fmt.Errorf("storage: encrypt migration: update message %s: %w", r.id, err)
			}
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("storage: commit encrypt migration: %w", err)
		}

		totalMigrated += len(batch)
		log.Printf("storage: encrypted %d messages (%d remaining)", totalMigrated, remaining-len(batch))
	}

	return nil
}
