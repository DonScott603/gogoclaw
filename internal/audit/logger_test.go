package audit

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/DonScott603/gogoclaw/internal/storage"
)

func TestLoggerWritesJSONLines(t *testing.T) {
	var buf bytes.Buffer
	l := NewLoggerFromWriter(&buf)

	l.Log(EventToolCall, map[string]string{
		"tool":   "file_read",
		"result": "ok",
	})

	output := buf.String()
	if !strings.HasSuffix(output, "\n") {
		t.Error("expected newline-terminated output")
	}

	var e Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &e); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if e.Type != EventToolCall {
		t.Errorf("event type = %q, want %q", e.Type, EventToolCall)
	}
	if e.Fields["tool"] != "file_read" {
		t.Errorf("tool = %q, want %q", e.Fields["tool"], "file_read")
	}
	if e.Timestamp.IsZero() {
		t.Error("timestamp should not be zero")
	}
}

func TestLoggerMultipleEvents(t *testing.T) {
	var buf bytes.Buffer
	l := NewLoggerFromWriter(&buf)

	l.LogLLMRequest("minimax", "MiniMax-M2.1", 100, 200, false, "base")
	l.LogToolCall("file_read", "core", "ok", 12)
	l.LogNetworkBlocked("evil.com", "skill:csv", "not_in_allowlist")
	l.LogPIIDetected([]string{"ssn", "phone"}, "strict", "blocked")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d", len(lines))
	}

	// Verify each line is valid JSON.
	for i, line := range lines {
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Errorf("line %d: invalid JSON: %v", i, err)
		}
	}
}

func TestLoggerDisabled(t *testing.T) {
	l, err := NewLogger(LoggerConfig{Enabled: false})
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}

	// Should not panic when logging to a disabled logger.
	l.Log(EventToolCall, map[string]string{"tool": "test"})
}

func newTestEncryptor(t *testing.T) *storage.Encryptor {
	t.Helper()
	key, err := storage.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	enc, err := storage.NewEncryptorFromKey(key)
	if err != nil {
		t.Fatalf("NewEncryptorFromKey: %v", err)
	}
	return enc
}

func TestEncryptedAuditLog(t *testing.T) {
	var buf bytes.Buffer
	l := NewLoggerFromWriter(&buf)
	enc := newTestEncryptor(t)
	l.SetEncryptor(enc)

	l.Log(EventToolCall, map[string]string{
		"tool":   "file_read",
		"result": "ok",
	})

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	if !strings.HasPrefix(lines[0], "enc:v1:") {
		t.Fatalf("expected enc:v1: prefix, got %q", lines[0])
	}

	// Decrypt and verify.
	e, err := DecryptAuditLine([]byte(lines[0]), enc)
	if err != nil {
		t.Fatalf("DecryptAuditLine: %v", err)
	}
	if e.Type != EventToolCall {
		t.Errorf("event type = %q, want %q", e.Type, EventToolCall)
	}
	if e.Fields["tool"] != "file_read" {
		t.Errorf("tool = %q, want %q", e.Fields["tool"], "file_read")
	}
}

func TestDecryptAuditLine(t *testing.T) {
	var buf bytes.Buffer
	l := NewLoggerFromWriter(&buf)
	enc := newTestEncryptor(t)
	l.SetEncryptor(enc)

	l.Log(EventLLMRequest, map[string]string{
		"provider": "openai",
		"model":    "gpt-4",
	})

	line := bytes.TrimSpace(buf.Bytes())
	e, err := DecryptAuditLine(line, enc)
	if err != nil {
		t.Fatalf("DecryptAuditLine: %v", err)
	}
	if e.Type != EventLLMRequest {
		t.Errorf("event type = %q, want %q", e.Type, EventLLMRequest)
	}
	if e.Fields["provider"] != "openai" {
		t.Errorf("provider = %q, want %q", e.Fields["provider"], "openai")
	}
}

func TestDecryptAuditLineMissingPrefix(t *testing.T) {
	enc := newTestEncryptor(t)

	// Plaintext JSON line should be rejected.
	plainJSON := []byte(`{"ts":"2026-01-01T00:00:00Z","event":"tool_call","fields":{"tool":"test"}}`)
	_, err := DecryptAuditLine(plainJSON, enc)
	if err == nil {
		t.Error("expected error when decrypting plaintext line without enc:v1: prefix")
	}
}

func TestMixedAuditLines(t *testing.T) {
	var buf bytes.Buffer
	l := NewLoggerFromWriter(&buf)

	// Write plaintext events first.
	l.Log(EventToolCall, map[string]string{"tool": "plain1"})
	l.Log(EventToolCall, map[string]string{"tool": "plain2"})

	// Enable encryption.
	enc := newTestEncryptor(t)
	l.SetEncryptor(enc)

	// Write encrypted events.
	l.Log(EventToolCall, map[string]string{"tool": "encrypted1"})
	l.Log(EventToolCall, map[string]string{"tool": "encrypted2"})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d", len(lines))
	}

	// First two should be plaintext JSON.
	for i := 0; i < 2; i++ {
		if strings.HasPrefix(lines[i], "enc:v1:") {
			t.Errorf("line %d should be plaintext, got encrypted", i)
		}
		var e Event
		if err := json.Unmarshal([]byte(lines[i]), &e); err != nil {
			t.Errorf("line %d: invalid JSON: %v", i, err)
		}
	}

	// Last two should be encrypted.
	for i := 2; i < 4; i++ {
		if !strings.HasPrefix(lines[i], "enc:v1:") {
			t.Errorf("line %d should be encrypted, got %q", i, lines[i][:20])
		}
		e, err := DecryptAuditLine([]byte(lines[i]), enc)
		if err != nil {
			t.Errorf("line %d: DecryptAuditLine: %v", i, err)
		}
		if e.Type != EventToolCall {
			t.Errorf("line %d: event type = %q, want %q", i, e.Type, EventToolCall)
		}
	}
}

func TestEncryptorFromFirstEvent(t *testing.T) {
	// Simulates the fixed startup path: encryptor attached before first event.
	var buf bytes.Buffer
	l := NewLoggerFromWriter(&buf)
	enc := newTestEncryptor(t)

	// Attach encryptor BEFORE any events are logged — this is the invariant.
	l.SetEncryptor(enc)

	// Emit events that would happen during startup.
	l.Log(EventConfigChanged, map[string]string{"change": "security init"})
	l.Log(EventToolCall, map[string]string{"tool": "startup_check"})
	l.Log(EventLLMRequest, map[string]string{"provider": "test", "model": "test"})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}

	// ALL lines must be encrypted — no plaintext leakage.
	for i, line := range lines {
		if !strings.HasPrefix(line, "enc:v1:") {
			t.Errorf("line %d is plaintext, expected encrypted: %q", i, line[:min(40, len(line))])
		}
		// Verify decryptable.
		e, err := DecryptAuditLine([]byte(line), enc)
		if err != nil {
			t.Errorf("line %d: DecryptAuditLine: %v", i, err)
		}
		if e.Timestamp.IsZero() {
			t.Errorf("line %d: timestamp is zero", i)
		}
	}
}

func TestLoggerToFile(t *testing.T) {
	path := t.TempDir() + "/audit.jsonl"
	l, err := NewLogger(LoggerConfig{Enabled: true, Path: path})
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}

	l.Log(EventConfigChanged, map[string]string{"change": "test"})
	l.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("audit file should not be empty")
	}

	var e Event
	if err := json.Unmarshal(bytes.TrimSpace(data), &e); err != nil {
		t.Fatalf("invalid JSON in file: %v", err)
	}
	if e.Type != EventConfigChanged {
		t.Errorf("event type = %q, want %q", e.Type, EventConfigChanged)
	}
}
