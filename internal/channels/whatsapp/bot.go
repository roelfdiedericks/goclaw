// Package whatsapp provides the WhatsApp channel adapter for GoClaw.
package whatsapp

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	chtypes "github.com/roelfdiedericks/goclaw/internal/channels/types"
	"github.com/roelfdiedericks/goclaw/internal/channels/whatsapp/config"
	"github.com/roelfdiedericks/goclaw/internal/commands"
	"github.com/roelfdiedericks/goclaw/internal/gateway"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/paths"
	"github.com/roelfdiedericks/goclaw/internal/session"
	itypes "github.com/roelfdiedericks/goclaw/internal/types"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

const maxWhatsAppMessage = 65536

// ChatPreferences stores per-chat preferences (mirrors Telegram)
type ChatPreferences struct {
	ShowThinking  bool
	ThinkingLevel string
}

// Bot represents the WhatsApp channel
type Bot struct {
	client  *whatsmeow.Client
	gateway *gateway.Gateway
	users   *user.Registry
	config  *config.Config
	store   *sqlstore.Container

	chatPrefs sync.Map // JID string -> *ChatPreferences

	ctx    context.Context
	cancel context.CancelFunc

	mu        sync.RWMutex
	running   bool
	startedAt time.Time
	lastError error
}

// goclawLogger bridges whatsmeow's waLog.Logger to our L_* functions
type goclawLogger struct {
	module string
}

func (l *goclawLogger) Debugf(msg string, args ...interface{}) {
	L_debug(fmt.Sprintf("whatsmeow/%s: %s", l.module, fmt.Sprintf(msg, args...)))
}

func (l *goclawLogger) Infof(msg string, args ...interface{}) {
	L_info(fmt.Sprintf("whatsmeow/%s: %s", l.module, fmt.Sprintf(msg, args...)))
}

func (l *goclawLogger) Warnf(msg string, args ...interface{}) {
	L_warn(fmt.Sprintf("whatsmeow/%s: %s", l.module, fmt.Sprintf(msg, args...)))
}

func (l *goclawLogger) Errorf(msg string, args ...interface{}) {
	L_error(fmt.Sprintf("whatsmeow/%s: %s", l.module, fmt.Sprintf(msg, args...)))
}

func (l *goclawLogger) Sub(module string) waLog.Logger {
	return &goclawLogger{module: l.module + "/" + module}
}

// New creates a new WhatsApp bot
func New(cfg *config.Config, gw *gateway.Gateway, users *user.Registry) (*Bot, error) {
	dbPath, err := paths.DataPath("whatsapp.db")
	if err != nil {
		return nil, fmt.Errorf("failed to resolve whatsapp db path: %w", err)
	}

	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("failed to open whatsapp db: %w", err)
	}

	storeLog := &goclawLogger{module: "store"}
	container := sqlstore.NewWithDB(db, "sqlite3", storeLog)

	if err := container.Upgrade(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to upgrade whatsapp store: %w", err)
	}

	device, err := container.GetFirstDevice(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get whatsapp device: %w", err)
	}

	if device == nil {
		return nil, fmt.Errorf("no whatsapp device paired — run 'goclaw whatsapp link' first")
	}

	clientLog := &goclawLogger{module: "client"}
	client := whatsmeow.NewClient(device, clientLog)

	ctx, cancel := context.WithCancel(context.Background())

	b := &Bot{
		client:  client,
		gateway: gw,
		users:   users,
		config:  cfg,
		store:   container,
		ctx:     ctx,
		cancel:  cancel,
	}

	return b, nil
}

// Start connects to WhatsApp and starts listening (implements ManagedChannel)
func (b *Bot) Start(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.running {
		return nil
	}

	b.client.AddEventHandler(b.handleEvent)

	if err := b.client.Connect(); err != nil {
		b.lastError = err
		return fmt.Errorf("whatsapp: failed to connect: %w", err)
	}

	b.running = true
	b.startedAt = time.Now()
	b.lastError = nil

	L_info("whatsapp: connected", "jid", b.client.Store.ID)
	return nil
}

// RegisterOperationalCommands registers runtime commands for this bot instance
func (b *Bot) RegisterOperationalCommands() {
	bus.RegisterCommand("whatsapp", "status", b.handleStatusCommand)
}

func (b *Bot) handleStatusCommand(cmd bus.Command) bus.CommandResult {
	jid := ""
	if b.client.Store.ID != nil {
		jid = b.client.Store.ID.String()
	}
	return bus.CommandResult{
		Success: true,
		Message: fmt.Sprintf("WhatsApp connected as %s", jid),
		Data: map[string]any{
			"connected": b.client.IsConnected(),
			"jid":       jid,
		},
	}
}

// Stop disconnects from WhatsApp (implements ManagedChannel)
func (b *Bot) Stop() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.running {
		return nil
	}

	L_info("whatsapp: disconnecting")
	b.cancel()
	b.client.Disconnect()
	b.running = false
	return nil
}

// Reload applies new configuration (implements ManagedChannel)
func (b *Bot) Reload(cfg any) error {
	newCfg, ok := cfg.(*config.Config)
	if !ok {
		return fmt.Errorf("expected *whatsapp.Config, got %T", cfg)
	}

	b.mu.Lock()
	wasRunning := b.running
	b.mu.Unlock()

	if wasRunning {
		if err := b.Stop(); err != nil {
			return fmt.Errorf("failed to stop for reload: %w", err)
		}
	}

	b.config = newCfg

	if wasRunning && newCfg.Enabled {
		b.ctx, b.cancel = context.WithCancel(context.Background())
		return b.Start(b.ctx)
	}

	return nil
}

// Status returns current channel status (implements ManagedChannel)
func (b *Bot) Status() chtypes.ChannelStatus {
	b.mu.RLock()
	defer b.mu.RUnlock()

	info := ""
	if b.client.Store.ID != nil {
		info = b.client.Store.ID.User
	}

	return chtypes.ChannelStatus{
		Running:   b.running,
		Connected: b.client.IsConnected(),
		Error:     b.lastError,
		StartedAt: b.startedAt,
		Info:      info,
	}
}

// Name returns the channel name (implements gateway.Channel)
func (b *Bot) Name() string {
	return "whatsapp"
}

// Send sends a message to the owner's WhatsApp chat (implements gateway.Channel)
func (b *Bot) Send(ctx context.Context, msg string) error {
	owner := b.users.Owner()
	if owner == nil || owner.WhatsAppID == "" {
		return nil
	}
	jid := phoneToJID(owner.WhatsAppID)
	formatted := FormatMessage(msg)
	_, err := b.client.SendMessage(ctx, jid, &waE2E.Message{
		Conversation: proto.String(formatted),
	})
	return err
}

// SendMirror sends a cross-channel mirror summary to the owner (implements gateway.Channel)
func (b *Bot) SendMirror(ctx context.Context, source, userMsg, response string) error {
	owner := b.users.Owner()
	if owner == nil || owner.WhatsAppID == "" {
		return nil
	}
	jid := phoneToJID(owner.WhatsAppID)

	agentName := b.gateway.AgentIdentity().Name
	truncatedUser := truncate(userMsg, 500)
	truncatedResponse := truncate(response, maxWhatsAppMessage-600)
	formattedResponse := FormatMessage(truncatedResponse)

	mirror := fmt.Sprintf("*%s*\n\n*You:* %s\n\n*%s:* %s",
		source, truncatedUser, agentName, formattedResponse)

	_, err := b.client.SendMessage(ctx, jid, &waE2E.Message{
		Conversation: proto.String(mirror),
	})
	if err != nil {
		L_error("whatsapp: failed to send mirror", "error", err)
	}
	return err
}

// HasUser returns true if the user has a WhatsApp identity (implements gateway.Channel)
func (b *Bot) HasUser(u *user.User) bool {
	return u.HasWhatsAppAuth()
}

// StreamEvent returns false — WhatsApp is batch-only (implements gateway.Channel)
func (b *Bot) StreamEvent(u *user.User, event gateway.AgentEvent) bool {
	return false
}

// DeliverGhostwrite sends a ghostwritten message with typing simulation (implements gateway.Channel)
func (b *Bot) DeliverGhostwrite(ctx context.Context, u *user.User, message string) error {
	if u == nil || u.WhatsAppID == "" {
		return nil
	}
	jid := phoneToJID(u.WhatsAppID)

	L_info("whatsapp: ghostwrite", "user", u.ID, "jid", jid.String(), "messageLen", len(message))

	_ = b.client.SendChatPresence(ctx, jid, types.ChatPresenceComposing, types.ChatPresenceMediaText)

	typingDelay := 500 * time.Millisecond
	if b.gateway != nil {
		if cfg := b.gateway.Config(); cfg != nil && cfg.Supervision.Ghostwriting.TypingDelayMs > 0 {
			typingDelay = time.Duration(cfg.Supervision.Ghostwriting.TypingDelayMs) * time.Millisecond
		}
	}
	time.Sleep(typingDelay)

	formatted := FormatMessage(message)
	_, err := b.client.SendMessage(ctx, jid, &waE2E.Message{
		Conversation: proto.String(formatted),
	})
	if err != nil {
		return fmt.Errorf("failed to send ghostwrite: %w", err)
	}

	_ = b.client.SendChatPresence(ctx, jid, types.ChatPresencePaused, types.ChatPresenceMediaText)
	L_info("whatsapp: ghostwrite delivered", "user", u.ID, "messageLen", len(message))
	return nil
}

// getChatPrefs returns preferences for a chat, initializing from user prefs if needed
func (b *Bot) getChatPrefs(jidStr string, u *user.User) *ChatPreferences {
	if prefs, ok := b.chatPrefs.Load(jidStr); ok {
		return prefs.(*ChatPreferences) //nolint:errcheck // type assertion safe
	}
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
	b.chatPrefs.Store(jidStr, prefs)
	return prefs
}

// getSessionKey returns the session key for a user
func (b *Bot) getSessionKey(u *user.User) string {
	if u.Role == "owner" {
		return session.PrimarySession
	}
	return fmt.Sprintf("user:%s", u.ID)
}

// canUserUseCommands checks if the user has permission to use slash commands
func (b *Bot) canUserUseCommands(u *user.User) bool {
	if u == nil {
		return false
	}
	resolvedRole, err := b.users.ResolveUserRole(u)
	if err != nil {
		L_warn("whatsapp: failed to resolve role for command check", "user", u.Name, "error", err)
		return false
	}
	return resolvedRole.CanUseCommands()
}

// handleEvent is the whatsmeow event handler
func (b *Bot) handleEvent(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		b.handleMessage(v)
	case *events.Connected:
		L_info("whatsapp: connected to server")
	case *events.Disconnected:
		L_warn("whatsapp: disconnected from server")
	case *events.LoggedOut:
		L_error("whatsapp: logged out — re-pair with 'goclaw whatsapp link'",
			"reason", v.Reason)
		b.mu.Lock()
		b.lastError = fmt.Errorf("logged out: %v", v.Reason)
		b.mu.Unlock()
	}
}

// handleMessage processes an incoming WhatsApp message
func (b *Bot) handleMessage(evt *events.Message) {
	// Ignore group messages
	if evt.Info.IsGroup {
		L_debug("whatsapp: ignoring group message")
		return
	}

	// Ignore own messages
	if evt.Info.IsFromMe {
		return
	}

	// WhatsApp may deliver messages with LID addressing, where Sender is
	// a LID (e.g. 249786758348836@lid) and SenderAlt has the phone number,
	// or vice versa. Try both to resolve the user.
	senderJID := evt.Info.Sender.User
	senderAlt := evt.Info.SenderAlt.User
	L_debug("whatsapp: message received", "sender", senderJID, "senderAlt", senderAlt, "addressingMode", evt.Info.AddressingMode)

	u := b.users.FromIdentity("whatsapp", senderJID)
	if u == nil && senderAlt != "" {
		u = b.users.FromIdentity("whatsapp", senderAlt)
	}
	if u == nil {
		L_warn("whatsapp: unknown user ignored", "sender", senderJID, "senderAlt", senderAlt)
		return
	}

	L_info("whatsapp: authenticated message", "user", u.Name, "role", u.Role)

	// Extract text, voice, or image
	msg := evt.Message
	text := ""
	var contentBlocks []itypes.ContentBlock

	if msg.GetConversation() != "" {
		text = msg.GetConversation()
	} else if msg.GetExtendedTextMessage() != nil {
		text = msg.GetExtendedTextMessage().GetText()
	} else if audioMsg := msg.GetAudioMessage(); audioMsg != nil && audioMsg.GetPTT() {
		block, err := b.downloadMedia(audioMsg, "voice", ".ogg", audioMsg.GetMimetype())
		if err != nil {
			L_error("whatsapp: failed to download voice", "error", err)
			return
		}
		contentBlocks = append(contentBlocks, *block)
		text = "[Voice note received]"
	} else if imageMsg := msg.GetImageMessage(); imageMsg != nil {
		block, err := b.downloadMedia(imageMsg, "image", mimeToExt(imageMsg.GetMimetype()), imageMsg.GetMimetype())
		if err != nil {
			L_error("whatsapp: failed to download image", "error", err)
			return
		}
		contentBlocks = append(contentBlocks, *block)
		caption := imageMsg.GetCaption()
		if caption != "" {
			text = caption
		} else {
			text = "<media:image>"
		}
	} else {
		L_debug("whatsapp: unsupported message type, ignoring")
		return
	}

	// Check for panic phrase (emergency stop) before commands
	if commands.IsPanicPhrase(text) {
		cancelled, _ := b.gateway.StopAllUserSessions(u.ID)
		chatJID := evt.Info.Chat
		var msg string
		if cancelled > 0 {
			msg = "Stopping all tasks."
		} else {
			msg = "Nothing running."
		}
		b.client.SendMessage(b.ctx, chatJID, &waE2E.Message{Conversation: proto.String(msg)}) //nolint:errcheck
		return
	}

	// Check for commands
	if commands.IsCommand(text) {
		b.handleCommand(u, evt, text)
		return
	}

	// Check for /thinking (channel-specific)
	if strings.HasPrefix(text, "/thinking") {
		b.handleThinkingCommand(u, evt, text)
		return
	}

	// Send typing indicator
	chatJID := evt.Info.Chat
	_ = b.client.SendChatPresence(b.ctx, chatJID, types.ChatPresenceComposing, types.ChatPresenceMediaText)

	prefs := b.getChatPrefs(senderJID, u)

	req := gateway.AgentRequest{
		User:           u,
		Source:         "whatsapp",
		ChatID:         senderJID,
		IsGroup:        false,
		UserMsg:        text,
		ContentBlocks:  contentBlocks,
		EnableThinking: prefs.ShowThinking,
		ThinkingLevel:  prefs.ThinkingLevel,
		OnMediaToSend: func(path, caption string) error {
			return b.sendMediaFile(chatJID, path, caption)
		},
	}

	evChan := make(chan gateway.AgentEvent, 100)

	go func() {
		if err := b.gateway.RunAgent(b.ctx, req, evChan); err != nil {
			L_error("whatsapp: agent error", "error", err)
		}
	}()

	b.processEvents(evt, evChan, prefs)
}

// handleCommand routes commands to the global command manager
func (b *Bot) handleCommand(u *user.User, evt *events.Message, text string) {
	if !b.canUserUseCommands(u) {
		L_debug("whatsapp: commands disabled for user", "user", u.Name, "command", text)
		return
	}

	sessionKey := b.getSessionKey(u)
	mgr := commands.GetManager()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result := mgr.Execute(ctx, text, sessionKey, u.ID)
	formatted := FormatMessage(result.Markdown)

	chatJID := evt.Info.Chat
	_, err := b.client.SendMessage(b.ctx, chatJID, &waE2E.Message{
		Conversation: proto.String(formatted),
	})
	if err != nil {
		L_error("whatsapp: failed to send command result", "error", err)
	}
}

// handleThinkingCommand handles the /thinking channel preference toggle
func (b *Bot) handleThinkingCommand(u *user.User, evt *events.Message, text string) {
	if !b.canUserUseCommands(u) {
		return
	}

	senderJID := evt.Info.Sender.User
	chatJID := evt.Info.Chat
	prefs := b.getChatPrefs(senderJID, u)

	arg := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(text, "/thinking")))

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
		resultMsg = "Thinking output disabled."
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
		if llm.IsValidThinkingLevel(arg) {
			prefs.ThinkingLevel = arg
			if arg == "off" {
				prefs.ShowThinking = false
				resultMsg = "Thinking disabled."
			} else {
				prefs.ShowThinking = true
				resultMsg = fmt.Sprintf("Thinking level set to %s (output enabled).", arg)
			}
		} else {
			resultMsg = "Usage: /thinking [on|off|toggle|status|minimal|low|medium|high|xhigh]"
		}
	}

	_, _ = b.client.SendMessage(b.ctx, chatJID, &waE2E.Message{
		Conversation: proto.String(resultMsg),
	})
}

// processEvents consumes agent events and sends buffered responses
func (b *Bot) processEvents(evt *events.Message, evChan <-chan gateway.AgentEvent, prefs *ChatPreferences) {
	chatJID := evt.Info.Chat
	var response strings.Builder
	var thinkingBuf strings.Builder

	for event := range evChan {
		switch e := event.(type) {
		case gateway.EventTextDelta:
			response.WriteString(e.Delta)

		case gateway.EventThinkingDelta:
			if prefs.ShowThinking {
				thinkingBuf.WriteString(e.Delta)
			}

		case gateway.EventThinking:
			if prefs.ShowThinking && e.Content != "" {
				thinkingText := fmt.Sprintf("_thinking: %s_", truncate(e.Content, maxWhatsAppMessage-30))
				_, _ = b.client.SendMessage(b.ctx, chatJID, &waE2E.Message{
					Conversation: proto.String(thinkingText),
				})
			}

		case gateway.EventToolStart:
			_ = b.client.SendChatPresence(b.ctx, chatJID, types.ChatPresenceComposing, types.ChatPresenceMediaText)
			if prefs.ShowThinking {
				// Flush accumulated thinking before tools
				if thinkingBuf.Len() > 0 {
					thinkingText := fmt.Sprintf("_thinking: %s_", truncate(thinkingBuf.String(), maxWhatsAppMessage-30))
					_, _ = b.client.SendMessage(b.ctx, chatJID, &waE2E.Message{
						Conversation: proto.String(thinkingText),
					})
					thinkingBuf.Reset()
				}

				inputStr := string(e.Input)
				if len(inputStr) > 1024 {
					inputStr = inputStr[:1024] + "..."
				}
				toolMsg := fmt.Sprintf("*%s*\n```\n%s\n```", e.ToolName, inputStr)
				_, _ = b.client.SendMessage(b.ctx, chatJID, &waE2E.Message{
					Conversation: proto.String(toolMsg),
				})
			}

		case gateway.EventToolEnd:
			if prefs.ShowThinking {
				status := "done"
				duration := ""
				if e.DurationMs > 0 {
					duration = fmt.Sprintf(" (%dms)", e.DurationMs)
				}
				if e.Error != "" {
					status = "error"
				}
				result := e.Result
				if e.Error != "" {
					result = e.Error
				}
				if len(result) > 1024 {
					result = result[:1024] + "..."
				}
				toolMsg := fmt.Sprintf("_%s%s_", status, duration)
				if result != "" {
					toolMsg += fmt.Sprintf("\n```\n%s\n```", result)
				}
				_, _ = b.client.SendMessage(b.ctx, chatJID, &waE2E.Message{
					Conversation: proto.String(toolMsg),
				})
			}

		case gateway.EventAgentEnd:
			_ = b.client.SendChatPresence(b.ctx, chatJID, types.ChatPresencePaused, types.ChatPresenceMediaText)

			finalText := e.FinalText
			if finalText == "" {
				finalText = response.String()
			}
			if finalText == "" {
				finalText = "(No response)"
			}

			if media.ContainsMediaRefs(finalText) {
				b.sendWithMediaRefs(chatJID, finalText)
			} else {
				formatted := FormatMessage(finalText)
				chunks := splitMessage(formatted, maxWhatsAppMessage)
				for _, chunk := range chunks {
					_, _ = b.client.SendMessage(b.ctx, chatJID, &waE2E.Message{
						Conversation: proto.String(chunk),
					})
				}
			}

		case gateway.EventAgentError:
			_ = b.client.SendChatPresence(b.ctx, chatJID, types.ChatPresencePaused, types.ChatPresenceMediaText)
			L_error("whatsapp: agent error", "error", e.Error)
			_, _ = b.client.SendMessage(b.ctx, chatJID, &waE2E.Message{
				Conversation: proto.String(fmt.Sprintf("Error: %s", e.Error)),
			})
		}
	}
}

// downloadMedia downloads a whatsmeow media message, saves it, and returns a ContentBlock
func (b *Bot) downloadMedia(msg whatsmeow.DownloadableMessage, category, ext, mimeType string) (*itypes.ContentBlock, error) {
	data, err := b.client.Download(b.ctx, msg)
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}

	L_debug("whatsapp: media downloaded", "category", category, "size", len(data), "mime", mimeType)

	if b.gateway == nil || b.gateway.MediaStore() == nil {
		return nil, fmt.Errorf("no media store available")
	}

	absPath, _, err := b.gateway.MediaStore().Save(data, category, ext)
	if err != nil {
		return nil, fmt.Errorf("save failed: %w", err)
	}

	blockType := "audio"
	if strings.HasPrefix(mimeType, "image/") {
		blockType = "image"
	}

	return &itypes.ContentBlock{
		Type:     blockType,
		FilePath: absPath,
		MimeType: mimeType,
		Source:   "whatsapp",
	}, nil
}

// sendMediaFile uploads and sends a media file to a WhatsApp chat
func (b *Bot) sendMediaFile(jid types.JID, filePath, caption string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	mimeType, _ := media.DetectMimeType(filePath)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	mediaType := mimeToMediaType(mimeType)
	resp, err := b.client.Upload(b.ctx, data, mediaType)
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}

	msg := buildMediaMessage(mimeType, &resp, caption, uint64(len(data)))
	_, err = b.client.SendMessage(b.ctx, jid, msg)
	return err
}

// sendWithMediaRefs parses and sends text with inline media references
func (b *Bot) sendWithMediaRefs(jid types.JID, text string) {
	segments := media.SplitMediaSegments(text)

	var mediaRoot string
	if b.gateway != nil && b.gateway.MediaStore() != nil {
		mediaRoot = b.gateway.MediaStore().BaseDir()
	}

	for _, seg := range segments {
		if !seg.IsMedia {
			if seg.Text != "" {
				formatted := FormatMessage(seg.Text)
				_, _ = b.client.SendMessage(b.ctx, jid, &waE2E.Message{
					Conversation: proto.String(formatted),
				})
			}
			continue
		}

		if strings.HasPrefix(seg.Mime, "error/") {
			errType := strings.TrimPrefix(seg.Mime, "error/")
			errMsg := fmt.Sprintf("[Media %s: %s]", errType, seg.Path)
			_, _ = b.client.SendMessage(b.ctx, jid, &waE2E.Message{
				Conversation: proto.String(errMsg),
			})
			continue
		}

		absPath, err := media.ResolveMediaPath(mediaRoot, seg.Path)
		if err != nil {
			L_warn("whatsapp: failed to resolve media path", "path", seg.Path, "error", err)
			continue
		}

		if err := b.sendMediaFile(jid, absPath, ""); err != nil {
			L_warn("whatsapp: failed to send media", "path", absPath, "error", err)
		}
	}
}

// buildMediaMessage creates the proto message for a media upload
func buildMediaMessage(mimeType string, resp *whatsmeow.UploadResponse, caption string, fileLength uint64) *waE2E.Message {
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return &waE2E.Message{
			ImageMessage: &waE2E.ImageMessage{
				Caption:       proto.String(caption),
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &fileLength,
			},
		}
	case strings.HasPrefix(mimeType, "video/"):
		return &waE2E.Message{
			VideoMessage: &waE2E.VideoMessage{
				Caption:       proto.String(caption),
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &fileLength,
			},
		}
	case strings.HasPrefix(mimeType, "audio/"):
		return &waE2E.Message{
			AudioMessage: &waE2E.AudioMessage{
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &fileLength,
			},
		}
	default:
		return &waE2E.Message{
			DocumentMessage: &waE2E.DocumentMessage{
				Caption:       proto.String(caption),
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &fileLength,
			},
		}
	}
}

// mimeToMediaType maps a MIME type to whatsmeow's MediaType for upload
func mimeToMediaType(mimeType string) whatsmeow.MediaType {
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return whatsmeow.MediaImage
	case strings.HasPrefix(mimeType, "video/"):
		return whatsmeow.MediaVideo
	case strings.HasPrefix(mimeType, "audio/"):
		return whatsmeow.MediaAudio
	default:
		return whatsmeow.MediaDocument
	}
}

// mimeToExt returns a file extension for common MIME types
func mimeToExt(mimeType string) string {
	switch mimeType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "audio/ogg", "audio/ogg; codecs=opus":
		return ".ogg"
	default:
		return ".bin"
	}
}

// phoneToJID converts a phone number string to a WhatsApp JID
func phoneToJID(phone string) types.JID {
	return types.NewJID(phone, types.DefaultUserServer)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// splitMessage splits a message into chunks that fit the WhatsApp limit
func splitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	for len(text) > 0 {
		end := maxLen
		if end > len(text) {
			end = len(text)
		}
		// Try to split at a newline
		if end < len(text) {
			if idx := strings.LastIndex(text[:end], "\n"); idx > end/2 {
				end = idx + 1
			}
		}
		chunks = append(chunks, text[:end])
		text = text[end:]
	}
	return chunks
}
