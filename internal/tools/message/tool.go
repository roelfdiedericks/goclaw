package message

import (
	"context"
	"encoding/json"
	"fmt"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// MessageChannel defines the interface for sending messages to channels.
type MessageChannel interface {
	SendText(chatID string, text string) (messageID string, err error)
	SendMedia(chatID string, filePath string, caption string) (messageID string, err error)
	EditMessage(chatID string, messageID string, text string) error
	DeleteMessage(chatID string, messageID string) error
	React(chatID string, messageID string, emoji string) error
}

// Tool allows the agent to send, edit, delete, and react to messages.
type Tool struct {
	channels  map[string]MessageChannel // channel name -> implementation
	mediaRoot string                    // base directory for media files
}

// NewTool creates a new message tool with the given channels.
func NewTool(channels map[string]MessageChannel) *Tool {
	return &Tool{
		channels: channels,
	}
}

// SetMediaRoot sets the media root directory for resolving content paths.
func (t *Tool) SetMediaRoot(root string) {
	t.mediaRoot = root
}

// ContentItem represents a single item in the content array.
type ContentItem struct {
	Type string `json:"type"` // "text" or "media"
	Text string `json:"text"` // for type="text"
	Path string `json:"path"` // for type="media", relative to media root
}

func (t *Tool) Name() string {
	return "message"
}

func (t *Tool) Description() string {
	return "Send, edit, delete, and react to messages. Use filePath for single media, or content array for mixed text/media. For 'send' action: omit channel to broadcast to all channels."
}

func (t *Tool) Schema() map[string]interface{} {
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
				"description": "Target channel (telegram, http). Defaults to current session's channel. If omitted for 'send' action, broadcasts to all channels.",
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
			"content": map[string]interface{}{
				"type":        "array",
				"description": "Mixed content array for interleaved text/media (alternative to message/filePath)",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"type": map[string]interface{}{
							"type":        "string",
							"enum":        []string{"text", "media"},
							"description": "Content type",
						},
						"text": map[string]interface{}{
							"type":        "string",
							"description": "Text content (for type=text)",
						},
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Media path relative to media root (for type=media)",
						},
					},
				},
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

func (t *Tool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Action    string        `json:"action"`
		Channel   string        `json:"channel"`
		To        string        `json:"to"`
		Message   string        `json:"message"`
		FilePath  string        `json:"filePath"`
		Caption   string        `json:"caption"`
		Content   []ContentItem `json:"content"`
		MessageID string        `json:"messageId"`
		Emoji     string        `json:"emoji"`
	}

	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if params.Action == "" {
		return "", fmt.Errorf("action is required")
	}

	// Get session context from context.Context for defaults
	sessionCtx := types.GetSessionContext(ctx)
	if sessionCtx == nil {
		sessionCtx = &types.SessionContext{}
	}

	// Apply defaults from session context
	channel := params.Channel
	if channel == "" {
		channel = sessionCtx.Channel
	}

	// For send action: broadcast if no channel specified OR channel doesn't have a MessageChannel
	if params.Action == "send" {
		// Check if channel exists in our message channels
		_, hasChannel := t.channels[channel]
		if channel == "" || !hasChannel {
			L_debug("message: broadcasting (channel not available)", "requestedChannel", channel)
			// Content array takes priority
			if len(params.Content) > 0 {
				return t.broadcastContent(sessionCtx, params.Content)
			}
			return t.broadcastSend(sessionCtx, params.Message, params.FilePath, params.Caption)
		}
	}

	// For non-send actions: require valid channel
	if channel == "" {
		return "", fmt.Errorf("channel is required (not in active session)")
	}

	chatID := params.To
	if chatID == "" {
		chatID = sessionCtx.ChatID
	}
	// Fallback to owner's chat ID for telegram (cron/heartbeat jobs)
	if chatID == "" && channel == "telegram" && sessionCtx.OwnerChatID != "" {
		chatID = sessionCtx.OwnerChatID
		L_debug("message: using owner chat ID fallback", "chatID", chatID)
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
		"hasContent", len(params.Content) > 0,
	)

	switch params.Action {
	case "send":
		// Content array takes priority over message/filePath
		if len(params.Content) > 0 {
			return t.sendContent(ch, chatID, params.Content)
		}
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

// broadcastSend sends to all available channels (used when no channel specified).
func (t *Tool) broadcastSend(sessionCtx *types.SessionContext, message, filePath, caption string) (string, error) {
	if len(t.channels) == 0 {
		return "", fmt.Errorf("no channels available")
	}

	var results []string
	var lastErr error

	for name, ch := range t.channels {
		// Determine chatID for this channel
		chatID := ""
		if name == "telegram" && sessionCtx.OwnerChatID != "" {
			chatID = sessionCtx.OwnerChatID
		}
		// HTTP channel doesn't need chatID (sends to all connected owner sessions)

		L_debug("message: broadcast sending", "channel", name, "chatID", chatID, "hasFilePath", filePath != "")

		result, err := t.send(ch, chatID, message, filePath, caption)
		if err != nil {
			L_warn("message: broadcast failed", "channel", name, "error", err)
			lastErr = err
			continue
		}
		results = append(results, fmt.Sprintf("%s: %s", name, result))
	}

	if len(results) == 0 {
		return "", fmt.Errorf("broadcast failed on all channels: %w", lastErr)
	}

	return fmt.Sprintf("Broadcast sent to %d channels: %v", len(results), results), nil
}

// broadcastContent broadcasts content array to all available channels.
func (t *Tool) broadcastContent(sessionCtx *types.SessionContext, content []ContentItem) (string, error) {
	if len(t.channels) == 0 {
		return "", fmt.Errorf("no channels available")
	}

	var results []string
	var lastErr error

	for name, ch := range t.channels {
		// Determine chatID for this channel
		chatID := ""
		if name == "telegram" && sessionCtx.OwnerChatID != "" {
			chatID = sessionCtx.OwnerChatID
		}
		// HTTP channel doesn't need chatID

		L_debug("message: broadcast content", "channel", name, "chatID", chatID, "items", len(content))

		result, err := t.sendContent(ch, chatID, content)
		if err != nil {
			L_warn("message: broadcast content failed", "channel", name, "error", err)
			lastErr = err
			continue
		}
		results = append(results, fmt.Sprintf("%s: %s", name, result))
	}

	if len(results) == 0 {
		return "", fmt.Errorf("broadcast content failed on all channels: %w", lastErr)
	}

	return fmt.Sprintf("Broadcast content to %d channels: %v", len(results), results), nil
}

// send sends a text or media message.
func (t *Tool) send(ch MessageChannel, chatID, message, filePath, caption string) (string, error) {
	if filePath != "" {
		// Use message as caption if caption is empty
		effectiveCaption := caption
		if effectiveCaption == "" && message != "" {
			effectiveCaption = message
		}
		// Send media
		msgID, err := ch.SendMedia(chatID, filePath, effectiveCaption)
		if err != nil {
			return "", fmt.Errorf("failed to send media: %w", err)
		}
		result := fmt.Sprintf("Media sent to %s", chatID)
		if msgID != "" {
			result = fmt.Sprintf("Media sent (messageId: %s)", msgID)
		}
		L_debug("message: media sent", "chatID", chatID, "filePath", filePath, "hasCaption", effectiveCaption != "")
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

// sendContent sends mixed content (text and media items in sequence).
func (t *Tool) sendContent(ch MessageChannel, chatID string, content []ContentItem) (string, error) {
	if len(content) == 0 {
		return "", fmt.Errorf("content array is empty")
	}

	var sentCount int
	for i, item := range content {
		switch item.Type {
		case "text":
			if item.Text == "" {
				continue
			}
			_, err := ch.SendText(chatID, item.Text)
			if err != nil {
				L_warn("message: failed to send text item", "index", i, "error", err)
				continue
			}
			sentCount++

		case "media":
			if item.Path == "" {
				continue
			}
			// Resolve path relative to media root
			absPath, err := media.ResolveMediaPath(t.mediaRoot, item.Path)
			if err != nil {
				L_warn("message: failed to resolve media path", "path", item.Path, "error", err)
				continue
			}
			_, err = ch.SendMedia(chatID, absPath, "")
			if err != nil {
				L_warn("message: failed to send media item", "index", i, "path", absPath, "error", err)
				continue
			}
			sentCount++

		default:
			L_warn("message: unknown content type", "index", i, "type", item.Type)
		}
	}

	if sentCount == 0 {
		return "", fmt.Errorf("failed to send any content items")
	}

	L_debug("message: content sent", "chatID", chatID, "items", sentCount)
	return fmt.Sprintf("Sent %d content items", sentCount), nil
}

// edit edits an existing message.
func (t *Tool) edit(ch MessageChannel, chatID, messageID, message string) (string, error) {
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

// delete deletes a message.
func (t *Tool) delete(ch MessageChannel, chatID, messageID string) (string, error) {
	if messageID == "" {
		return "", fmt.Errorf("messageId is required for delete action")
	}

	if err := ch.DeleteMessage(chatID, messageID); err != nil {
		return "", fmt.Errorf("failed to delete message: %w", err)
	}

	L_debug("message: deleted", "chatID", chatID, "messageID", messageID)
	return fmt.Sprintf("Message %s deleted", messageID), nil
}

// react adds a reaction to a message.
func (t *Tool) react(ch MessageChannel, chatID, messageID, emoji string) (string, error) {
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
