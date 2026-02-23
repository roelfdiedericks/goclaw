package tools

import (
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// MessageChannel defines the interface for sending messages to channels.
// This mirrors the interface in internal/telegram/message_channel.go.
type MessageChannel interface {
	SendText(chatID string, text string) (messageID string, err error)
	SendMedia(chatID string, filePath string, caption string) (messageID string, err error)
	EditMessage(chatID string, messageID string, text string) error
	DeleteMessage(chatID string, messageID string) error
	React(chatID string, messageID string, emoji string) error
}

// Re-export types from types package for backward compatibility
type SessionElevator = types.SessionElevator
type SessionContext = types.SessionContext

var WithSessionContext = types.WithSessionContext
var GetSessionContext = types.GetSessionContext
