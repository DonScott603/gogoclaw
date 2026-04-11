// Package storage implements SQLite-backed conversation persistence.
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	db       *sql.DB
	scrubber Scrubber
	onScrub  ScrubNotifyFn
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
func (s *Store) AddMessage(ctx context.Context, m StoredMessage) error {
	// Scrub secrets from message content before persisting.
	if s.scrubber != nil && s.scrubber.HasSecrets(m.Content) {
		m.Content = s.scrubber.Scrub(m.Content)
		if s.onScrub != nil {
			s.onScrub("storage", "secrets scrubbed before SQLite persistence")
		}
	}

	toolCallsJSON := ""
	if m.ToolCalls != nil {
		toolCallsJSON = string(m.ToolCalls)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (id, conversation_id, role, content, tool_calls, tool_call_id, token_count, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.ConversationID, m.Role, m.Content, toolCallsJSON, m.ToolCallID, m.TokenCount, m.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("storage: add message: %w", err)
	}
	// Touch conversation updated_at.
	s.db.ExecContext(ctx,
		`UPDATE conversations SET updated_at = datetime('now') WHERE id = ?`, m.ConversationID)
	return nil
}

// GetMessages retrieves all messages for a conversation in chronological order.
func (s *Store) GetMessages(ctx context.Context, conversationID string) ([]StoredMessage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, conversation_id, role, content, tool_calls, tool_call_id, token_count, created_at
		 FROM messages WHERE conversation_id = ? ORDER BY created_at ASC`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("storage: get messages: %w", err)
	}
	defer rows.Close()

	var msgs []StoredMessage
	for rows.Next() {
		var m StoredMessage
		var toolCalls string
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &toolCalls, &m.ToolCallID, &m.TokenCount, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("storage: scan message: %w", err)
		}
		if toolCalls != "" {
			m.ToolCalls = json.RawMessage(toolCalls)
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
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
