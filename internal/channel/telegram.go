package channel

import (
	"context"
	"fmt"
	"io"
	"log"
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
	cfg            config.ChannelConfig
	engine         *engine.Engine
	sessionManager *engine.SessionManager
	auditLogger    *audit.Logger
	inboxDir       string
	allowedUsers   map[string]bool // usernames and string user IDs
	bot            *tele.Bot
	ctx            context.Context // shutdown context
	webhookMode    bool

	mu      sync.Mutex
	handler func(ctx context.Context, msg types.InboundMessage)
}

// TelegramConfig holds the dependencies for creating a Telegram channel.
type TelegramConfig struct {
	Channel        config.ChannelConfig
	Engine         *engine.Engine
	SessionManager *engine.SessionManager
	AuditLogger    *audit.Logger
	InboxDir       string
	Ctx            context.Context
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

	poller, webhookMode := resolveTelegramPoller(cfg.Channel)

	bot, err := tele.NewBot(tele.Settings{
		Token:  token,
		Poller: poller,
	})
	if err != nil {
		return nil, fmt.Errorf("channel: telegram: create bot: %w", err)
	}

	allowed := make(map[string]bool, len(cfg.Channel.AllowedUsers))
	for _, u := range cfg.Channel.AllowedUsers {
		allowed[u] = true
	}

	shutdownCtx := cfg.Ctx
	if shutdownCtx == nil {
		shutdownCtx = context.Background()
	}

	tc := &TelegramChannel{
		cfg:            cfg.Channel,
		engine:         cfg.Engine,
		sessionManager: cfg.SessionManager,
		auditLogger:    cfg.AuditLogger,
		inboxDir:       cfg.InboxDir,
		allowedUsers:   allowed,
		bot:            bot,
		ctx:            shutdownCtx,
		webhookMode:    webhookMode,
	}

	bot.Handle(tele.OnText, tc.onText)
	bot.Handle(tele.OnDocument, tc.onDocument)
	bot.Handle(tele.OnPhoto, tc.onPhoto)

	return tc, nil
}

// resolveTelegramPoller returns the appropriate poller and whether webhook mode is active.
// This is a pure function of config — no side effects, no network calls.
func resolveTelegramPoller(cfg config.ChannelConfig) (tele.Poller, bool) {
	if cfg.WebhookURL == "" {
		pollTimeout := cfg.PollingTimeout
		if pollTimeout <= 0 {
			pollTimeout = 10 * time.Second
		}
		return &tele.LongPoller{Timeout: pollTimeout}, false
	}

	listen := cfg.WebhookListen
	if listen == "" {
		listen = ":8443"
	}

	wh := &tele.Webhook{
		Listen: listen,
		Endpoint: &tele.WebhookEndpoint{
			PublicURL: cfg.WebhookURL,
		},
	}

	if cfg.WebhookCertFile != "" {
		wh.Endpoint.Cert = cfg.WebhookCertFile
		if cfg.WebhookKeyFile != "" {
			wh.TLS = &tele.WebhookTLS{
				Key:  cfg.WebhookKeyFile,
				Cert: cfg.WebhookCertFile,
			}
		}
	}

	if cfg.WebhookSecret != "" {
		wh.SecretToken = cfg.WebhookSecret
	}

	return wh, true
}

// Name returns the channel name.
func (tc *TelegramChannel) Name() string { return "telegram" }

// Start begins polling or webhook listening for Telegram updates (blocking).
func (tc *TelegramChannel) Start(ctx context.Context) error {
	if tc.auditLogger != nil {
		if tc.webhookMode {
			tc.auditLogger.Log(audit.EventTelegramWebhook, map[string]string{
				"action": "webhook_mode_starting",
				"url":    tc.cfg.WebhookURL,
			})
		} else {
			tc.auditLogger.Log(audit.EventTelegramWebhook, map[string]string{
				"action": "polling_mode_starting",
			})
		}
	}
	go func() {
		<-ctx.Done()
		tc.bot.Stop()
	}()
	tc.bot.Start()
	return nil
}

// Stop gracefully stops the Telegram bot.
func (tc *TelegramChannel) Stop(_ context.Context) error {
	if tc.webhookMode {
		if tc.auditLogger != nil {
			tc.auditLogger.Log(audit.EventTelegramWebhook, map[string]string{
				"action": "remove_webhook_attempted",
			})
		}
		if err := tc.bot.RemoveWebhook(false); err != nil {
			log.Printf("telegram: failed to remove webhook: %v", err)
		}
	}
	tc.bot.Stop()
	return nil
}

// WebhookHealthy reports webhook status. Returns (true, description) if healthy.
// For long-polling mode, always returns healthy with mode description.
func (tc *TelegramChannel) WebhookHealthy() (bool, string) {
	if !tc.webhookMode {
		return true, "long-polling"
	}
	info, err := tc.bot.Webhook()
	if err != nil {
		return false, fmt.Sprintf("webhook check failed: %v", err)
	}
	if info.ErrorUnixtime > 0 {
		return false, fmt.Sprintf("webhook error: %s (at %d)", info.ErrorMessage, info.ErrorUnixtime)
	}
	return true, fmt.Sprintf("webhook active: %s", info.Listen)
}

// IsWebhookMode returns whether the channel is using webhook mode.
func (tc *TelegramChannel) IsWebhookMode() bool {
	return tc.webhookMode
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
	session, err := tc.sessionManager.GetOrCreate(tc.ctx, "telegram", convID)
	if err != nil {
		return c.Send("Error loading session: " + err.Error())
	}

	tc.notifyHandler(convID, c.Text(), c.Sender())

	prompt := "[Channel: Telegram] " + c.Text()
	resp, err := tc.engine.Send(tc.ctx, session, prompt)
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
	fileCtx := fmt.Sprintf("[File uploaded: inbox/%s] You can read it with file_read using path \"inbox/%s\".", doc.FileName, doc.FileName)
	if text == "" {
		text = fileCtx
	} else {
		text = fileCtx + " " + text
	}

	convID := telegramConversationID(c.Chat().ID)
	session, err := tc.sessionManager.GetOrCreate(tc.ctx, "telegram", convID)
	if err != nil {
		return c.Send("Error loading session: " + err.Error())
	}
	tc.notifyHandler(convID, text, c.Sender())

	prompt := "[Channel: Telegram] " + text
	resp, err := tc.engine.Send(tc.ctx, session, prompt)
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
	fileCtx := fmt.Sprintf("[File uploaded: inbox/%s] You can read it with file_read using path \"inbox/%s\".", filename, filename)
	if text == "" {
		text = fileCtx
	} else {
		text = fileCtx + " " + text
	}

	convID := telegramConversationID(c.Chat().ID)
	session, err := tc.sessionManager.GetOrCreate(tc.ctx, "telegram", convID)
	if err != nil {
		return c.Send("Error loading session: " + err.Error())
	}
	tc.notifyHandler(convID, text, c.Sender())

	prompt := "[Channel: Telegram] " + text
	resp, err := tc.engine.Send(tc.ctx, session, prompt)
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

// isUserAllowed returns true if the user is permitted. Empty list denies all (fail closed).
func (tc *TelegramChannel) isUserAllowed(username, userID string) bool {
	if len(tc.allowedUsers) == 0 {
		return false // fail closed: no allowlist = no access
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

	h(tc.ctx, types.InboundMessage{
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

	base := filepath.Base(filename)
	if base == "." || base == "/" {
		base = "upload"
	}
	name := fmt.Sprintf("%d_%s", time.Now().UnixNano(), base)

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
