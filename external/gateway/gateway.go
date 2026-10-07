//go:build gateway || gateway.telegram || gateway.pachca

// Package gateway provides a pluggable messenger gateway for Coddy Agent.
// Build with -tags gateway (all adapters), or -tags gateway.telegram /
// -tags gateway.pachca for one adapter.
package gateway

import "context"

// Adapter is the interface every messenger gateway must implement.
type Adapter interface {
	// Name returns a short identifier for logging (e.g. "telegram").
	Name() string
	// Start begins processing incoming messages; blocks until ctx is cancelled.
	Start(ctx context.Context) error
}

// IncomingMessage is a normalised inbound message from any messenger.
type IncomingMessage struct {
	// GatewayName identifies the adapter (e.g. "telegram").
	GatewayName string

	// ChatID is the chat/channel identifier (negative for Telegram groups).
	ChatID int64
	// UserID is the sender's user identifier within the messenger.
	UserID int64
	// Username is the sender's handle (without @).
	Username string
	// FirstName is the sender's display name.
	FirstName string

	// Text is the plain message body (command prefix stripped for /commands).
	Text string

	// IsCommand is true when the message starts with a bot command (e.g. /clear).
	IsCommand bool
	// Command is the command word without the leading slash (e.g. "clear").
	Command string

	// IsMention is true when the bot was @-mentioned in a group message.
	IsMention bool
	// IsReply is true when the message is a direct reply to a bot message.
	IsReply bool

	// IsGroup is true when the message originates from a group/supergroup/channel.
	IsGroup bool

	// Raw holds the original platform message for adapter-specific processing.
	Raw interface{}
}

// OutgoingMessage is a normalised response to send back to a chat.
type OutgoingMessage struct {
	ChatID int64
	// ReplyToMessageID, when non-zero, makes the response a thread reply.
	ReplyToMessageID int
	Text             string
	// ParseMode is "HTML" or "MarkdownV2" (Telegram-specific; ignored by other adapters).
	ParseMode string
}
