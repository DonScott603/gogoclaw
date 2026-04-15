package app

import (
	"context"
	"fmt"
	"log"

	"github.com/DonScott603/gogoclaw/internal/channel"
)

// TelegramDeps holds the Telegram channel and its lifecycle.
type TelegramDeps struct {
	Channel *channel.TelegramChannel
}

// InitTelegram creates and starts the Telegram bot channel.
func InitTelegram(cfg channel.TelegramConfig) (*TelegramDeps, error) {
	tc, err := channel.NewTelegram(cfg)
	if err != nil {
		return nil, err
	}

	go func() {
		mode := "polling"
		if tc.IsWebhookMode() {
			mode = fmt.Sprintf("webhook (%s)", cfg.Channel.WebhookURL)
		}
		log.Printf("telegram: bot starting (%s)", mode)
		if err := tc.Start(context.Background()); err != nil {
			log.Printf("telegram: %v", err)
		}
	}()

	return &TelegramDeps{Channel: tc}, nil
}

// Close stops the Telegram bot.
func (d *TelegramDeps) Close() {
	if d != nil && d.Channel != nil {
		d.Channel.Stop(context.Background())
	}
}
