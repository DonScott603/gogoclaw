package channel

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DonScott603/gogoclaw/internal/audit"
	"github.com/DonScott603/gogoclaw/internal/config"
	"github.com/DonScott603/gogoclaw/internal/engine"
	"github.com/DonScott603/gogoclaw/pkg/types"
	tele "gopkg.in/telebot.v4"
)

// telegramMaxMessage is the safe limit for a single Telegram message.
// Telegram's hard limit is 4096 chars, but telebot auto-converts text at
// or near that boundary to a .txt file attachment. We use 3500 to provide
// ample margin against any automatic document conversion.
const telegramMaxMessage = 3500

// TelegramChannel implements Channel using the Telegram Bot API.
type TelegramChannel struct {
	cfg          config.ChannelConfig
	engine       *engine.Engine
	auditLogger  *audit.Logger
	inboxDir     string
	allowedUsers map[string]bool // usernames and string user IDs
	bot          *tele.Bot

	mu      sync.Mutex
	handler func(ctx context.Context, msg types.InboundMessage)
}

// TelegramConfig holds the dependencies for creating a Telegram channel.
type TelegramConfig struct {
	Channel     config.ChannelConfig
	Engine      *engine.Engine
	AuditLogger *audit.Logger
	InboxDir    string
}

// NewTelegram creates a new Telegram bot channel.
func NewTelegram(cfg TelegramConfig) (*TelegramChannel, error) {
	token := ""
	if cfg.Channel.TokenEnv != "" {
		token = os.Getenv(cfg.Channel.TokenEnv)
	}
	if token == "" {
		return nil, fmt.Errorf("channel: telegram: bot token not set (env var %q is empty)", cfg.Channel.TokenEnv)
	}

	pollTimeout := cfg.Channel.PollingTimeout
	if pollTimeout <= 0 {
		pollTimeout = 10 * time.Second
	}

	bot, err := tele.NewBot(tele.Settings{
		Token:  token,
		Poller: &tele.LongPoller{Timeout: pollTimeout},
	})
	if err != nil {
		return nil, fmt.Errorf("channel: telegram: create bot: %w", err)
	}

	allowed := make(map[string]bool, len(cfg.Channel.AllowedUsers))
	for _, u := range cfg.Channel.AllowedUsers {
		allowed[u] = true
	}

	tc := &TelegramChannel{
		cfg:          cfg.Channel,
		engine:       cfg.Engine,
		auditLogger:  cfg.AuditLogger,
		inboxDir:     cfg.InboxDir,
		allowedUsers: allowed,
		bot:          bot,
	}

	bot.Handle(tele.OnText, tc.onText)
	bot.Handle(tele.OnDocument, tc.onDocument)
	bot.Handle(tele.OnPhoto, tc.onPhoto)

	return tc, nil
}

// Name returns the channel name.
func (tc *TelegramChannel) Name() string { return "telegram" }

// Start begins polling for Telegram updates (blocking).
func (tc *TelegramChannel) Start(_ context.Context) error {
	tc.bot.Start()
	return nil
}

// Stop gracefully stops the Telegram bot.
func (tc *TelegramChannel) Stop(_ context.Context) error {
	tc.bot.Stop()
	return nil
}

// Send sends a message to a Telegram chat, splitting if necessary.
func (tc *TelegramChannel) Send(_ context.Context, conversationID string, msg types.OutboundMessage) error {
	// Parse chat ID from conversation ID (format: telegram-{chatID}).
	chatIDStr := strings.TrimPrefix(conversationID, "telegram-")
	chatID, err := strconv.ParseInt(chatIDStr, 10, 64)
	if err != nil {
		return fmt.Errorf("channel: telegram: invalid conversation ID %q: %w", conversationID, err)
	}

	chat := &tele.Chat{ID: chatID}
	if err := tc.sendChunks(chat, msg.Text); err != nil {
		return err
	}
	// TODO: send outbox files back automatically.
	return nil
}

// OnMessage registers an inbound message handler.
func (tc *TelegramChannel) OnMessage(handler func(ctx context.Context, msg types.InboundMessage)) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.handler = handler
}

// --- internal handlers ---

func (tc *TelegramChannel) onText(c tele.Context) error {
	if !tc.checkAccess(c) {
		return nil
	}

	convID := telegramConversationID(c.Chat().ID)
	tc.engine.SetConversationID(convID)

	tc.notifyHandler(convID, c.Text(), c.Sender())

	resp, err := tc.engine.Send(context.Background(), c.Text())
	if err != nil {
		return c.Send("Error: " + err.Error())
	}

	if err := tc.sendChunks(c.Chat(), resp); err != nil {
		return err
	}
	return nil
}

func (tc *TelegramChannel) onDocument(c tele.Context) error {
	if !tc.checkAccess(c) {
		return nil
	}

	doc := c.Message().Document
	if doc == nil {
		return nil
	}

	if err := tc.downloadFile(&doc.File, doc.FileName); err != nil {
		return c.Send("Failed to save file: " + err.Error())
	}

	text := c.Message().Caption
	if text == "" {
		text = fmt.Sprintf("[uploaded file: %s]", doc.FileName)
	}

	convID := telegramConversationID(c.Chat().ID)
	tc.engine.SetConversationID(convID)
	tc.notifyHandler(convID, text, c.Sender())

	resp, err := tc.engine.Send(context.Background(), text)
	if err != nil {
		return c.Send("Error: " + err.Error())
	}

	if err := tc.sendChunks(c.Chat(), resp); err != nil {
		return err
	}
	return nil
}

func (tc *TelegramChannel) onPhoto(c tele.Context) error {
	if !tc.checkAccess(c) {
		return nil
	}

	photo := c.Message().Photo
	if photo == nil {
		return nil
	}

	filename := fmt.Sprintf("photo_%d.jpg", time.Now().UnixNano())
	if err := tc.downloadFile(&photo.File, filename); err != nil {
		return c.Send("Failed to save photo: " + err.Error())
	}

	text := c.Message().Caption
	if text == "" {
		text = fmt.Sprintf("[uploaded photo: %s]", filename)
	}

	convID := telegramConversationID(c.Chat().ID)
	tc.engine.SetConversationID(convID)
	tc.notifyHandler(convID, text, c.Sender())

	resp, err := tc.engine.Send(context.Background(), text)
	if err != nil {
		return c.Send("Error: " + err.Error())
	}

	if err := tc.sendChunks(c.Chat(), resp); err != nil {
		return err
	}
	return nil
}

// --- helpers ---

// sendChunks splits text and sends each chunk as a plain text message using
// bot.Send with explicit SendOptions to prevent telebot from auto-converting
// long text into .txt file attachments.
func (tc *TelegramChannel) sendChunks(chat *tele.Chat, text string) error {
	for _, chunk := range SplitMessage(text, telegramMaxMessage) {
		if _, err := tc.bot.Send(chat, chunk, &tele.SendOptions{}); err != nil {
			return fmt.Errorf("channel: telegram: send chunk: %w", err)
		}
	}
	return nil
}

// checkAccess validates the message is from a private chat and an allowed user.
// Returns false and sends a rejection if access is denied.
func (tc *TelegramChannel) checkAccess(c tele.Context) bool {
	sender := c.Sender()
	chat := c.Chat()

	senderName := ""
	senderID := ""
	if sender != nil {
		senderName = sender.Username
		senderID = strconv.FormatInt(sender.ID, 10)
	}

	// Reject group chats for security.
	if chat != nil && isGroupChat(string(chat.Type)) {
		tc.logMessage(senderName, senderID, c.Text(), "rejected_group")
		c.Send("I only respond in private/DM conversations.")
		return false
	}

	// Check allowed users list.
	if !tc.isUserAllowed(senderName, senderID) {
		tc.logMessage(senderName, senderID, c.Text(), "rejected_user")
		c.Send("You are not authorized to use this bot.")
		return false
	}

	tc.logMessage(senderName, senderID, c.Text(), "allowed")
	return true
}

// isUserAllowed returns true if the user is permitted. Empty list allows all.
func (tc *TelegramChannel) isUserAllowed(username, userID string) bool {
	if len(tc.allowedUsers) == 0 {
		return true
	}
	return tc.allowedUsers[username] || tc.allowedUsers[userID]
}

// isGroupChat returns true if the chat type is not a private/DM chat.
func isGroupChat(chatType string) bool {
	return chatType != "private"
}

func (tc *TelegramChannel) logMessage(username, userID, text, action string) {
	if tc.auditLogger == nil {
		return
	}
	tc.auditLogger.Log(audit.EventTelegramMessage, map[string]string{
		"username": username,
		"user_id":  userID,
		"action":   action,
	})
}

func (tc *TelegramChannel) notifyHandler(convID, text string, sender *tele.User) {
	tc.mu.Lock()
	h := tc.handler
	tc.mu.Unlock()
	if h == nil {
		return
	}

	senderID := ""
	if sender != nil {
		senderID = strconv.FormatInt(sender.ID, 10)
	}

	h(context.Background(), types.InboundMessage{
		ConversationID: convID,
		SenderID:       senderID,
		Text:           text,
		Channel:        "telegram",
		Timestamp:      time.Now(),
	})
}

func (tc *TelegramChannel) downloadFile(file *tele.File, filename string) error {
	if tc.inboxDir == "" {
		return nil
	}

	reader, err := tc.bot.File(file)
	if err != nil {
		return fmt.Errorf("get file: %w", err)
	}
	defer reader.Close()

	name := filepath.Base(filename)
	if name == "." || name == "/" {
		name = "upload"
	}

	destPath := filepath.Join(tc.inboxDir, name)
	dst, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, reader); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}

// telegramConversationID returns a GoGoClaw conversation ID for a Telegram chat.
func telegramConversationID(chatID int64) string {
	return fmt.Sprintf("telegram-%d", chatID)
}

// SplitMessage splits text into chunks of at most maxLen characters,
// breaking at paragraph or sentence boundaries when possible.
func SplitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			chunks = append(chunks, text)
			break
		}

		chunk := text[:maxLen]
		splitAt := maxLen

		// Try to split at paragraph boundary.
		if idx := strings.LastIndex(chunk, "\n\n"); idx > maxLen/4 {
			splitAt = idx + 2
		} else if idx := strings.LastIndex(chunk, "\n"); idx > maxLen/4 {
			splitAt = idx + 1
		} else if idx := strings.LastIndex(chunk, ". "); idx > maxLen/4 {
			splitAt = idx + 2
		} else if idx := strings.LastIndex(chunk, " "); idx > maxLen/4 {
			splitAt = idx + 1
		}

		chunks = append(chunks, strings.TrimRight(text[:splitAt], " "))
		text = text[splitAt:]
	}

	return chunks
}
