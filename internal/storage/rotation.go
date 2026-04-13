package storage

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

const rotationBatchSize = 100

// RotateConfig holds parameters for a key rotation operation.
type RotateConfig struct {
	OldEncryptor *Encryptor
	NewEncryptor *Encryptor
	DBPath       string
	AuditPath    string // empty string = skip audit rotation
}

// RotateResult holds counts from a completed rotation.
type RotateResult struct {
	MessagesRotated      int // encrypted rows re-encrypted from old to new key
	MessagesSkipped      int // rows already under new key (from a previous partial run)
	PlaintextEncrypted   int // plaintext rows encrypted with new key
	AuditLinesRotated    int // enc:v1: lines re-encrypted
	AuditLinesPassedThru int // plaintext audit lines left unchanged
}

// RotateKeys re-encrypts all data from OldEncryptor to NewEncryptor.
// It processes the SQLite database first, then the audit log.
//
// Each SQLite batch is atomic. The audit log rewrite is atomic via temp-file rename.
// The overall operation is resumable but not globally all-or-nothing.
// If failure occurs mid-rotation, re-running with the same encryptors resumes safely.
func RotateKeys(ctx context.Context, cfg RotateConfig) (*RotateResult, error) {
	if cfg.OldEncryptor == nil || cfg.NewEncryptor == nil {
		return nil, fmt.Errorf("rotation: old and new encryptors must not be nil")
	}
	if cfg.OldEncryptor.KeyEquals(cfg.NewEncryptor) {
		return nil, fmt.Errorf("rotation: old and new keys are identical")
	}

	result := &RotateResult{}

	// --- SQLite rotation ---
	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("rotation: open db: %w", err)
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		return nil, fmt.Errorf("rotation: set WAL mode: %w", err)
	}

	var totalMessages int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages`).Scan(&totalMessages); err != nil {
		return nil, fmt.Errorf("rotation: count total messages: %w", err)
	}
	if totalMessages == 0 {
		return nil, fmt.Errorf("rotation: no messages to rotate")
	}

	// Pass 1: re-encrypt encrypted rows.
	if err := rotateEncryptedRows(ctx, db, cfg.OldEncryptor, cfg.NewEncryptor, result); err != nil {
		return nil, err
	}

	// Pass 2: encrypt plaintext rows.
	if err := encryptPlaintextRows(ctx, db, cfg.NewEncryptor, result); err != nil {
		return nil, err
	}

	// --- Audit log rotation ---
	if err := rotateAuditLog(cfg.AuditPath, cfg.OldEncryptor, cfg.NewEncryptor, result); err != nil {
		return nil, err
	}

	return result, nil
}

// rotateEncryptedRows processes rows where encrypted = 1, re-encrypting from old to new key.
// Uses try-both-keys for resumability after partial prior runs.
// Pagination uses OFFSET since re-encrypting a row does not change its encrypted flag.
func rotateEncryptedRows(ctx context.Context, db *sql.DB, oldEnc, newEnc *Encryptor, result *RotateResult) error {
	offset := 0
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("rotation: context canceled: %w", err)
		}

		rows, err := db.QueryContext(ctx,
			`SELECT id, conversation_id, role, content, tool_calls
			 FROM messages WHERE encrypted = 1
			 ORDER BY created_at ASC, id ASC LIMIT ? OFFSET ?`, rotationBatchSize, offset)
		if err != nil {
			return fmt.Errorf("rotation: query encrypted messages: %w", err)
		}

		type rowData struct {
			id, convID, role, content, toolCalls string
		}
		var batch []rowData
		for rows.Next() {
			var r rowData
			if err := rows.Scan(&r.id, &r.convID, &r.role, &r.content, &r.toolCalls); err != nil {
				rows.Close()
				return fmt.Errorf("rotation: scan encrypted message: %w", err)
			}
			batch = append(batch, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("rotation: iterate encrypted messages: %w", err)
		}

		if len(batch) == 0 {
			break
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("rotation: begin re-encrypt tx: %w", err)
		}

		batchRotated := 0
		batchSkipped := 0

		for _, r := range batch {
			if err := ctx.Err(); err != nil {
				tx.Rollback()
				return fmt.Errorf("rotation: context canceled: %w", err)
			}

			aad := BuildMessageAAD(r.id, r.convID, r.role)

			// Determine which key decrypts content.
			contentKey, contentPlain, err := tryBothKeys(r.content, aad, oldEnc, newEnc, r.id, "content")
			if err != nil {
				tx.Rollback()
				return err
			}

			// Determine which key decrypts tool_calls (if present).
			var toolCallsKey string // "old", "new", or "none"
			var toolCallsPlain []byte
			if r.toolCalls != "" {
				toolCallsKey, toolCallsPlain, err = tryBothKeys(r.toolCalls, aad, oldEnc, newEnc, r.id, "tool_calls")
				if err != nil {
					tx.Rollback()
					return err
				}
			} else {
				toolCallsKey = "none"
			}

			// Consistency check: content and tool_calls must decrypt under same key.
			if toolCallsKey != "none" && contentKey != toolCallsKey {
				tx.Rollback()
				return fmt.Errorf("rotation: message %s has inconsistent encryption state: content decrypts with %s key but tool_calls decrypts with %s key", r.id, contentKey, toolCallsKey)
			}

			// If already under new key, skip.
			if contentKey == "new" {
				batchSkipped++
				continue
			}

			// Re-encrypt with new key.
			newContentCt, err := newEnc.Encrypt(contentPlain, aad)
			if err != nil {
				tx.Rollback()
				return fmt.Errorf("rotation: re-encrypt content for message %s: %w", r.id, err)
			}
			newContentB64 := base64.StdEncoding.EncodeToString(newContentCt)

			newToolCallsB64 := ""
			if toolCallsKey != "none" {
				newTCCt, err := newEnc.Encrypt(toolCallsPlain, aad)
				if err != nil {
					tx.Rollback()
					return fmt.Errorf("rotation: re-encrypt tool_calls for message %s: %w", r.id, err)
				}
				newToolCallsB64 = base64.StdEncoding.EncodeToString(newTCCt)
			}

			if _, err := tx.ExecContext(ctx,
				`UPDATE messages SET content = ?, tool_calls = ?, encrypted = 1 WHERE id = ?`,
				newContentB64, newToolCallsB64, r.id,
			); err != nil {
				tx.Rollback()
				return fmt.Errorf("rotation: update message %s: %w", r.id, err)
			}
			batchRotated++
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("rotation: commit re-encrypt batch: %w", err)
		}

		result.MessagesRotated += batchRotated
		result.MessagesSkipped += batchSkipped
		offset += len(batch)

		log.Printf("rotate: re-encrypted %d messages, skipped %d (already rotated)",
			result.MessagesRotated, result.MessagesSkipped)
	}

	return nil
}

// tryBothKeys attempts to base64-decode and decrypt a field value with the old key first,
// then the new key. Returns which key succeeded ("old" or "new") and the plaintext.
func tryBothKeys(b64Value string, aad []byte, oldEnc, newEnc *Encryptor, msgID, field string) (string, []byte, error) {
	ct, err := base64.StdEncoding.DecodeString(b64Value)
	if err != nil {
		return "", nil, fmt.Errorf("rotation: message %s: %s: invalid base64: %w", msgID, field, err)
	}

	// Try old key first.
	plain, err := oldEnc.Decrypt(ct, aad)
	if err == nil {
		return "old", plain, nil
	}

	// Try new key (already rotated in previous partial run).
	plain, err = newEnc.Decrypt(ct, aad)
	if err == nil {
		return "new", plain, nil
	}

	return "", nil, fmt.Errorf("rotation: message %s: %s: neither old nor new key can decrypt", msgID, field)
}

// encryptPlaintextRows processes rows where encrypted = 0, encrypting with the new key.
func encryptPlaintextRows(ctx context.Context, db *sql.DB, newEnc *Encryptor, result *RotateResult) error {
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("rotation: context canceled: %w", err)
		}

		rows, err := db.QueryContext(ctx,
			`SELECT id, conversation_id, role, content, tool_calls
			 FROM messages WHERE encrypted = 0
			 ORDER BY created_at ASC, id ASC LIMIT ?`, rotationBatchSize)
		if err != nil {
			return fmt.Errorf("rotation: query plaintext messages: %w", err)
		}

		type rowData struct {
			id, convID, role, content, toolCalls string
		}
		var batch []rowData
		for rows.Next() {
			var r rowData
			if err := rows.Scan(&r.id, &r.convID, &r.role, &r.content, &r.toolCalls); err != nil {
				rows.Close()
				return fmt.Errorf("rotation: scan plaintext message: %w", err)
			}
			batch = append(batch, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("rotation: iterate plaintext messages: %w", err)
		}

		if len(batch) == 0 {
			break
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("rotation: begin plaintext encrypt tx: %w", err)
		}

		for _, r := range batch {
			if err := ctx.Err(); err != nil {
				tx.Rollback()
				return fmt.Errorf("rotation: context canceled: %w", err)
			}

			aad := BuildMessageAAD(r.id, r.convID, r.role)

			contentCt, err := newEnc.Encrypt([]byte(r.content), aad)
			if err != nil {
				tx.Rollback()
				return fmt.Errorf("rotation: encrypt plaintext content for message %s: %w", r.id, err)
			}
			encContent := base64.StdEncoding.EncodeToString(contentCt)

			encToolCalls := ""
			if r.toolCalls != "" {
				tcCt, err := newEnc.Encrypt([]byte(r.toolCalls), aad)
				if err != nil {
					tx.Rollback()
					return fmt.Errorf("rotation: encrypt plaintext tool_calls for message %s: %w", r.id, err)
				}
				encToolCalls = base64.StdEncoding.EncodeToString(tcCt)
			}

			if _, err := tx.ExecContext(ctx,
				`UPDATE messages SET content = ?, tool_calls = ?, encrypted = 1 WHERE id = ?`,
				encContent, encToolCalls, r.id,
			); err != nil {
				tx.Rollback()
				return fmt.Errorf("rotation: update plaintext message %s: %w", r.id, err)
			}
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("rotation: commit plaintext encrypt batch: %w", err)
		}

		result.PlaintextEncrypted += len(batch)
		log.Printf("rotate: encrypted %d plaintext messages", result.PlaintextEncrypted)
	}

	return nil
}

// rotateAuditLog re-encrypts encrypted audit lines and passes through plaintext lines.
// Uses temp-file + rename for atomicity.
func rotateAuditLog(auditPath string, oldEnc, newEnc *Encryptor, result *RotateResult) error {
	if auditPath == "" {
		return nil
	}

	if _, err := os.Stat(auditPath); os.IsNotExist(err) {
		return nil
	}

	f, err := os.Open(auditPath)
	if err != nil {
		return fmt.Errorf("rotation: open audit log: %w", err)
	}
	defer f.Close()

	tmpPath := auditPath + ".rotating"
	tmp, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("rotation: create audit temp file: %w", err)
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	writer := bufio.NewWriter(tmp)
	var writeErr error

	for scanner.Scan() {
		line := scanner.Text()

		if strings.TrimSpace(line) == "" {
			if _, writeErr = writer.WriteString(line + "\n"); writeErr != nil {
				break
			}
			continue
		}

		if strings.HasPrefix(line, "enc:v1:") {
			encoded := line[len("enc:v1:"):]
			ct, decErr := base64.StdEncoding.DecodeString(encoded)
			if decErr != nil {
				writeErr = fmt.Errorf("rotation: audit line: base64 decode: %w", decErr)
				break
			}
			plain, decErr := oldEnc.Decrypt(ct, nil)
			if decErr != nil {
				writeErr = fmt.Errorf("rotation: audit line: decrypt with old key: %w", decErr)
				break
			}
			newCt, encErr := newEnc.Encrypt(plain, nil)
			if encErr != nil {
				writeErr = fmt.Errorf("rotation: audit line: re-encrypt: %w", encErr)
				break
			}
			newEncoded := base64.StdEncoding.EncodeToString(newCt)
			if _, writeErr = writer.WriteString("enc:v1:" + newEncoded + "\n"); writeErr != nil {
				break
			}
			result.AuditLinesRotated++
		} else {
			if _, writeErr = writer.WriteString(line + "\n"); writeErr != nil {
				break
			}
			result.AuditLinesPassedThru++
		}
	}

	if scanErr := scanner.Err(); scanErr != nil && writeErr == nil {
		writeErr = fmt.Errorf("rotation: scan audit log: %w", scanErr)
	}

	writer.Flush()
	tmp.Close()
	f.Close()

	if writeErr != nil {
		os.Remove(tmpPath)
		result.AuditLinesRotated = 0
		result.AuditLinesPassedThru = 0
		return writeErr
	}

	if err := os.Rename(tmpPath, auditPath); err != nil {
		os.Remove(tmpPath)
		result.AuditLinesRotated = 0
		result.AuditLinesPassedThru = 0
		return fmt.Errorf("rotation: rename audit temp file: %w", err)
	}

	return nil
}

// RotationStats provides counts for pre-rotation planning.
type RotationStats struct {
	TotalMessages       int
	EncryptedMessages   int
	PlaintextMessages   int
	AuditLines          int
	EncryptedAuditLines int
}

// GetRotationStats returns counts for pre-rotation planning.
// If the DB or audit file doesn't exist, returns zero counts for that component.
func GetRotationStats(ctx context.Context, dbPath, auditPath string) (*RotationStats, error) {
	stats := &RotationStats{}

	if _, err := os.Stat(dbPath); err == nil {
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			return nil, fmt.Errorf("rotation: open db for stats: %w", err)
		}
		defer db.Close()

		if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
			return nil, fmt.Errorf("rotation: set WAL mode for stats: %w", err)
		}

		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages`).Scan(&stats.TotalMessages); err != nil {
			return nil, fmt.Errorf("rotation: count total messages: %w", err)
		}
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE encrypted = 1`).Scan(&stats.EncryptedMessages); err != nil {
			return nil, fmt.Errorf("rotation: count encrypted messages: %w", err)
		}
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE encrypted = 0`).Scan(&stats.PlaintextMessages); err != nil {
			return nil, fmt.Errorf("rotation: count plaintext messages: %w", err)
		}
	}

	if auditPath != "" {
		if _, err := os.Stat(auditPath); err == nil {
			f, err := os.Open(auditPath)
			if err != nil {
				return nil, fmt.Errorf("rotation: open audit log for stats: %w", err)
			}
			defer f.Close()

			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
			for scanner.Scan() {
				line := scanner.Text()
				if strings.TrimSpace(line) == "" {
					continue
				}
				stats.AuditLines++
				if strings.HasPrefix(line, "enc:v1:") {
					stats.EncryptedAuditLines++
				}
			}
			if err := scanner.Err(); err != nil {
				return nil, fmt.Errorf("rotation: scan audit log for stats: %w", err)
			}
		}
	}

	return stats, nil
}
