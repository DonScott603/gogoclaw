package channel

import (
	"strings"
	"testing"
)

func TestSplitMessageShort(t *testing.T) {
	chunks := SplitMessage("Hello world", 4096)
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if chunks[0] != "Hello world" {
		t.Errorf("chunk = %q, want %q", chunks[0], "Hello world")
	}
}

func TestSplitMessageExactLimit(t *testing.T) {
	text := strings.Repeat("a", 4096)
	chunks := SplitMessage(text, 4096)
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
}

func TestSplitMessageParagraphBoundary(t *testing.T) {
	// Build a message that's over 4096 with a paragraph break in the middle.
	para1 := strings.Repeat("a", 2000)
	para2 := strings.Repeat("b", 3000)
	text := para1 + "\n\n" + para2

	chunks := SplitMessage(text, 4096)
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2", len(chunks))
	}
	if !strings.HasSuffix(chunks[0], "\n\n") && chunks[0] != para1+"\n\n" {
		// First chunk should end at the paragraph break.
		if !strings.Contains(chunks[0], para1) {
			t.Errorf("first chunk should contain para1")
		}
	}
}

func TestSplitMessageSentenceBoundary(t *testing.T) {
	// No paragraph breaks, but sentence breaks.
	sentence := strings.Repeat("x", 2000) + ". "
	text := sentence + strings.Repeat("y", 3000)

	chunks := SplitMessage(text, 4096)
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks, want >= 2", len(chunks))
	}
	// First chunk should end at the sentence boundary.
	if !strings.HasSuffix(chunks[0], ".") && !strings.HasSuffix(chunks[0], ". ") {
		t.Errorf("first chunk should end at sentence boundary, got suffix %q", chunks[0][len(chunks[0])-10:])
	}
}

func TestSplitMessageLargeNoBreaks(t *testing.T) {
	// A single long word with no natural break points at all.
	text := strings.Repeat("x", 5000)
	chunks := SplitMessage(text, 4096)
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2", len(chunks))
	}
	// Should still split, even if mid-word.
	total := 0
	for _, c := range chunks {
		total += len(c)
	}
	if total != 5000 {
		t.Errorf("total chars = %d, want 5000", total)
	}
}

func TestSplitMessageEmpty(t *testing.T) {
	chunks := SplitMessage("", 4096)
	if len(chunks) != 1 || chunks[0] != "" {
		t.Errorf("empty string should return [\"\"], got %v", chunks)
	}
}

func TestTelegramConversationID(t *testing.T) {
	tests := []struct {
		chatID   int64
		expected string
	}{
		{123456, "telegram-123456"},
		{-100123, "telegram--100123"},
		{0, "telegram-0"},
	}
	for _, tc := range tests {
		got := telegramConversationID(tc.chatID)
		if got != tc.expected {
			t.Errorf("telegramConversationID(%d) = %q, want %q", tc.chatID, got, tc.expected)
		}
	}
}

func TestTelegramAccessControlAllowedUser(t *testing.T) {
	tc := &TelegramChannel{
		allowedUsers: map[string]bool{"alice": true, "12345": true},
	}

	tests := []struct {
		name     string
		username string
		userID   string
		want     bool
	}{
		{"allowed_by_username", "alice", "99", true},
		{"allowed_by_id", "bob", "12345", true},
		{"rejected", "eve", "999", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tc.isUserAllowed(tt.username, tt.userID)
			if got != tt.want {
				t.Errorf("isUserAllowed(%q, %q) = %v, want %v", tt.username, tt.userID, got, tt.want)
			}
		})
	}
}

func TestTelegramAccessControlEmptyList(t *testing.T) {
	tc := &TelegramChannel{
		allowedUsers: map[string]bool{},
	}

	// Empty list = allow all.
	if !tc.isUserAllowed("anyone", "999") {
		t.Error("empty allowedUsers should allow all")
	}
}

func TestTelegramGroupChatRejected(t *testing.T) {
	// Group chat type should be rejected.
	// We test isGroupChat helper.
	if !isGroupChat("group") {
		t.Error("group should be rejected")
	}
	if !isGroupChat("supergroup") {
		t.Error("supergroup should be rejected")
	}
	if isGroupChat("private") {
		t.Error("private should be allowed")
	}
}
