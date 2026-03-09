package types

import "io"

// Attachment represents a file attached to a message.
type Attachment struct {
	Filename string    `json:"filename"`
	MimeType string    `json:"mime_type"`
	Size     int64     `json:"size"`
	Reader   io.Reader `json:"-"`
}
