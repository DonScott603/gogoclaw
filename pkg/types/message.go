package types

import "time"

// InboundMessage represents a message received from a channel.
type InboundMessage struct {
	ConversationID string       `json:"conversation_id"`
	SenderID       string       `json:"sender_id"`
	Text           string       `json:"text"`
	Attachments    []Attachment `json:"attachments,omitempty"`
	Channel        string       `json:"channel"`
	Timestamp      time.Time    `json:"timestamp"`
}

// OutboundMessage represents a message sent to a channel.
type OutboundMessage struct {
	Text        string            `json:"text"`
	Attachments []Attachment      `json:"attachments,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}
