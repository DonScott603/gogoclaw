package audit

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
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
