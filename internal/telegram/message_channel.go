// Package telegram provides the Telegram bot adapter for GoClaw.
// message_channel.go implements the MessageChannel interface for the message tool.
package telegram

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// MessageChannel defines the interface for sending messages to channels.
// This is implemented by channel adapters (Telegram, TUI, etc.)
type MessageChannel interface {
	// SendText sends a text message to the channel
	SendText(chatID string, text string) (messageID string, err error)

	// SendMedia sends a media file to the channel
	SendMedia(chatID string, filePath string, caption string) (messageID string, err error)

	// EditMessage edits an existing message
	EditMessage(chatID string, messageID string, text string) error

	// DeleteMessage deletes a message
	DeleteMessage(chatID string, messageID string) error

	// React adds a reaction to a message
	React(chatID string, messageID string, emoji string) error
}

// MessageChannelAdapter adapts the Telegram Bot to the MessageChannel interface.
type MessageChannelAdapter struct {
	bot       *Bot
	mediaBase string // Base path for resolving relative media paths
}

// NewMessageChannelAdapter creates a new adapter for the Telegram bot.
func NewMessageChannelAdapter(bot *Bot, mediaBase string) *MessageChannelAdapter {
	return &MessageChannelAdapter{
		bot:       bot,
		mediaBase: mediaBase,
	}
}

// SendText sends a text message to the Telegram chat.
func (a *MessageChannelAdapter) SendText(chatID string, text string) (string, error) {
	id, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid chat ID: %w", err)
	}

	msg, err := a.bot.SendText(id, text)
	if err != nil {
		return "", err
	}

	return strconv.Itoa(msg.ID), nil
}

// SendMedia sends a media file to the Telegram chat.
// Supports photos, videos, audio, and documents based on file extension.
func (a *MessageChannelAdapter) SendMedia(chatID string, filePath string, caption string) (string, error) {
	id, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid chat ID: %w", err)
	}

	// Resolve relative paths (./media/...) to absolute paths
	absPath := a.resolveMediaPath(filePath)

	// Detect media type from extension
	mediaType := detectMediaType(absPath)
	L_debug("telegram: sending media", "chatID", chatID, "path", absPath, "type", mediaType)

	switch mediaType {
	case "photo":
		err = a.bot.SendPhoto(id, absPath, caption)
	case "video":
		err = a.bot.SendVideo(id, absPath, caption)
	case "audio":
		err = a.bot.SendAudio(id, absPath, caption)
	default:
		err = a.bot.SendDocument(id, absPath, caption)
	}

	if err != nil {
		return "", err
	}

	// Telegram doesn't return message ID for media sends easily, return empty for now
	return "", nil
}

// EditMessage edits an existing Telegram message.
func (a *MessageChannelAdapter) EditMessage(chatID string, messageID string, text string) error {
	chatIDInt, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid chat ID: %w", err)
	}

	msgIDInt, err := strconv.Atoi(messageID)
	if err != nil {
		return fmt.Errorf("invalid message ID: %w", err)
	}

	return a.bot.EditMessage(chatIDInt, msgIDInt, text)
}

// DeleteMessage deletes a Telegram message.
func (a *MessageChannelAdapter) DeleteMessage(chatID string, messageID string) error {
	chatIDInt, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid chat ID: %w", err)
	}

	msgIDInt, err := strconv.Atoi(messageID)
	if err != nil {
		return fmt.Errorf("invalid message ID: %w", err)
	}

	return a.bot.DeleteMessage(chatIDInt, msgIDInt)
}

// React adds a reaction emoji to a Telegram message.
func (a *MessageChannelAdapter) React(chatID string, messageID string, emoji string) error {
	chatIDInt, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid chat ID: %w", err)
	}

	msgIDInt, err := strconv.Atoi(messageID)
	if err != nil {
		return fmt.Errorf("invalid message ID: %w", err)
	}

	return a.bot.React(chatIDInt, msgIDInt, emoji)
}

// resolveMediaPath converts relative ./media/... paths to absolute paths.
func (a *MessageChannelAdapter) resolveMediaPath(path string) string {
	if strings.HasPrefix(path, "./media/") {
		// Convert ./media/browser/xxx.png to {mediaBase}/browser/xxx.png
		subpath := strings.TrimPrefix(path, "./media/")
		return filepath.Join(a.mediaBase, subpath)
	}
	return path
}

// detectMediaType determines the media type from file extension.
func detectMediaType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return "photo"
	case ".mp4", ".mov", ".webm", ".avi":
		return "video"
	case ".mp3", ".ogg", ".wav", ".m4a", ".flac":
		return "audio"
	default:
		return "document"
	}
}
