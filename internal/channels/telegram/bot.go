// Package telegram provides the Telegram bot adapter for GoClaw.
package telegram

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	tele "gopkg.in/telebot.v4"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/commands"
	"github.com/roelfdiedericks/goclaw/internal/gateway"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/session"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// ChatPreferences stores per-chat preferences
type ChatPreferences struct {
	ShowThinking  bool   // Show tool calls and thinking output
	ThinkingLevel string // Thinking intensity: off/minimal/low/medium/high/xhigh
}

// Bot represents the Telegram bot
type Bot struct {
	bot     *tele.Bot
	gateway *gateway.Gateway
	users   *user.Registry
	config  *Config

	// Per-chat preferences
	chatPrefs sync.Map // chatID (int64) -> *ChatPreferences

	ctx    context.Context
	cancel context.CancelFunc
}

// getChatPrefs returns preferences for a chat, creating if needed.
// If user is provided and prefs don't exist, initializes from user preferences.
func (b *Bot) getChatPrefs(chatID int64, u *user.User) *ChatPreferences {
	if prefs, ok := b.chatPrefs.Load(chatID); ok {
		return prefs.(*ChatPreferences) //nolint:errcheck // type assertion safe - we only store *ChatPreferences
	}
	// Initialize from user preference if available
	showThinking := false
	thinkingLevel := ""
	if u != nil {
		showThinking = u.Thinking
		thinkingLevel = u.ThinkingLevel
	}
	prefs := &ChatPreferences{
		ShowThinking:  showThinking,
		ThinkingLevel: thinkingLevel,
	}
	b.chatPrefs.Store(chatID, prefs)
	return prefs
}

// New creates a new Telegram bot
func New(cfg *Config, gw *gateway.Gateway, users *user.Registry) (*Bot, error) {
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

	// Log bot info - confirm connection worked and show identity
	L_info("telegram: connected",
		"bot", "@"+bot.Me.Username,
		"name", bot.Me.FirstName,
		"id", bot.Me.ID,
		"canJoinGroups", bot.Me.CanJoinGroups,
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

	// Handle /start command (Telegram-specific, not in global registry)
	b.bot.Handle("/start", func(c tele.Context) error {
		return c.Send("Hello! I'm GoClaw, your AI assistant. Send me a message to get started.")
	})

	// Handle /thinking command (channel-specific preference)
	b.bot.Handle("/thinking", func(c tele.Context) error {
		chatID := c.Chat().ID
		userID := fmt.Sprintf("%d", c.Sender().ID)
		u := b.users.FromIdentity("telegram", userID)

		// Check command permission
		if !b.canUserUseCommands(u) {
			L_debug("telegram: commands disabled for user", "user", u.Name, "command", "/thinking")
			return nil // Silently ignore - treat as if they sent a message
		}

		prefs := b.getChatPrefs(chatID, u)

		// Parse subcommand
		arg := strings.ToLower(strings.TrimSpace(c.Message().Payload))

		var resultMsg string
		switch arg {
		case "on":
			prefs.ShowThinking = true
			if prefs.ThinkingLevel == "" || prefs.ThinkingLevel == "off" {
				prefs.ThinkingLevel = llm.DefaultThinkingLevel.String()
			}
			resultMsg = fmt.Sprintf("Thinking output enabled (level: %s).", prefs.ThinkingLevel)
		case "off":
			prefs.ShowThinking = false
			prefs.ThinkingLevel = "off"
			resultMsg = "Thinking output disabled. You'll only see final responses."
		case "toggle", "":
			prefs.ShowThinking = !prefs.ShowThinking
			if prefs.ShowThinking {
				if prefs.ThinkingLevel == "" || prefs.ThinkingLevel == "off" {
					prefs.ThinkingLevel = llm.DefaultThinkingLevel.String()
				}
				resultMsg = fmt.Sprintf("Thinking output enabled (level: %s).", prefs.ThinkingLevel)
			} else {
				resultMsg = "Thinking output disabled."
			}
		case "status":
			if prefs.ShowThinking {
				level := prefs.ThinkingLevel
				if level == "" {
					level = llm.DefaultThinkingLevel.String()
				}
				resultMsg = fmt.Sprintf("Thinking output: ON, level: %s", level)
			} else {
				resultMsg = "Thinking output: OFF"
			}
		default:
			// Check if arg is a valid thinking level
			if llm.IsValidThinkingLevel(arg) {
				prefs.ThinkingLevel = arg
				if arg == "off" {
					prefs.ShowThinking = false
					resultMsg = "Thinking disabled."
				} else {
					prefs.ShowThinking = true // Setting a level automatically enables thinking display
					resultMsg = fmt.Sprintf("Thinking level set to %s (output enabled).", arg)
				}
			} else {
				resultMsg = "Usage: /thinking [on|off|toggle|status|minimal|low|medium|high|xhigh]"
			}
		}

		return c.Send(resultMsg)
	})
}

// getSessionKey returns the session key for the current user
func (b *Bot) getSessionKey(c tele.Context) (string, error) {
	userID := fmt.Sprintf("%d", c.Sender().ID)
	u := b.users.FromIdentity("telegram", userID)
	if u == nil {
		return "", fmt.Errorf("You're not authorized to use this bot.")
	}

	// Owner uses primary session (inherited from OpenClaw), others use user-specific
	if u.Role == "owner" {
		return session.PrimarySession, nil
	}
	return fmt.Sprintf("user:%s", u.ID), nil
}

// canUserUseCommands checks if the user has permission to use slash commands
func (b *Bot) canUserUseCommands(u *user.User) bool {
	if u == nil {
		return false
	}
	resolvedRole, err := b.users.ResolveUserRole(u)
	if err != nil {
		L_warn("telegram: failed to resolve role for command permission check", "user", u.Name, "error", err)
		return false
	}
	return resolvedRole.CanUseCommands()
}

// handleCommand routes commands to the global command manager
func (b *Bot) handleCommand(c tele.Context, u *user.User) error {
	text := c.Text()

	// Check command permission
	if !b.canUserUseCommands(u) {
		L_debug("telegram: commands disabled for user", "user", u.Name, "command", text)
		return nil // Silently ignore
	}

	sessionKey, err := b.getSessionKey(c)
	if err != nil {
		return c.Send(err.Error())
	}

	mgr := commands.GetManager()

	// Special handling for long-running commands
	cmdName := strings.Fields(text)[0]
	if cmdName == "/compact" {
		msg, _ := c.Bot().Send(c.Chat(), "Compacting session... (this may take a minute)")

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		result := mgr.Execute(ctx, text, sessionKey)

		if msg != nil {
			c.Bot().Edit(msg, FormatMessage(result.Markdown), &tele.SendOptions{ParseMode: tele.ModeHTML}) //nolint:errcheck // fire-and-forget telegram edit
		} else {
			c.Send(FormatMessage(result.Markdown), &tele.SendOptions{ParseMode: tele.ModeHTML}) //nolint:errcheck // fire-and-forget telegram send
		}
		return nil
	}

	// Standard command handling
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result := mgr.Execute(ctx, text, sessionKey)

	// Telegram has a 4096 char limit, truncate if needed
	msg := FormatMessage(result.Markdown)
	if len(msg) > 4000 {
		msg = msg[:4000] + "..."
	}

	return c.Send(msg, &tele.SendOptions{ParseMode: tele.ModeHTML})
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

	// Check if this is a command - route to global command manager
	if commands.IsCommand(c.Text()) {
		return b.handleCommand(c, u)
	}

	// Show typing indicator
	_ = c.Notify(tele.Typing)

	// Get chat preferences for thinking level
	prefs := b.getChatPrefs(chatID, u)

	// Create agent request with media callback
	req := gateway.AgentRequest{
		User:           u,
		Source:         "telegram",
		ChatID:         fmt.Sprintf("%d", chatID),
		IsGroup:        isGroup,
		UserMsg:        c.Text(),
		EnableThinking: prefs.ShowThinking,  // Extended thinking based on chat preference
		ThinkingLevel:  prefs.ThinkingLevel, // Thinking intensity level
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
	imageData, err := media.DownloadAndOptimize(b.ctx, b.bot, photo)
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

	// Get chat preferences for thinking level
	prefs := b.getChatPrefs(chatID, u)

	// Create agent request with image and media callback
	req := gateway.AgentRequest{
		User:           u,
		Source:         "telegram",
		ChatID:         fmt.Sprintf("%d", chatID),
		IsGroup:        isGroup,
		UserMsg:        caption,
		Images:         []session.ImageAttachment{imageAttachment},
		EnableThinking: prefs.ShowThinking,  // Extended thinking based on chat preference
		ThinkingLevel:  prefs.ThinkingLevel, // Thinking intensity level
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

	// Thinking delta tracking
	var thinkingBuf strings.Builder
	var thinkingMsg *tele.Message
	var lastThinkingUpdate time.Time

	// Get thinking mode preference upfront
	userID := fmt.Sprintf("%d", c.Sender().ID)
	u := b.users.FromIdentity("telegram", userID)
	prefs := b.getChatPrefs(c.Chat().ID, u)
	bufferMode := prefs.ShowThinking // When thinking is ON, buffer response until end

	L_debug("telegram: starting response stream", "chatID", c.Chat().ID, "bufferMode", bufferMode)

	for event := range events {
		switch e := event.(type) {
		case gateway.EventTextDelta:
			response.WriteString(e.Delta)

			// In buffer mode (thinking ON): just accumulate, don't stream
			// This ensures tools appear before the response in the timeline
			if bufferMode {
				continue
			}

			// Normal streaming mode (thinking OFF)
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

			// Flush thinking buffer before showing tool (ensures thinking is complete)
			if prefs.ShowThinking && thinkingBuf.Len() > 0 && thinkingMsg != nil {
				thinkingText := fmt.Sprintf("💭 <i>%s</i>", html.EscapeString(thinkingBuf.String()))
				_, err := b.bot.Edit(thinkingMsg, thinkingText, &tele.SendOptions{ParseMode: tele.ModeHTML})
				if err != nil {
					L_trace("telegram: thinking flush on tool start failed", "error", err)
				}
			}

			// Show tool start if thinking mode is on
			if prefs.ShowThinking {
				inputStr := string(e.Input)
				if len(inputStr) > 1024 {
					inputStr = inputStr[:1024] + "..."
				}
				toolMsg := fmt.Sprintf("⚙️ <b>%s</b>\n<code>%s</code>",
					escapeHTML(e.ToolName), escapeHTML(inputStr))
				_, _ = b.bot.Send(c.Chat(), toolMsg, &tele.SendOptions{ParseMode: tele.ModeHTML})
			}

		case gateway.EventToolEnd:
			L_debug("telegram: tool ended", "tool", e.ToolName, "hasError", e.Error != "")

			// Show tool result if thinking mode is on
			if prefs.ShowThinking {
				status := "✓"
				duration := ""
				if e.DurationMs > 0 {
					duration = fmt.Sprintf(" (%dms)", e.DurationMs)
				}
				if e.Error != "" {
					status = "✗"
				}

				result := e.Result
				if e.Error != "" {
					result = e.Error
				}
				if len(result) > 1024 {
					result = result[:1024] + "..."
				}

				toolMsg := fmt.Sprintf("%s Completed%s", status, duration)
				if result != "" {
					toolMsg += fmt.Sprintf("\n<code>%s</code>", escapeHTML(result))
				}
				_, _ = b.bot.Send(c.Chat(), toolMsg, &tele.SendOptions{ParseMode: tele.ModeHTML})
			}

		case gateway.EventThinkingDelta:
			// Accumulate thinking deltas if thinking mode is on
			if prefs.ShowThinking {
				thinkingBuf.WriteString(e.Delta)

				// Update thinking message periodically
				if time.Since(lastThinkingUpdate) > updateInterval {
					thinkingText := fmt.Sprintf("💭 <i>%s</i>", html.EscapeString(thinkingBuf.String()))
					if thinkingMsg == nil {
						msg, err := b.bot.Send(c.Chat(), thinkingText, &tele.SendOptions{ParseMode: tele.ModeHTML})
						if err != nil {
							L_error("telegram: failed to send thinking message", "error", err)
						} else {
							thinkingMsg = msg
						}
					} else {
						_, err := b.bot.Edit(thinkingMsg, thinkingText, &tele.SendOptions{ParseMode: tele.ModeHTML})
						if err != nil {
							L_trace("telegram: thinking edit failed", "error", err)
						}
					}
					lastThinkingUpdate = time.Now()
				}
			}

		case gateway.EventThinking:
			L_debug("telegram: thinking", "contentLen", len(e.Content))

			// Final thinking content - always update/send with complete content
			if prefs.ShowThinking && e.Content != "" {
				thinkingText := fmt.Sprintf("💭 <i>%s</i>", html.EscapeString(e.Content))
				if thinkingMsg != nil {
					// Update existing message with complete content
					_, err := b.bot.Edit(thinkingMsg, thinkingText, &tele.SendOptions{ParseMode: tele.ModeHTML})
					if err != nil {
						L_trace("telegram: thinking final edit failed", "error", err)
					}
				} else {
					// No streaming message exists, send new one
					msg, err := b.bot.Send(c.Chat(), thinkingText, &tele.SendOptions{ParseMode: tele.ModeHTML})
					if err != nil {
						L_trace("telegram: thinking final send failed", "error", err)
					} else {
						thinkingMsg = msg
					}
				}
			}

		case gateway.EventAgentEnd:
			// Use enriched finalText from event (has media refs processed)
			// instead of accumulated response.String() which has raw refs
			finalText := e.FinalText
			if finalText == "" {
				finalText = response.String() // fallback to accumulated
			}
			if finalText == "" {
				finalText = "(No response)"
			}

			elapsed := time.Since(startTime)
			L_debug("telegram: agent completed",
				"responseLength", len(finalText),
				"editCount", editCount,
				"elapsed", elapsed.Round(time.Millisecond),
			)

			// Check for inline media references
			if containsMediaRefs(finalText) {
				L_debug("telegram: response contains media refs, sending with media")
				// Delete the streaming message if we have one (we'll send fresh)
				if currentMsg != nil {
					_ = b.bot.Delete(currentMsg)
				}
				// Send text/media segments
				if err := b.sendWithMediaRefs(c.Chat(), finalText); err != nil {
					L_error("telegram: failed to send with media", "error", err)
				}
			} else {
				// No media refs - send as regular text
				// Convert markdown to Telegram HTML
				formattedText := FormatMessage(finalText)

				L_trace("telegram: formatting message",
					"rawMarkdown", finalText,
					"formattedHTML", formattedText)

				// Split long messages to fit Telegram's 4096 char limit
				chunks := splitMessage(finalText, maxTelegramMessage)

				if currentMsg == nil {
					// Send all chunks as new messages
					for i, chunk := range chunks {
						formatted := FormatMessage(chunk)
						_, err := b.bot.Send(c.Chat(), formatted, &tele.SendOptions{ParseMode: tele.ModeHTML})
						if err != nil {
							L_debug("telegram: HTML send failed, falling back to plain text", "error", err, "chunk", i+1)
							_, err = b.bot.Send(c.Chat(), chunk)
							if err != nil {
								L_error("telegram: failed to send message chunk", "error", err, "chunk", i+1)
							}
						}
					}
				} else {
					// Edit first chunk into existing message, send rest as new
					formatted := FormatMessage(chunks[0])
					_, err := b.bot.Edit(currentMsg, formatted, &tele.SendOptions{ParseMode: tele.ModeHTML})
					if err != nil {
						L_debug("telegram: HTML edit failed, falling back to plain text", "error", err)
						_, err = b.bot.Edit(currentMsg, chunks[0])
						if err != nil {
							L_debug("telegram: failed to edit final message", "error", err)
						}
					}
					// Send remaining chunks as new messages
					for i := 1; i < len(chunks); i++ {
						formatted := FormatMessage(chunks[i])
						_, err := b.bot.Send(c.Chat(), formatted, &tele.SendOptions{ParseMode: tele.ModeHTML})
						if err != nil {
							L_debug("telegram: HTML send failed, falling back to plain text", "error", err, "chunk", i+1)
							_, err = b.bot.Send(c.Chat(), chunks[i])
							if err != nil {
								L_error("telegram: failed to send message chunk", "error", err, "chunk", i+1)
							}
						}
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
	L_info("telegram: starting polling", "bot", "@"+b.bot.Me.Username)
	go b.bot.Start()
}

// RegisterOperationalCommands registers runtime commands for this bot instance.
func (b *Bot) RegisterOperationalCommands() {
	bus.RegisterCommand("telegram", "status", b.handleStatusCommand)
}

// handleStatusCommand returns the current bot status
func (b *Bot) handleStatusCommand(cmd bus.Command) bus.CommandResult {
	username := b.bot.Me.Username
	return bus.CommandResult{
		Success: true,
		Message: fmt.Sprintf("Telegram bot @%s is running", username),
		Data: map[string]any{
			"connected": true,
			"username":  username,
			"botID":     b.bot.Me.ID,
		},
	}
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
	// Send to owner's chat
	owner := b.users.Owner()
	if owner == nil || owner.TelegramID == "" {
		return nil
	}
	var chatID int64
	if _, err := fmt.Sscanf(owner.TelegramID, "%d", &chatID); err != nil {
		L_warn("telegram: invalid owner telegram ID", "telegramID", owner.TelegramID, "error", err)
		return nil
	}
	_, err := b.SendText(chatID, msg)
	return err
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

// mediaRefPattern matches enriched media refs: {{media:mime:'path'}}
var mediaRefPattern = regexp.MustCompile(`\{\{media:([a-z]+/[a-z0-9.+-]+):'((?:[^'\\]|\\.)*)'\}\}`)

// containsMediaRefs checks if text contains any media references
func containsMediaRefs(text string) bool {
	return mediaRefPattern.MatchString(text)
}

// mediaSegment represents a segment of text or media
type mediaSegment struct {
	IsMedia bool
	Text    string // for text segments
	Path    string // for media segments
	Mime    string // for media segments
}

// splitMediaSegments splits text into text and media segments
func splitMediaSegments(text string) []mediaSegment {
	var segments []mediaSegment
	lastIndex := 0

	matches := mediaRefPattern.FindAllStringSubmatchIndex(text, -1)
	for _, match := range matches {
		// Text before this match
		if match[0] > lastIndex {
			textBefore := strings.TrimSpace(text[lastIndex:match[0]])
			if textBefore != "" {
				segments = append(segments, mediaSegment{Text: textBefore})
			}
		}

		// Extract mime and path from match
		mime := text[match[2]:match[3]]
		escapedPath := text[match[4]:match[5]]
		path := media.UnescapePath(escapedPath)

		segments = append(segments, mediaSegment{
			IsMedia: true,
			Path:    path,
			Mime:    mime,
		})

		lastIndex = match[1]
	}

	// Text after last match
	if lastIndex < len(text) {
		textAfter := strings.TrimSpace(text[lastIndex:])
		if textAfter != "" {
			segments = append(segments, mediaSegment{Text: textAfter})
		}
	}

	return segments
}

// sendWithMediaRefs parses and sends text with inline media references
// Supports captions (preceding text < 1024 chars) and albums (consecutive images)
func (b *Bot) sendWithMediaRefs(chat *tele.Chat, text string) error {
	segments := splitMediaSegments(text)

	// Get media root
	var mediaRoot string
	if b.gateway != nil && b.gateway.MediaStore() != nil {
		mediaRoot = b.gateway.MediaStore().BaseDir()
	}

	i := 0
	for i < len(segments) {
		seg := segments[i]

		if !seg.IsMedia {
			// Text segment - check if next segment is media for caption attachment
			if i+1 < len(segments) && segments[i+1].IsMedia && !strings.HasPrefix(segments[i+1].Mime, "error/") {
				// Check if text is short enough for caption
				if len(seg.Text) <= TelegramCaptionLimit {
					// Look ahead for consecutive images (for album)
					imageSegments := b.collectConsecutiveImages(segments, i+1)

					if len(imageSegments) > 1 {
						// Album with caption
						b.sendAlbum(chat, mediaRoot, imageSegments, seg.Text)
						i += 1 + len(imageSegments) // skip text + all images
						continue
					}

					// Single media with caption
					nextSeg := segments[i+1]
					absPath, err := media.ResolveMediaPath(mediaRoot, nextSeg.Path)
					if err != nil {
						// Send text separately, then continue
						_, _ = b.sendWithHTMLFallback(chat, seg.Text)
						i++
						continue
					}

					b.sendMediaByMime(chat.ID, absPath, nextSeg.Mime, seg.Text)
					i += 2 // skip both text and media
					continue
				}
			}

			// No media follows, or text too long - send text separately
			if seg.Text != "" {
				_, _ = b.sendWithHTMLFallback(chat, seg.Text)
			}
			i++
			continue
		}

		// Media segment (not preceded by suitable caption text)
		// Handle error mimes
		if strings.HasPrefix(seg.Mime, "error/") {
			errType := strings.TrimPrefix(seg.Mime, "error/")
			errMsg := fmt.Sprintf("[Media %s: %s]", errType, seg.Path)
			_, _ = b.sendWithHTMLFallback(chat, errMsg)
			i++
			continue
		}

		// Check for consecutive images (album without caption)
		imageSegments := b.collectConsecutiveImages(segments, i)
		if len(imageSegments) > 1 {
			b.sendAlbum(chat, mediaRoot, imageSegments, "")
			i += len(imageSegments)
			continue
		}

		// Single media without caption
		absPath, err := media.ResolveMediaPath(mediaRoot, seg.Path)
		if err != nil {
			L_warn("telegram: failed to resolve media path", "path", seg.Path, "error", err)
			i++
			continue
		}

		b.sendMediaByMime(chat.ID, absPath, seg.Mime, "")
		i++
	}

	return nil
}

// collectConsecutiveImages collects consecutive image segments starting at index
func (b *Bot) collectConsecutiveImages(segments []mediaSegment, startIdx int) []mediaSegment {
	var images []mediaSegment
	for j := startIdx; j < len(segments); j++ {
		seg := segments[j]
		if !seg.IsMedia {
			break
		}
		if strings.HasPrefix(seg.Mime, "error/") {
			break
		}
		if !strings.HasPrefix(seg.Mime, "image/") {
			break
		}
		images = append(images, seg)
	}
	return images
}

// sendAlbum sends multiple images as a Telegram album
func (b *Bot) sendAlbum(chat *tele.Chat, mediaRoot string, segments []mediaSegment, caption string) {
	if len(segments) == 0 {
		return
	}

	// Telegram album max is 10 items
	maxItems := 10
	if len(segments) > maxItems {
		segments = segments[:maxItems]
	}

	var album tele.Album
	for i, seg := range segments {
		absPath, err := media.ResolveMediaPath(mediaRoot, seg.Path)
		if err != nil {
			L_warn("telegram: failed to resolve album item path", "path", seg.Path, "error", err)
			continue
		}

		photo := &tele.Photo{File: tele.FromDisk(absPath)}
		// Caption only on first item, formatted as HTML
		if i == 0 && caption != "" {
			photo.Caption = FormatMessage(caption)
		}
		album = append(album, photo)
	}

	if len(album) == 0 {
		return
	}

	_, err := b.bot.SendAlbum(chat, album, &tele.SendOptions{ParseMode: tele.ModeHTML})
	if err != nil {
		L_warn("telegram: failed to send album", "count", len(album), "error", err)
		// Fallback: send individually
		for i, seg := range segments {
			absPath, _ := media.ResolveMediaPath(mediaRoot, seg.Path)
			cap := ""
			if i == 0 {
				cap = caption
			}
			b.sendMediaByMime(chat.ID, absPath, seg.Mime, cap)
		}
	} else {
		L_debug("telegram: sent album", "count", len(album), "hasCaption", caption != "")
	}
}

// sendMediaByMime sends media based on mimetype with optional caption
func (b *Bot) sendMediaByMime(chatID int64, absPath, mime, caption string) {
	switch {
	case strings.HasPrefix(mime, "image/"):
		if err := b.SendPhoto(chatID, absPath, caption); err != nil {
			L_warn("telegram: failed to send photo", "path", absPath, "error", err)
		}
	case strings.HasPrefix(mime, "video/"):
		if err := b.SendVideo(chatID, absPath, caption); err != nil {
			L_warn("telegram: failed to send video", "path", absPath, "error", err)
		}
	case strings.HasPrefix(mime, "audio/"):
		if err := b.SendAudio(chatID, absPath, caption); err != nil {
			L_warn("telegram: failed to send audio", "path", absPath, "error", err)
		}
	default:
		if err := b.SendDocument(chatID, absPath, caption); err != nil {
			L_warn("telegram: failed to send document", "path", absPath, "error", err)
		}
	}
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
	telegramID := owner.TelegramID
	if telegramID == "" {
		return nil
	}

	// Parse telegram ID to int64
	var chatID int64
	if _, err := fmt.Sscanf(telegramID, "%d", &chatID); err != nil {
		L_warn("telegram: invalid telegram ID for mirror", "telegramID", telegramID, "error", err)
		return nil
	}
	chat := &tele.Chat{ID: chatID}

	// Telegram max message is 4096 chars. Reserve space for formatting.
	const maxTelegramMsg = 4096
	agentName := b.gateway.AgentIdentity().Name
	headerReserve := 80 + len(agentName) // for "📱 <b>source</b>\n\n<b>You:</b> ...\n\n<b>AgentName:</b> "

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

	mirror := fmt.Sprintf("📱 <b>%s</b>\n\n<b>You:</b> %s\n\n<b>%s:</b> %s",
		source, escapedUser, agentName, escapedResponse)

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
		plainMirror := fmt.Sprintf("📱 %s\n\nYou: %s\n\n%s: %s",
			source, truncatedUser, agentName, truncatedResponse)
		_, err = b.bot.Send(chat, plainMirror)
	}
	if err != nil {
		L_error("failed to send telegram mirror", "error", err)
	}
	return err
}

// HasUser returns true if the user has a Telegram identity
func (b *Bot) HasUser(u *user.User) bool {
	return u.HasTelegramAuth()
}

// StreamEvent returns false - Telegram is batch-only, doesn't support real-time streaming.
func (b *Bot) StreamEvent(u *user.User, event gateway.AgentEvent) bool {
	return false // Telegram doesn't stream events
}

// DeliverGhostwrite sends a ghostwritten message with typing simulation.
func (b *Bot) DeliverGhostwrite(ctx context.Context, u *user.User, message string) error {
	if u == nil || u.TelegramID == "" {
		return nil // User doesn't have Telegram
	}

	// Parse telegram ID
	chatID, err := strconv.ParseInt(u.TelegramID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid telegram ID: %s", u.TelegramID)
	}

	chat := &tele.Chat{ID: chatID}

	L_info("telegram: ghostwrite", "user", u.ID, "chatID", chatID, "messageLen", len(message))

	// Send typing indicator
	_ = b.bot.Notify(chat, tele.Typing)

	// Get typing delay from config
	typingDelay := 500 * time.Millisecond // default
	if b.gateway != nil {
		if cfg := b.gateway.Config(); cfg != nil && cfg.Supervision.Ghostwriting.TypingDelayMs > 0 {
			typingDelay = time.Duration(cfg.Supervision.Ghostwriting.TypingDelayMs) * time.Millisecond
		}
	}

	// Wait for typing delay (simulates thinking/typing)
	time.Sleep(typingDelay)

	// Send the message
	_, err = b.SendText(chatID, message)
	if err != nil {
		return fmt.Errorf("failed to send ghostwrite: %w", err)
	}
	L_info("telegram: ghostwrite delivered", "user", u.ID, "messageLen", len(message))
	return nil
}

// SendText sends a text message to a chat, splitting if necessary.
// Returns the last sent message for potential editing/deletion.
func (b *Bot) SendText(chatID int64, text string) (*tele.Message, error) {
	chat := &tele.Chat{ID: chatID}

	// Split long messages
	chunks := splitMessage(text, maxTelegramMessage)
	var lastMsg *tele.Message

	for i, chunk := range chunks {
		formatted := FormatMessage(chunk)
		msg, err := b.bot.Send(chat, formatted, &tele.SendOptions{ParseMode: tele.ModeHTML})
		if err != nil {
			// Fallback to plain text
			L_debug("telegram: HTML send failed, falling back to plain text", "error", err, "chunk", i+1)
			msg, err = b.bot.Send(chat, chunk)
		}
		if err != nil {
			return lastMsg, fmt.Errorf("failed to send text chunk %d: %w", i+1, err)
		}
		lastMsg = msg
		L_debug("telegram: sent text message", "chatID", chatID, "msgID", msg.ID, "chunk", i+1, "length", len(chunk))
	}

	return lastMsg, nil
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

// escapeHTML escapes HTML special characters for Telegram HTML mode
func escapeHTML(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	)
	return replacer.Replace(s)
}

// maxTelegramMessage is the maximum message length for Telegram (4096 chars).
// We use 4000 to leave room for formatting overhead.
const maxTelegramMessage = 4000

// splitMessage splits a long message into chunks that fit within Telegram's limit.
// It tries to split at natural boundaries: paragraphs, then sentences, then words.
func splitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	remaining := text

	for len(remaining) > 0 {
		if len(remaining) <= maxLen {
			chunks = append(chunks, remaining)
			break
		}

		// Find a good split point within maxLen
		splitAt := findSplitPoint(remaining, maxLen)
		chunks = append(chunks, strings.TrimSpace(remaining[:splitAt]))
		remaining = strings.TrimSpace(remaining[splitAt:])
	}

	return chunks
}

// findSplitPoint finds the best position to split text, preferring natural boundaries.
func findSplitPoint(text string, maxLen int) int {
	if len(text) <= maxLen {
		return len(text)
	}

	searchArea := text[:maxLen]

	// Try to split at paragraph boundary (double newline)
	if idx := strings.LastIndex(searchArea, "\n\n"); idx > maxLen/2 {
		return idx + 2 // Include the newlines
	}

	// Try to split at single newline
	if idx := strings.LastIndex(searchArea, "\n"); idx > maxLen/2 {
		return idx + 1
	}

	// Try to split at sentence boundary (. ! ?)
	for _, sep := range []string{". ", "! ", "? "} {
		if idx := strings.LastIndex(searchArea, sep); idx > maxLen/2 {
			return idx + len(sep)
		}
	}

	// Try to split at word boundary (space)
	if idx := strings.LastIndex(searchArea, " "); idx > maxLen/2 {
		return idx + 1
	}

	// Fallback: hard split at maxLen
	return maxLen
}
