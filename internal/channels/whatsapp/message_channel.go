package whatsapp

import (
	"fmt"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// MessageChannelAdapter adapts the WhatsApp Bot to the MessageChannel interface
type MessageChannelAdapter struct {
	bot       *Bot
	mediaBase string
}

// NewMessageChannelAdapter creates a new adapter for the WhatsApp bot
func NewMessageChannelAdapter(bot *Bot, mediaBase string) *MessageChannelAdapter {
	return &MessageChannelAdapter{
		bot:       bot,
		mediaBase: mediaBase,
	}
}

// SendText sends a text message to a WhatsApp chat
func (a *MessageChannelAdapter) SendText(chatID string, text string) (string, error) {
	jid := phoneToJID(chatID)
	formatted := FormatMessage(text)

	resp, err := a.bot.client.SendMessage(a.bot.ctx, jid, &waE2E.Message{
		Conversation: proto.String(formatted),
	})
	if err != nil {
		return "", err
	}

	return resp.ID, nil
}

// SendMedia sends a media file to a WhatsApp chat
func (a *MessageChannelAdapter) SendMedia(chatID string, filePath string, caption string) (string, error) {
	jid := phoneToJID(chatID)

	absPath := a.resolveMediaPath(filePath)
	L_debug("whatsapp: sending media", "chatID", chatID, "path", absPath)

	if err := a.bot.sendMediaFile(jid, absPath, caption); err != nil {
		return "", err
	}
	return "", nil
}

// EditMessage is not supported by WhatsApp
func (a *MessageChannelAdapter) EditMessage(chatID string, messageID string, text string) error {
	return fmt.Errorf("whatsapp does not support message editing")
}

// DeleteMessage revokes a message (own messages only)
func (a *MessageChannelAdapter) DeleteMessage(chatID string, messageID string) error {
	jid := phoneToJID(chatID)
	revokeMsg := a.bot.client.BuildRevoke(jid, types.EmptyJID, types.MessageID(messageID))
	_, err := a.bot.client.SendMessage(a.bot.ctx, jid, revokeMsg)
	if err != nil {
		return fmt.Errorf("failed to revoke message: %w", err)
	}
	L_debug("whatsapp: revoked message", "chatID", chatID, "msgID", messageID)
	return nil
}

// React adds a reaction emoji to a WhatsApp message
func (a *MessageChannelAdapter) React(chatID string, messageID string, emoji string) error {
	jid := phoneToJID(chatID)
	reactionMsg := a.bot.client.BuildReaction(jid, types.EmptyJID, types.MessageID(messageID), emoji)
	_, err := a.bot.client.SendMessage(a.bot.ctx, jid, reactionMsg)
	if err != nil {
		return fmt.Errorf("failed to send reaction: %w", err)
	}
	L_debug("whatsapp: reaction sent", "chatID", chatID, "msgID", messageID, "emoji", emoji)
	return nil
}

func (a *MessageChannelAdapter) resolveMediaPath(path string) string {
	if strings.HasPrefix(path, "./media/") {
		subpath := strings.TrimPrefix(path, "./media/")
		return filepath.Join(a.mediaBase, subpath)
	}
	return path
}
