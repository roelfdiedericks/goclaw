// Package telegram provides the Telegram bot adapter for GoClaw.
package telegram

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	tele "gopkg.in/telebot.v4"

	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/gateway"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/session"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// Bot represents the Telegram bot
type Bot struct {
	bot     *tele.Bot
	gateway *gateway.Gateway
	users   *user.Registry
	config  *config.TelegramConfig

	// Track active messages for editing during streaming
	activeMessages sync.Map // chatID -> *tele.Message

	ctx    context.Context
	cancel context.CancelFunc
}

// New creates a new Telegram bot
func New(cfg *config.TelegramConfig, gw *gateway.Gateway, users *user.Registry) (*Bot, error) {
	if cfg.BotToken == "" {
		return nil, fmt.Errorf("telegram bot token not configured")
	}

	pref := tele.Settings{
		Token:  cfg.BotToken,
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
	}

	L_debug("telegram: creating bot", "tokenLength", len(cfg.BotToken))

	bot, err := tele.NewBot(pref)
	if err != nil {
		return nil, fmt.Errorf("failed to create telegram bot: %w", err)
	}

	// Log bot info
	L_debug("telegram: bot created",
		"username", bot.Me.Username,
		"id", bot.Me.ID,
		"firstName", bot.Me.FirstName,
	)

	ctx, cancel := context.WithCancel(context.Background())

	b := &Bot{
		bot:     bot,
		gateway: gw,
		users:   users,
		config:  cfg,
		ctx:     ctx,
		cancel:  cancel,
	}

	// Register handlers
	b.setupHandlers()
	L_debug("telegram: handlers registered")

	return b, nil
}

// setupHandlers registers message handlers
func (b *Bot) setupHandlers() {
	// Handle all text messages
	b.bot.Handle(tele.OnText, b.handleMessage)

	// Handle photo messages
	b.bot.Handle(tele.OnPhoto, b.handlePhoto)

	// Handle /start command
	b.bot.Handle("/start", func(c tele.Context) error {
		return c.Send("Hello! I'm GoClaw, your AI assistant. Send me a message to get started.")
	})

	// Handle /help command
	b.bot.Handle("/help", func(c tele.Context) error {
		help := `*GoClaw Commands*

/start - Start the bot
/help - Show this help
/status - Show session info and last compaction
/clear - Clear conversation history
/compact - Force context compaction

Just send a message to chat with me!`
		return c.Send(help, &tele.SendOptions{ParseMode: tele.ModeMarkdown})
	})

	// Handle /status command
	b.bot.Handle("/status", func(c tele.Context) error {
		userID := fmt.Sprintf("%d", c.Sender().ID)
		u := b.users.FromIdentity("telegram", userID)
		if u == nil {
			return c.Send("You're not authorized to use this bot.")
		}

		// Owner uses primary session
		var sessionKey string
		if u.Role == "owner" {
			sessionKey = session.PrimarySession
		} else {
			sessionKey = fmt.Sprintf("user:%s", u.ID)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		info, err := b.gateway.GetSessionInfo(ctx, sessionKey)
		if err != nil {
			return c.Send(fmt.Sprintf("Error: %s", err))
		}

		// Build status message
		status := fmt.Sprintf(`*Session Status*

Messages: %d
Tokens: %d / %d (%.1f%%)
Compactions: %d`,
			info.Messages,
			info.TotalTokens,
			info.MaxTokens,
			info.UsagePercent,
			info.CompactionCount)

		if info.LastCompaction != nil {
			status += fmt.Sprintf(`

*Last Compaction* (%s)
Tokens before: %d
Summary:
%s`,
				info.LastCompaction.Timestamp.Format("2006-01-02 15:04"),
				info.LastCompaction.TokensBefore,
				info.LastCompaction.Summary)
		}

		// Telegram has a 4096 char limit, truncate if needed
		if len(status) > 4000 {
			status = status[:4000] + "..."
		}

		return c.Send(status, &tele.SendOptions{ParseMode: tele.ModeMarkdown})
	})

	// Handle /clear command
	b.bot.Handle("/clear", func(c tele.Context) error {
		userID := fmt.Sprintf("%d", c.Sender().ID)
		u := b.users.FromIdentity("telegram", userID)
		if u == nil {
			return c.Send("You're not authorized to use this bot.")
		}
		
		// Owner uses primary session (inherited from OpenClaw), others use user-specific
		var sessionKey string
		if u.Role == "owner" {
			sessionKey = session.PrimarySession
		} else {
			sessionKey = fmt.Sprintf("user:%s", u.ID)
		}
		
		b.gateway.ResetSession(sessionKey)
		return c.Send("Conversation cleared.")
	})

	// Handle /compact command
	b.bot.Handle("/compact", func(c tele.Context) error {
		userID := fmt.Sprintf("%d", c.Sender().ID)
		u := b.users.FromIdentity("telegram", userID)
		if u == nil {
			return c.Send("You're not authorized to use this bot.")
		}

		// Owner uses primary session (inherited from OpenClaw), others use user-specific
		var sessionKey string
		if u.Role == "owner" {
			sessionKey = session.PrimarySession
		} else {
			sessionKey = fmt.Sprintf("user:%s", u.ID)
		}

		// Send "working" message
		msg, _ := c.Bot().Send(c.Chat(), "Compacting session... (this may take a minute)")

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		result, err := b.gateway.ForceCompact(ctx, sessionKey)
		if err != nil {
			if msg != nil {
				c.Bot().Edit(msg, fmt.Sprintf("Compaction failed: %s", err))
			}
			return nil
		}

		summary := fmt.Sprintf("Compaction complete!\n\nTokens before: %d\nSummary: %s",
			result.TokensBefore,
			truncate(result.Summary, 200))

		if msg != nil {
			c.Bot().Edit(msg, summary)
		} else {
			c.Send(summary)
		}
		return nil
	})
}

// handleMessage handles incoming text messages
func (b *Bot) handleMessage(c tele.Context) error {
	// Get sender info
	sender := c.Sender()
	userID := fmt.Sprintf("%d", sender.ID)
	chatID := c.Chat().ID
	isGroup := c.Chat().Type != tele.ChatPrivate

	L_debug("telegram message received",
		"userID", userID,
		"chatID", chatID,
		"isGroup", isGroup,
		"text", truncate(c.Text(), 50),
	)

	// Skip group messages for MVP
	if isGroup {
		L_debug("ignoring group message")
		return nil
	}

	// Look up user by Telegram identity
	L_debug("telegram: looking up user", "provider", "telegram", "userID", userID)
	u := b.users.FromIdentity("telegram", userID)
	if u == nil {
		L_warn("telegram: unknown user ignored", "userID", userID, "senderName", sender.FirstName+" "+sender.LastName)
		// Silently ignore unauthorized users
		return nil
	}

	L_info("telegram: authenticated message", "user", u.Name, "role", u.Role, "userID", userID)

	// Show typing indicator
	_ = c.Notify(tele.Typing)

	// Create agent request with media callback
	req := gateway.AgentRequest{
		User:    u,
		Source:  "telegram",
		ChatID:  fmt.Sprintf("%d", chatID),
		IsGroup: isGroup,
		UserMsg: c.Text(),
		OnMediaToSend: func(path, caption string) error {
			return b.SendPhoto(chatID, path, caption)
		},
	}

	// Run agent with streaming
	events := make(chan gateway.AgentEvent, 100)

	go func() {
		if err := b.gateway.RunAgent(b.ctx, req, events); err != nil {
			L_error("telegram agent error", "error", err)
		}
	}()

	// Process events and stream response
	return b.streamResponse(c, events)
}

// handlePhoto handles incoming photo messages
func (b *Bot) handlePhoto(c tele.Context) error {
	sender := c.Sender()
	userID := fmt.Sprintf("%d", sender.ID)
	chatID := c.Chat().ID
	isGroup := c.Chat().Type != tele.ChatPrivate

	L_debug("telegram photo received",
		"userID", userID,
		"chatID", chatID,
		"isGroup", isGroup,
	)

	// Skip group messages for MVP
	if isGroup {
		L_debug("ignoring group photo")
		return nil
	}

	// Look up user
	u := b.users.FromIdentity("telegram", userID)
	if u == nil {
		L_warn("telegram: unknown user ignored (photo)", "userID", userID)
		return nil
	}

	L_info("telegram: authenticated photo", "user", u.Name, "role", u.Role)

	// Show typing indicator
	_ = c.Notify(tele.Typing)

	// Get the photo (telebot gives us the largest size)
	photo := c.Message().Photo
	if photo == nil {
		L_warn("telegram: photo message but no photo found")
		return nil
	}

	// Download and optimize the image
	L_debug("telegram: downloading photo", "fileID", photo.FileID, "width", photo.Width, "height", photo.Height)
	imageData, err := media.DownloadAndOptimize(b.bot, photo)
	if err != nil {
		L_error("telegram: failed to download/optimize photo", "error", err)
		return c.Send("Sorry, I couldn't process that image.")
	}

	L_debug("telegram: photo optimized",
		"originalSize", photo.FileSize,
		"optimizedSize", len(imageData.Data),
		"dimensions", fmt.Sprintf("%dx%d", imageData.Width, imageData.Height),
	)

	// Create image attachment
	imageAttachment := session.ImageAttachment{
		Data:     imageData.Base64(),
		MimeType: imageData.MimeType,
		Source:   "telegram",
	}

	// Get caption (if any) as the text message
	caption := c.Message().Caption
	if caption == "" {
		caption = "<media:image>" // Placeholder if no caption
	}

	// Create agent request with image and media callback
	req := gateway.AgentRequest{
		User:    u,
		Source:  "telegram",
		ChatID:  fmt.Sprintf("%d", chatID),
		IsGroup: isGroup,
		UserMsg: caption,
		Images:  []session.ImageAttachment{imageAttachment},
		OnMediaToSend: func(path, caption string) error {
			return b.SendPhoto(chatID, path, caption)
		},
	}

	// Run agent with streaming
	events := make(chan gateway.AgentEvent, 100)

	go func() {
		if err := b.gateway.RunAgent(b.ctx, req, events); err != nil {
			L_error("telegram agent error", "error", err)
		}
	}()

	return b.streamResponse(c, events)
}

// streamResponse handles streaming the response to Telegram
func (b *Bot) streamResponse(c tele.Context, events <-chan gateway.AgentEvent) error {
	var response strings.Builder
	var currentMsg *tele.Message
	var lastUpdate time.Time
	var startTime = time.Now()
	var editCount int
	updateInterval := 500 * time.Millisecond // Don't update too frequently

	L_debug("telegram: starting response stream", "chatID", c.Chat().ID)

	for event := range events {
		switch e := event.(type) {
		case gateway.EventTextDelta:
			response.WriteString(e.Delta)

			// Update message periodically to show streaming
			// During streaming, send plain text (HTML formatting only on final)
			if time.Since(lastUpdate) > updateInterval {
				if currentMsg == nil {
					// Send initial message (plain text during streaming)
					msg, err := b.bot.Send(c.Chat(), response.String())
					if err != nil {
						L_error("telegram: failed to send initial message", "error", err)
						continue
					}
					currentMsg = msg
					L_debug("telegram: sent initial message", "msgID", msg.ID, "length", response.Len())
				} else {
					// Edit existing message (plain text during streaming)
					_, err := b.bot.Edit(currentMsg, response.String())
					if err != nil {
						// Edit can fail if content unchanged or rate limited
						L_trace("telegram: edit failed", "error", err)
					} else {
						editCount++
					}
				}
				lastUpdate = time.Now()

				// Keep showing typing indicator
				_ = c.Notify(tele.Typing)
			}

		case gateway.EventToolStart:
			L_debug("telegram: tool started", "tool", e.ToolName)
			_ = c.Notify(tele.Typing)

		case gateway.EventToolEnd:
			L_debug("telegram: tool ended", "tool", e.ToolName, "hasError", e.Error != "")

		case gateway.EventAgentEnd:
			// Send or update final message with HTML formatting
			finalText := response.String()
			if finalText == "" {
				finalText = "(No response)"
			}

			elapsed := time.Since(startTime)
			L_debug("telegram: agent completed",
				"responseLength", len(finalText),
				"editCount", editCount,
				"elapsed", elapsed.Round(time.Millisecond),
			)

			// Convert markdown to Telegram HTML
			formattedText := FormatMessage(finalText)

			if currentMsg == nil {
				// Try HTML first, fallback to plain text
				_, err := b.bot.Send(c.Chat(), formattedText, &tele.SendOptions{ParseMode: tele.ModeHTML})
				if err != nil {
					L_debug("telegram: HTML send failed, falling back to plain text", "error", err)
					_, err = b.bot.Send(c.Chat(), finalText)
					if err != nil {
						L_error("telegram: failed to send final message", "error", err)
					}
				}
			} else {
				// Try HTML edit first, fallback to plain text
				_, err := b.bot.Edit(currentMsg, formattedText, &tele.SendOptions{ParseMode: tele.ModeHTML})
				if err != nil {
					L_debug("telegram: HTML edit failed, falling back to plain text", "error", err)
					_, err = b.bot.Edit(currentMsg, finalText)
					if err != nil {
						L_debug("telegram: failed to edit final message", "error", err)
					}
				}
			}

		case gateway.EventAgentError:
			L_error("telegram: agent error", "error", e.Error)
			errMsg := fmt.Sprintf("Error: %s", e.Error)
			if currentMsg == nil {
				_, _ = b.bot.Send(c.Chat(), errMsg)
			} else {
				_, _ = b.bot.Edit(currentMsg, errMsg)
			}
		}
	}

	return nil
}

// Start starts the bot polling
func (b *Bot) Start() {
	L_info("starting telegram bot")
	go b.bot.Start()
}

// Stop stops the bot
func (b *Bot) Stop() {
	L_info("stopping telegram bot")
	b.cancel()
	b.bot.Stop()
}

// Name returns the channel name (implements gateway.Channel)
func (b *Bot) Name() string {
	return "telegram"
}

// Send sends a message to the default chat (not implemented for Telegram)
func (b *Bot) Send(ctx context.Context, msg string) error {
	// Telegram requires a specific chat ID, so this is a no-op
	return nil
}

// sendWithHTMLFallback sends a message with HTML formatting, falling back to plain text
func (b *Bot) sendWithHTMLFallback(chat *tele.Chat, text string) (*tele.Message, error) {
	formatted := FormatMessage(text)
	msg, err := b.bot.Send(chat, formatted, &tele.SendOptions{ParseMode: tele.ModeHTML})
	if err != nil {
		L_debug("telegram: HTML send failed, falling back to plain text", "error", err)
		return b.bot.Send(chat, text)
	}
	return msg, nil
}

// TelegramCaptionLimit is Telegram's maximum caption length
const TelegramCaptionLimit = 1024

// SendPhoto sends a photo to a chat with optional caption.
// If caption exceeds Telegram's limit, sends photo first then follow-up message.
func (b *Bot) SendPhoto(chatID int64, path string, caption string) error {
	chat := &tele.Chat{ID: chatID}
	photo := &tele.Photo{File: tele.FromDisk(path)}

	// Format caption as HTML
	formattedCaption := ""
	if caption != "" {
		formattedCaption = FormatMessage(caption)
	}

	if len(formattedCaption) <= TelegramCaptionLimit {
		// Caption fits - send photo with caption
		photo.Caption = formattedCaption
		_, err := b.bot.Send(chat, photo, &tele.SendOptions{ParseMode: tele.ModeHTML})
		if err != nil {
			// Fallback: try without HTML formatting
			L_debug("telegram: HTML caption failed, trying plain text", "error", err)
			photo.Caption = caption
			_, err = b.bot.Send(chat, photo)
		}
		return err
	}

	// Caption too long - send photo first, then follow-up message
	L_debug("telegram: caption exceeds limit, sending photo then text",
		"captionLen", len(formattedCaption),
		"limit", TelegramCaptionLimit,
	)

	_, err := b.bot.Send(chat, photo)
	if err != nil {
		return fmt.Errorf("failed to send photo: %w", err)
	}

	// Send follow-up text message with full caption
	_, err = b.sendWithHTMLFallback(chat, caption)
	if err != nil {
		L_warn("telegram: failed to send follow-up caption", "error", err)
	}
	return nil
}

// SendPhotoFromBytes sends a photo from bytes data to a chat
func (b *Bot) SendPhotoFromBytes(chatID int64, data []byte, caption string) error {
	chat := &tele.Chat{ID: chatID}
	photo := &tele.Photo{File: tele.FromReader(strings.NewReader(string(data)))}

	formattedCaption := ""
	if caption != "" {
		formattedCaption = FormatMessage(caption)
	}

	if len(formattedCaption) <= TelegramCaptionLimit {
		photo.Caption = formattedCaption
		_, err := b.bot.Send(chat, photo, &tele.SendOptions{ParseMode: tele.ModeHTML})
		if err != nil {
			photo.Caption = caption
			_, err = b.bot.Send(chat, photo)
		}
		return err
	}

	// Caption too long - send photo first, then follow-up
	_, err := b.bot.Send(chat, photo)
	if err != nil {
		return fmt.Errorf("failed to send photo: %w", err)
	}

	_, err = b.sendWithHTMLFallback(chat, caption)
	return err
}

// SendMirror sends a mirrored message to a user's Telegram chat
func (b *Bot) SendMirror(ctx context.Context, source, userMsg, response string) error {
	// Find all users who should receive mirrors
	// For now, we'll send to the owner's chat if they have a Telegram identity
	owner := b.users.Owner()
	if owner == nil {
		return nil
	}

	// Get owner's Telegram ID
	var telegramID string
	for _, identity := range owner.Identities {
		if identity.Provider == "telegram" {
			telegramID = identity.Value
			break
		}
	}

	if telegramID == "" {
		return nil
	}

	// Parse telegram ID to int64
	var chatID int64
	fmt.Sscanf(telegramID, "%d", &chatID)
	chat := &tele.Chat{ID: chatID}

	// Telegram max message is 4096 chars. Reserve space for formatting.
	const maxTelegramMsg = 4096
	const headerReserve = 100 // for "📱 <b>source</b>\n\n<b>You:</b> ...\n\n<b>Assistant:</b> "

	// Calculate available space
	availableForContent := maxTelegramMsg - headerReserve
	userMsgLimit := min(500, availableForContent/4)     // User msg gets up to 1/4
	responseLimit := availableForContent - len(userMsg) // Response gets the rest
	if len(userMsg) > userMsgLimit {
		responseLimit = availableForContent - userMsgLimit
	}

	truncatedUser := truncate(userMsg, userMsgLimit)
	truncatedResponse := truncate(response, responseLimit)

	// Format mirror message using HTML (escape HTML entities in content)
	escapedUser := escapeHTML(truncatedUser)
	escapedResponse := FormatMessage(truncatedResponse) // Convert markdown to HTML

	mirror := fmt.Sprintf("📱 <b>%s</b>\n\n<b>You:</b> %s\n\n<b>Assistant:</b> %s",
		source, escapedUser, escapedResponse)

	_, err := b.bot.Send(chat, mirror, &tele.SendOptions{ParseMode: tele.ModeHTML})
	if err != nil {
		// Fallback to plain text (common with emoji/special chars)
		// Show snippet of what failed for debugging
		snippet := mirror
		if len(snippet) > 100 {
			snippet = snippet[:100] + "..."
		}
		L_debug("telegram: HTML mirror failed, falling back to plain text",
			"error", err,
			"source", source,
			"mirrorLen", len(mirror),
			"snippet", snippet)
		plainMirror := fmt.Sprintf("📱 %s\n\nYou: %s\n\nAssistant: %s",
			source, truncatedUser, truncatedResponse)
		_, err = b.bot.Send(chat, plainMirror)
	}
	if err != nil {
		L_error("failed to send telegram mirror", "error", err)
	}
	return err
}

// HasUser returns true if the user has a Telegram identity
func (b *Bot) HasUser(u *user.User) bool {
	for _, identity := range u.Identities {
		if identity.Provider == "telegram" {
			return true
		}
	}
	return false
}

// SendText sends a text message to a chat.
// Returns the sent message for potential editing/deletion.
func (b *Bot) SendText(chatID int64, text string) (*tele.Message, error) {
	chat := &tele.Chat{ID: chatID}
	formatted := FormatMessage(text)

	msg, err := b.bot.Send(chat, formatted, &tele.SendOptions{ParseMode: tele.ModeHTML})
	if err != nil {
		// Fallback to plain text
		L_debug("telegram: HTML send failed, falling back to plain text", "error", err)
		msg, err = b.bot.Send(chat, text)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to send text: %w", err)
	}

	L_debug("telegram: sent text message", "chatID", chatID, "msgID", msg.ID, "length", len(text))
	return msg, nil
}

// EditMessage edits an existing message.
func (b *Bot) EditMessage(chatID int64, messageID int, text string) error {
	msg := &tele.Message{
		ID:   messageID,
		Chat: &tele.Chat{ID: chatID},
	}

	formatted := FormatMessage(text)
	_, err := b.bot.Edit(msg, formatted, &tele.SendOptions{ParseMode: tele.ModeHTML})
	if err != nil {
		// Fallback to plain text
		L_debug("telegram: HTML edit failed, falling back to plain text", "error", err)
		_, err = b.bot.Edit(msg, text)
	}

	if err != nil {
		return fmt.Errorf("failed to edit message: %w", err)
	}

	L_debug("telegram: edited message", "chatID", chatID, "msgID", messageID)
	return nil
}

// DeleteMessage deletes a message from a chat.
func (b *Bot) DeleteMessage(chatID int64, messageID int) error {
	msg := &tele.Message{
		ID:   messageID,
		Chat: &tele.Chat{ID: chatID},
	}

	if err := b.bot.Delete(msg); err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}

	L_debug("telegram: deleted message", "chatID", chatID, "msgID", messageID)
	return nil
}

// React adds a reaction emoji to a message.
// Note: Reactions require Telegram Bot API 6.0+ and specific bot permissions.
func (b *Bot) React(chatID int64, messageID int, emoji string) error {
	// Telegram reactions use the setMessageReaction API method
	// telebot v4 requires (Recipient, Editable, Reactions)
	chat := &tele.Chat{ID: chatID}
	msg := &tele.Message{
		ID:   messageID,
		Chat: chat,
	}

	reactions := tele.Reactions{
		Reactions: []tele.Reaction{
			{
				Type:  tele.ReactionTypeEmoji,
				Emoji: emoji,
			},
		},
	}

	if err := b.bot.React(chat, msg, reactions); err != nil {
		return fmt.Errorf("failed to add reaction: %w", err)
	}

	L_debug("telegram: added reaction", "chatID", chatID, "msgID", messageID, "emoji", emoji)
	return nil
}

// SendVideo sends a video file to a chat.
func (b *Bot) SendVideo(chatID int64, path string, caption string) error {
	chat := &tele.Chat{ID: chatID}
	video := &tele.Video{File: tele.FromDisk(path)}

	formattedCaption := ""
	if caption != "" {
		formattedCaption = FormatMessage(caption)
	}

	if len(formattedCaption) <= TelegramCaptionLimit {
		video.Caption = formattedCaption
		_, err := b.bot.Send(chat, video, &tele.SendOptions{ParseMode: tele.ModeHTML})
		if err != nil {
			video.Caption = caption
			_, err = b.bot.Send(chat, video)
		}
		return err
	}

	// Caption too long - send video first, then follow-up
	_, err := b.bot.Send(chat, video)
	if err != nil {
		return fmt.Errorf("failed to send video: %w", err)
	}

	_, err = b.sendWithHTMLFallback(chat, caption)
	return err
}

// SendDocument sends a document file to a chat.
func (b *Bot) SendDocument(chatID int64, path string, caption string) error {
	chat := &tele.Chat{ID: chatID}
	doc := &tele.Document{File: tele.FromDisk(path)}

	formattedCaption := ""
	if caption != "" {
		formattedCaption = FormatMessage(caption)
	}

	if len(formattedCaption) <= TelegramCaptionLimit {
		doc.Caption = formattedCaption
		_, err := b.bot.Send(chat, doc, &tele.SendOptions{ParseMode: tele.ModeHTML})
		if err != nil {
			doc.Caption = caption
			_, err = b.bot.Send(chat, doc)
		}
		return err
	}

	// Caption too long - send document first, then follow-up
	_, err := b.bot.Send(chat, doc)
	if err != nil {
		return fmt.Errorf("failed to send document: %w", err)
	}

	_, err = b.sendWithHTMLFallback(chat, caption)
	return err
}

// SendAudio sends an audio file to a chat.
func (b *Bot) SendAudio(chatID int64, path string, caption string) error {
	chat := &tele.Chat{ID: chatID}
	audio := &tele.Audio{File: tele.FromDisk(path)}

	formattedCaption := ""
	if caption != "" {
		formattedCaption = FormatMessage(caption)
	}

	if len(formattedCaption) <= TelegramCaptionLimit {
		audio.Caption = formattedCaption
		_, err := b.bot.Send(chat, audio, &tele.SendOptions{ParseMode: tele.ModeHTML})
		if err != nil {
			audio.Caption = caption
			_, err = b.bot.Send(chat, audio)
		}
		return err
	}

	// Caption too long - send audio first, then follow-up
	_, err := b.bot.Send(chat, audio)
	if err != nil {
		return fmt.Errorf("failed to send audio: %w", err)
	}

	_, err = b.sendWithHTMLFallback(chat, caption)
	return err
}

// truncate truncates a string to maxLen characters
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// escapeMarkdown escapes Telegram Markdown special characters to prevent parse errors
func escapeMarkdown(s string) string {
	// Telegram Markdown v1 special chars: * _ ` [
	replacer := strings.NewReplacer(
		"*", "\\*",
		"_", "\\_",
		"`", "\\`",
		"[", "\\[",
	)
	return replacer.Replace(s)
}

// escapeHTML escapes HTML special characters for Telegram HTML mode
func escapeHTML(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	)
	return replacer.Replace(s)
}
