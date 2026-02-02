package tools

import (
	"context"
	"encoding/json"
	"fmt"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
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

// SessionContext provides current session information for the message tool.
type SessionContext struct {
	Channel string // Current channel name (e.g., "telegram", "tui")
	ChatID  string // Current chat ID
}

// sessionContextKey is used to store SessionContext in context.Context
type sessionContextKey struct{}

// WithSessionContext adds session context to a context.Context
func WithSessionContext(ctx context.Context, sc *SessionContext) context.Context {
	return context.WithValue(ctx, sessionContextKey{}, sc)
}

// GetSessionContext extracts session context from context.Context
func GetSessionContext(ctx context.Context) *SessionContext {
	if sc, ok := ctx.Value(sessionContextKey{}).(*SessionContext); ok {
		return sc
	}
	return nil
}

// MessageTool allows the agent to send, edit, delete, and react to messages.
type MessageTool struct {
	channels map[string]MessageChannel // channel name -> implementation
}

// NewMessageTool creates a new message tool with the given channels.
func NewMessageTool(channels map[string]MessageChannel) *MessageTool {
	return &MessageTool{
		channels: channels,
	}
}

func (t *MessageTool) Name() string {
	return "message"
}

func (t *MessageTool) Description() string {
	return "Send, edit, delete, and react to messages. Use filePath to send local files (screenshots, images, etc.) to the user's channel."
}

func (t *MessageTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"send", "edit", "delete", "react"},
				"description": "Action to perform",
			},
			"channel": map[string]interface{}{
				"type":        "string",
				"description": "Target channel (telegram, tui). Defaults to current session's channel.",
			},
			"to": map[string]interface{}{
				"type":        "string",
				"description": "Chat ID to send to. Defaults to current chat.",
			},
			"message": map[string]interface{}{
				"type":        "string",
				"description": "Text content for send/edit actions",
			},
			"filePath": map[string]interface{}{
				"type":        "string",
				"description": "Local file path to send as media (e.g., screenshot from browser tool)",
			},
			"caption": map[string]interface{}{
				"type":        "string",
				"description": "Caption for media files",
			},
			"messageId": map[string]interface{}{
				"type":        "string",
				"description": "Message ID for edit/delete/react actions",
			},
			"emoji": map[string]interface{}{
				"type":        "string",
				"description": "Emoji for react action (e.g., 👍, ❤️)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *MessageTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Action    string `json:"action"`
		Channel   string `json:"channel"`
		To        string `json:"to"`
		Message   string `json:"message"`
		FilePath  string `json:"filePath"`
		Caption   string `json:"caption"`
		MessageID string `json:"messageId"`
		Emoji     string `json:"emoji"`
	}

	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if params.Action == "" {
		return "", fmt.Errorf("action is required")
	}

	// Get session context from context.Context for defaults
	sessionCtx := GetSessionContext(ctx)
	if sessionCtx == nil {
		sessionCtx = &SessionContext{}
	}

	// Apply defaults from session context
	channel := params.Channel
	if channel == "" {
		channel = sessionCtx.Channel
	}
	if channel == "" {
		return "", fmt.Errorf("channel is required (not in active session)")
	}

	chatID := params.To
	if chatID == "" {
		chatID = sessionCtx.ChatID
	}
	if chatID == "" && params.Action != "react" {
		return "", fmt.Errorf("to (chat ID) is required (not in active session)")
	}

	// Get channel implementation
	ch, ok := t.channels[channel]
	if !ok {
		return "", fmt.Errorf("unknown channel: %s", channel)
	}

	L_debug("message: executing",
		"action", params.Action,
		"channel", channel,
		"chatID", chatID,
		"hasFilePath", params.FilePath != "",
	)

	switch params.Action {
	case "send":
		return t.send(ch, chatID, params.Message, params.FilePath, params.Caption)
	case "edit":
		return t.edit(ch, chatID, params.MessageID, params.Message)
	case "delete":
		return t.delete(ch, chatID, params.MessageID)
	case "react":
		return t.react(ch, chatID, params.MessageID, params.Emoji)
	default:
		return "", fmt.Errorf("unknown action: %s", params.Action)
	}
}

// send sends a text or media message
func (t *MessageTool) send(ch MessageChannel, chatID, message, filePath, caption string) (string, error) {
	if filePath != "" {
		// Send media
		msgID, err := ch.SendMedia(chatID, filePath, caption)
		if err != nil {
			return "", fmt.Errorf("failed to send media: %w", err)
		}
		result := fmt.Sprintf("Media sent to %s", chatID)
		if msgID != "" {
			result = fmt.Sprintf("Media sent (messageId: %s)", msgID)
		}
		L_debug("message: media sent", "chatID", chatID, "filePath", filePath)
		return result, nil
	}

	if message == "" {
		return "", fmt.Errorf("message or filePath is required for send action")
	}

	// Send text
	msgID, err := ch.SendText(chatID, message)
	if err != nil {
		return "", fmt.Errorf("failed to send message: %w", err)
	}

	result := fmt.Sprintf("Message sent to %s", chatID)
	if msgID != "" {
		result = fmt.Sprintf("Message sent (messageId: %s)", msgID)
	}
	L_debug("message: text sent", "chatID", chatID, "msgID", msgID)
	return result, nil
}

// edit edits an existing message
func (t *MessageTool) edit(ch MessageChannel, chatID, messageID, message string) (string, error) {
	if messageID == "" {
		return "", fmt.Errorf("messageId is required for edit action")
	}
	if message == "" {
		return "", fmt.Errorf("message is required for edit action")
	}

	if err := ch.EditMessage(chatID, messageID, message); err != nil {
		return "", fmt.Errorf("failed to edit message: %w", err)
	}

	L_debug("message: edited", "chatID", chatID, "messageID", messageID)
	return fmt.Sprintf("Message %s edited", messageID), nil
}

// delete deletes a message
func (t *MessageTool) delete(ch MessageChannel, chatID, messageID string) (string, error) {
	if messageID == "" {
		return "", fmt.Errorf("messageId is required for delete action")
	}

	if err := ch.DeleteMessage(chatID, messageID); err != nil {
		return "", fmt.Errorf("failed to delete message: %w", err)
	}

	L_debug("message: deleted", "chatID", chatID, "messageID", messageID)
	return fmt.Sprintf("Message %s deleted", messageID), nil
}

// react adds a reaction to a message
func (t *MessageTool) react(ch MessageChannel, chatID, messageID, emoji string) (string, error) {
	if messageID == "" {
		return "", fmt.Errorf("messageId is required for react action")
	}
	if emoji == "" {
		return "", fmt.Errorf("emoji is required for react action")
	}

	if err := ch.React(chatID, messageID, emoji); err != nil {
		return "", fmt.Errorf("failed to add reaction: %w", err)
	}

	L_debug("message: reacted", "chatID", chatID, "messageID", messageID, "emoji", emoji)
	return fmt.Sprintf("Reaction %s added to message %s", emoji, messageID), nil
}
