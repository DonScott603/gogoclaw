// Package channel defines the communication channel interface
// and implementations for Telegram, REST API, and file transfer.
package channel

import (
	"context"

	"github.com/DonScott603/gogoclaw/pkg/types"
)

// Channel represents a communication channel (Telegram, REST API, etc.).
type Channel interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Send(ctx context.Context, conversationID string, msg types.OutboundMessage) error
	OnMessage(handler func(ctx context.Context, msg types.InboundMessage))
	Name() string
}
