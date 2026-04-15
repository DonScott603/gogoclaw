package channel

import (
	"strings"
	"testing"
	"time"

	"github.com/DonScott603/gogoclaw/internal/config"
	tele "gopkg.in/telebot.v4"
)

func TestSplitMessageShort(t *testing.T) {
	chunks := SplitMessage("Hello world", telegramMaxMessage)
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if chunks[0] != "Hello world" {
		t.Errorf("chunk = %q, want %q", chunks[0], "Hello world")
	}
}

func TestSplitMessageExactLimit(t *testing.T) {
	text := strings.Repeat("a", telegramMaxMessage)
	chunks := SplitMessage(text, telegramMaxMessage)
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
}

func TestSplitMessageParagraphBoundary(t *testing.T) {
	// Build a message that's over the limit with a paragraph break in the middle.
	para1 := strings.Repeat("a", 2000)
	para2 := strings.Repeat("b", 3000)
	text := para1 + "\n\n" + para2

	chunks := SplitMessage(text, telegramMaxMessage)
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

	chunks := SplitMessage(text, telegramMaxMessage)
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
	chunks := SplitMessage(text, telegramMaxMessage)
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
	chunks := SplitMessage("", telegramMaxMessage)
	if len(chunks) != 1 || chunks[0] != "" {
		t.Errorf("empty string should return [\"\"], got %v", chunks)
	}
}

func TestSplitMessageChunksUnderLimit(t *testing.T) {
	// Verify every chunk produced by SplitMessage is strictly under 4096
	// so telebot never auto-converts to a .txt file attachment.
	text := strings.Repeat("word ", 1500) // ~7500 chars
	chunks := SplitMessage(text, telegramMaxMessage)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if len(c) > telegramMaxMessage {
			t.Errorf("chunk %d has %d chars, exceeds limit %d", i, len(c), telegramMaxMessage)
		}
		if len(c) >= 4096 {
			t.Errorf("chunk %d has %d chars, would trigger telebot file attachment (>= 4096)", i, len(c))
		}
	}
	// Verify no significant content is lost (trailing spaces may be trimmed at split points).
	reassembled := strings.Join(chunks, "")
	if len(reassembled) < len(text)-len(chunks) {
		t.Errorf("reassembled length = %d, want at least %d", len(reassembled), len(text)-len(chunks))
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

	// Empty list = deny all (fail closed).
	if tc.isUserAllowed("anyone", "999") {
		t.Error("empty allowedUsers should deny all (fail closed)")
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

func TestResolveTelegramPollerLongPolling(t *testing.T) {
	// Empty WebhookURL → long-polling mode.
	poller, webhookMode := resolveTelegramPoller(config.ChannelConfig{})
	if webhookMode {
		t.Error("expected webhookMode=false for empty WebhookURL")
	}
	lp, ok := poller.(*tele.LongPoller)
	if !ok {
		t.Fatalf("expected *tele.LongPoller, got %T", poller)
	}
	// Default timeout.
	if lp.Timeout != 10*time.Second {
		t.Errorf("default timeout = %v, want 10s", lp.Timeout)
	}

	// Custom timeout.
	poller2, _ := resolveTelegramPoller(config.ChannelConfig{PollingTimeout: 30 * time.Second})
	lp2 := poller2.(*tele.LongPoller)
	if lp2.Timeout != 30*time.Second {
		t.Errorf("custom timeout = %v, want 30s", lp2.Timeout)
	}
}

func TestResolveTelegramPollerWebhook(t *testing.T) {
	poller, webhookMode := resolveTelegramPoller(config.ChannelConfig{
		WebhookURL: "https://example.com/webhook",
	})
	if !webhookMode {
		t.Error("expected webhookMode=true")
	}
	wh, ok := poller.(*tele.Webhook)
	if !ok {
		t.Fatalf("expected *tele.Webhook, got %T", poller)
	}
	// Default listen address.
	if wh.Listen != ":8443" {
		t.Errorf("default listen = %q, want ':8443'", wh.Listen)
	}
	if wh.Endpoint == nil || wh.Endpoint.PublicURL != "https://example.com/webhook" {
		t.Errorf("PublicURL not set correctly")
	}

	// Custom listen address.
	poller2, _ := resolveTelegramPoller(config.ChannelConfig{
		WebhookURL:    "https://example.com/wh",
		WebhookListen: ":9443",
	})
	wh2 := poller2.(*tele.Webhook)
	if wh2.Listen != ":9443" {
		t.Errorf("custom listen = %q, want ':9443'", wh2.Listen)
	}
}

func TestResolveTelegramPollerWebhookWithTLS(t *testing.T) {
	// Cert + key → both Endpoint.Cert and TLS populated.
	poller, _ := resolveTelegramPoller(config.ChannelConfig{
		WebhookURL:      "https://example.com/webhook",
		WebhookCertFile: "/path/cert.pem",
		WebhookKeyFile:  "/path/key.pem",
	})
	wh := poller.(*tele.Webhook)
	if wh.Endpoint.Cert != "/path/cert.pem" {
		t.Errorf("Endpoint.Cert = %q, want '/path/cert.pem'", wh.Endpoint.Cert)
	}
	if wh.TLS == nil {
		t.Fatal("expected TLS to be set")
	}
	if wh.TLS.Cert != "/path/cert.pem" || wh.TLS.Key != "/path/key.pem" {
		t.Errorf("TLS = {Cert:%q, Key:%q}, want cert.pem/key.pem", wh.TLS.Cert, wh.TLS.Key)
	}

	// Cert without key → Endpoint.Cert set, TLS nil (for Telegram cert upload only).
	poller2, _ := resolveTelegramPoller(config.ChannelConfig{
		WebhookURL:      "https://example.com/webhook",
		WebhookCertFile: "/path/cert.pem",
	})
	wh2 := poller2.(*tele.Webhook)
	if wh2.Endpoint.Cert != "/path/cert.pem" {
		t.Errorf("Endpoint.Cert = %q, want '/path/cert.pem'", wh2.Endpoint.Cert)
	}
	if wh2.TLS != nil {
		t.Error("TLS should be nil when no key file provided")
	}
}

func TestResolveTelegramPollerWebhookSecret(t *testing.T) {
	poller, _ := resolveTelegramPoller(config.ChannelConfig{
		WebhookURL:    "https://example.com/webhook",
		WebhookSecret: "my-secret-token",
	})
	wh := poller.(*tele.Webhook)
	if wh.SecretToken != "my-secret-token" {
		t.Errorf("SecretToken = %q, want 'my-secret-token'", wh.SecretToken)
	}
}

func TestWebhookHealthyPollingMode(t *testing.T) {
	tc := &TelegramChannel{webhookMode: false}
	healthy, desc := tc.WebhookHealthy()
	if !healthy {
		t.Error("expected healthy=true for polling mode")
	}
	if desc != "long-polling" {
		t.Errorf("desc = %q, want 'long-polling'", desc)
	}
}
