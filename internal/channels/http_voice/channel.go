package http_voice

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/roelfdiedericks/goclaw/internal/channels/types"
	"github.com/roelfdiedericks/goclaw/internal/gateway"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/user"
	"github.com/roelfdiedericks/goclaw/internal/voicellm"
)

// Config is the configuration for the voice channel
type Config struct {
	Enabled bool `json:"enabled"`
}

// VoiceChannel implements the voice chat channel for real-time voice conversations
type VoiceChannel struct {
	gw     *gateway.Gateway
	config Config

	// Active voice sessions (keyed by user ID)
	sessions   map[string]*VoiceSession
	sessionsMu sync.RWMutex

	status types.ChannelStatus
	ctx    context.Context
	cancel context.CancelFunc
}

// VoiceSession represents an active voice conversation
type VoiceSession struct {
	UserID    string
	User      *user.User
	StartTime time.Time

	// WebSocket connection to browser
	wsConn   *websocket.Conn
	wsMu     sync.Mutex
	wsCancel context.CancelFunc

	// VoiceLLM provider connection
	provider    voicellm.Provider
	providerMu  sync.Mutex
	isConnected bool

	// Transcript accumulation
	assistantTranscript string
	transcriptMu        sync.Mutex
}

// NewVoiceChannel creates a new voice channel
func NewVoiceChannel(cfg Config) *VoiceChannel {
	return &VoiceChannel{
		config:   cfg,
		sessions: make(map[string]*VoiceSession),
		status: types.ChannelStatus{
			Running:   false,
			Connected: false,
		},
	}
}

// SetGateway sets the gateway for this channel
func (c *VoiceChannel) SetGateway(gw *gateway.Gateway) {
	c.gw = gw
}

// Name returns the channel identifier
func (c *VoiceChannel) Name() string {
	return "http_voice"
}

// Start begins processing (registers routes with HTTP server)
func (c *VoiceChannel) Start(ctx context.Context) error {
	c.ctx, c.cancel = context.WithCancel(ctx)
	c.status.Running = true
	c.status.Connected = true
	c.status.StartedAt = time.Now()
	c.status.Info = "voice"
	L_info("http_voice: channel started")
	return nil
}

// Stop gracefully shuts down the channel
func (c *VoiceChannel) Stop() error {
	if c.cancel != nil {
		c.cancel()
	}

	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()

	for userID, sess := range c.sessions {
		c.closeSessionLocked(sess)
		delete(c.sessions, userID)
	}

	c.status.Running = false
	c.status.Connected = false
	L_info("http_voice: channel stopped")
	return nil
}

// Reload applies new configuration
func (c *VoiceChannel) Reload(cfg any) error {
	newCfg, ok := cfg.(Config)
	if !ok {
		return fmt.Errorf("invalid config type: expected http_voice.Config")
	}
	c.config = newCfg
	L_info("http_voice: config reloaded", "enabled", newCfg.Enabled)
	return nil
}

// Status returns the current channel status
func (c *VoiceChannel) Status() types.ChannelStatus {
	c.sessionsMu.RLock()
	count := len(c.sessions)
	c.sessionsMu.RUnlock()

	status := c.status
	if count > 0 {
		status.Info = fmt.Sprintf("voice (%d sessions)", count)
	}
	return status
}

// Send sends a message (not used for voice - text goes via transcript)
func (c *VoiceChannel) Send(ctx context.Context, msg string) error {
	return nil
}

// SendMirror sends a mirrored user message to all owner voice sessions
func (c *VoiceChannel) SendMirror(ctx context.Context, source, userMsg string) error {
	c.sessionsMu.RLock()
	defer c.sessionsMu.RUnlock()

	msg := ServerMessage{
		Type:    MsgTypeMirror,
		Source:  source,
		UserMsg: userMsg,
	}

	for _, sess := range c.sessions {
		if sess.User != nil && sess.User.IsOwner() {
			sess.sendMessage(msg)
		}
	}
	return nil
}

// DeliverMessage sends agent output to the user's voice session
func (c *VoiceChannel) DeliverMessage(ctx context.Context, u *user.User, message string) error {
	if u == nil {
		return nil
	}
	c.sessionsMu.RLock()
	defer c.sessionsMu.RUnlock()

	sess, exists := c.sessions[u.ID]
	if !exists {
		return nil
	}

	msg := ServerMessage{
		Type:    MsgTypeAgentMessage,
		Message: message,
	}
	sess.sendMessage(msg)
	return nil
}

// HasUser returns true if this channel can reach the given user
func (c *VoiceChannel) HasUser(u *user.User) bool {
	if u == nil {
		return false
	}
	c.sessionsMu.RLock()
	defer c.sessionsMu.RUnlock()
	_, exists := c.sessions[u.ID]
	return exists
}

// StreamEvent streams agent events (not used for voice - has its own event model)
func (c *VoiceChannel) StreamEvent(u *user.User, event gateway.AgentEvent) bool {
	return false
}

// DeliverGhostwrite sends a ghostwritten message (not supported for voice MVP)
func (c *VoiceChannel) DeliverGhostwrite(ctx context.Context, u *user.User, message string) error {
	return nil
}

// GetSession returns a voice session for a user
func (c *VoiceChannel) GetSession(userID string) *VoiceSession {
	c.sessionsMu.RLock()
	defer c.sessionsMu.RUnlock()
	return c.sessions[userID]
}

// CreateSession creates a new voice session for a user
func (c *VoiceChannel) CreateSession(u *user.User, wsConn *websocket.Conn) (*VoiceSession, error) {
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()

	// Close existing session if any
	if existing := c.sessions[u.ID]; existing != nil {
		c.closeSessionLocked(existing)
	}

	ctx, cancel := context.WithCancel(c.ctx)
	sess := &VoiceSession{
		UserID:    u.ID,
		User:      u,
		StartTime: time.Now(),
		wsConn:    wsConn,
		wsCancel:  cancel,
	}

	c.sessions[u.ID] = sess
	L_info("http_voice: session created", "user", u.ID)

	// Start WebSocket read loop
	go c.runSession(ctx, sess)

	return sess, nil
}

// CloseSession closes a voice session for a user
func (c *VoiceChannel) CloseSession(userID string) {
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()

	if sess := c.sessions[userID]; sess != nil {
		c.closeSessionLocked(sess)
		delete(c.sessions, userID)
	}
}

// closeSessionLocked closes a session (must hold sessionsMu)
func (c *VoiceChannel) closeSessionLocked(sess *VoiceSession) {
	if sess.wsCancel != nil {
		sess.wsCancel()
	}

	sess.providerMu.Lock()
	if sess.provider != nil {
		sess.provider.Close()
		sess.provider = nil
	}
	sess.isConnected = false
	sess.providerMu.Unlock()

	sess.wsMu.Lock()
	if sess.wsConn != nil {
		sess.wsConn.Close()
		sess.wsConn = nil
	}
	sess.wsMu.Unlock()

	L_info("http_voice: session closed", "user", sess.UserID, "duration", time.Since(sess.StartTime))
}

// runSession handles the WebSocket connection for a voice session
func (c *VoiceChannel) runSession(ctx context.Context, sess *VoiceSession) {
	defer c.CloseSession(sess.UserID)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Read message from browser
		var msg ClientMessage
		sess.wsMu.Lock()
		if sess.wsConn == nil {
			sess.wsMu.Unlock()
			return
		}
		err := sess.wsConn.ReadJSON(&msg)
		sess.wsMu.Unlock()

		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				L_warn("http_voice: websocket read error", "user", sess.UserID, "error", err)
			}
			return
		}

		c.handleClientMessage(ctx, sess, msg)
	}
}

// handleClientMessage processes a message from the browser
func (c *VoiceChannel) handleClientMessage(ctx context.Context, sess *VoiceSession, msg ClientMessage) {
	switch msg.Type {
	case MsgTypeConnect:
		L_debug("http_voice: received connect message", "user", sess.UserID)
		c.handleConnect(ctx, sess)
	case MsgTypeDisconnect:
		L_debug("http_voice: received disconnect message", "user", sess.UserID)
		c.handleDisconnect(sess)
	case MsgTypeAudio:
		c.handleAudio(sess, msg.Audio)
	case MsgTypeCapabilities:
		c.handleCapabilities(sess, msg)
	default:
		L_warn("http_voice: unknown message type", "type", msg.Type, "user", sess.UserID)
	}
}

// handleCapabilities logs browser audio capabilities for debugging
func (c *VoiceChannel) handleCapabilities(sess *VoiceSession, msg ClientMessage) {
	L_info("http_voice: browser capabilities",
		"user", sess.UserID,
		"browserRate", msg.BrowserSampleRate,
		"outputRate", msg.OutputSampleRate,
		"userAgent", msg.UserAgent,
		"constraints", msg.AudioConstraints)
}

// handleConnect initiates a voice connection to the VoiceLLM provider
func (c *VoiceChannel) handleConnect(ctx context.Context, sess *VoiceSession) {
	sess.providerMu.Lock()
	defer sess.providerMu.Unlock()

	if sess.isConnected {
		sess.sendMessage(ServerMessage{Type: MsgTypeStatus, Status: "already connected"})
		return
	}

	// Get VoiceLLM registry
	registry := voicellm.GetRegistry()
	if registry == nil {
		sess.sendMessage(ServerMessage{Type: MsgTypeError, Error: "VoiceLLM not configured"})
		return
	}

	// Create provider session
	provider, err := registry.CreateSession("")
	if err != nil {
		L_error("http_voice: failed to create VoiceLLM session", "error", err, "user", sess.UserID)
		sess.sendMessage(ServerMessage{Type: MsgTypeError, Error: "Failed to connect: " + err.Error()})
		return
	}

	// Set up callbacks
	provider.SetCallbacks(voicellm.Callbacks{
		OnAudioDelta: func(audio []byte) {
			c.onProviderAudio(sess, audio)
		},
		OnTranscriptDelta: func(text string) {
			c.onProviderTranscriptDelta(sess, text)
		},
		OnInputTranscript: func(text string) {
			c.onProviderInputTranscript(ctx, sess, text)
		},
		OnToolCall: func(callID, name, args string) string {
			return c.onProviderToolCall(ctx, sess, name, args)
		},
		OnTurnComplete: func() {
			c.onProviderTurnComplete(ctx, sess)
		},
		OnError: func(err error) {
			c.onProviderError(sess, err)
		},
	})

	// Connect to provider
	if err := provider.Connect(ctx); err != nil {
		L_error("http_voice: provider connect failed", "error", err, "user", sess.UserID)
		sess.sendMessage(ServerMessage{Type: MsgTypeError, Error: "Connection failed: " + err.Error()})
		return
	}

	// Build system prompt
	voicePromptCfg := registry.GetPromptConfig()
	systemPrompt := c.gw.BuildSystemPromptForVoice(ctx, gateway.VoicePromptParams{
		User:                   sess.User,
		Source:                 "http_voice",
		Language:               voicePromptCfg.Language,
		MaxSentences:           voicePromptCfg.MaxSentences,
		Pronunciations:         voicePromptCfg.Pronunciations,
		AdditionalInstructions: voicePromptCfg.AdditionalInstructions,
	})

	// Get tool definitions for user
	toolDefs := c.gw.GetToolDefinitionsForUser(sess.User)
	L_debug("http_voice: tools available", "user", sess.UserID, "count", len(toolDefs))

	// Configure session
	if err := provider.Configure(voicellm.SessionConfig{
		Instructions: systemPrompt,
		ServerVAD:    registry.GetConfig().ServerVAD,
		Tools:        toolDefs,
	}); err != nil {
		L_error("http_voice: provider configure failed", "error", err, "user", sess.UserID)
		provider.Close()
		sess.sendMessage(ServerMessage{Type: MsgTypeError, Error: "Configuration failed: " + err.Error()})
		return
	}

	sess.provider = provider
	sess.isConnected = true
	sess.StartTime = time.Now()

	sess.sendMessage(ServerMessage{Type: MsgTypeConnected})
	L_info("http_voice: provider connected", "user", sess.UserID)
}

// handleDisconnect closes the voice connection
func (c *VoiceChannel) handleDisconnect(sess *VoiceSession) {
	sess.providerMu.Lock()
	defer sess.providerMu.Unlock()

	if sess.provider != nil {
		sess.provider.Close()
		sess.provider = nil
	}
	sess.isConnected = false

	sess.sendMessage(ServerMessage{Type: MsgTypeStatus, Status: "disconnected"})
	L_info("http_voice: provider disconnected", "user", sess.UserID, "duration", time.Since(sess.StartTime))
}

// handleAudio forwards audio from browser to provider
func (c *VoiceChannel) handleAudio(sess *VoiceSession, audioB64 string) {
	sess.providerMu.Lock()
	provider := sess.provider
	sess.providerMu.Unlock()

	if provider == nil {
		L_debug("http_voice: audio received but no provider", "user", sess.UserID)
		return
	}

	L_trace("http_voice: forwarding audio to provider", "user", sess.UserID, "b64len", len(audioB64))
	if err := provider.SendAudioBase64(audioB64); err != nil {
		L_warn("http_voice: failed to send audio", "error", err, "user", sess.UserID)
	}
}

// Provider callbacks

func (c *VoiceChannel) onProviderAudio(sess *VoiceSession, audio []byte) {
	sess.sendAudio(audio)
}

// onProviderTranscriptDelta handles streaming assistant transcript deltas
func (c *VoiceChannel) onProviderTranscriptDelta(sess *VoiceSession, text string) {
	sess.sendMessage(ServerMessage{
		Type:       MsgTypeTranscript,
		Transcript: text,
		Role:       "assistant",
		IsFinal:    false,
	})

	// Accumulate for final transcript
	sess.transcriptMu.Lock()
	sess.assistantTranscript += text
	sess.transcriptMu.Unlock()
}

// onProviderInputTranscript handles finalized user speech transcript
func (c *VoiceChannel) onProviderInputTranscript(ctx context.Context, sess *VoiceSession, text string) {
	sess.sendMessage(ServerMessage{
		Type:       MsgTypeTranscript,
		Transcript: text,
		Role:       "user",
		IsFinal:    true,
	})

	// Persist and broadcast user transcript
	if text != "" {
		// Log user transcript with green background (matches regular agent loop)
		const previewLen = 100
		preview := text
		if len(preview) > previewLen {
			preview = preview[:previewLen] + "..."
		}
		if len(preview) < previewLen {
			preview = preview + strings.Repeat(" ", previewLen-len(preview))
		}
		L_info("voice user", "msg", "\033[1;30;102m "+preview+" \033[0m")

		_, err := c.gw.PersistConversationTurn(ctx, gateway.PersistParams{
			User:        sess.User,
			Source:      "http_voice",
			UserMessage: text,
		})
		if err != nil {
			L_warn("http_voice: failed to persist user transcript", "error", err)
		}
		// Mirror user message to other channels (no response yet)
		c.gw.MirrorUserMessageToOthers(ctx, "http_voice", text, sess.User)
	}
}

// onProviderTurnComplete handles end of assistant response turn
func (c *VoiceChannel) onProviderTurnComplete(ctx context.Context, sess *VoiceSession) {
	// Get accumulated assistant transcript
	sess.transcriptMu.Lock()
	transcript := sess.assistantTranscript
	sess.assistantTranscript = ""
	sess.transcriptMu.Unlock()

	// Send final marker (empty transcript - deltas already sent the text)
	sess.sendMessage(ServerMessage{
		Type:    MsgTypeTranscript,
		Role:    "assistant",
		IsFinal: true,
	})

	// Persist and broadcast assistant transcript
	if transcript != "" {
		// Log agent transcript with blue background (matches regular agent loop)
		const previewLen = 100
		preview := transcript
		if len(preview) > previewLen {
			preview = preview[:previewLen] + "..."
		}
		if len(preview) < previewLen {
			preview = preview + strings.Repeat(" ", previewLen-len(preview))
		}
		L_info("voice agent", "msg", "\033[1;30;104m "+preview+" \033[0m")

		_, err := c.gw.PersistConversationTurn(ctx, gateway.PersistParams{
			User:             sess.User,
			Source:           "http_voice",
			AssistantMessage: transcript,
			SkipUserMessage:  true,
		})
		if err != nil {
			L_warn("http_voice: failed to persist assistant transcript", "error", err)
		}
		// Deliver agent response to other channels
		c.gw.DeliverAgentMessageToOthers(ctx, "http_voice", sess.User, transcript)
	}
}

func (c *VoiceChannel) onProviderToolCall(ctx context.Context, sess *VoiceSession, name, args string) string {
	L_info("http_voice: tool call", "tool", name, "user", sess.UserID)
	L_debug("http_voice: tool args", "tool", name, "args", args)

	// Execute tool via gateway
	result, err := c.gw.ExecuteTool(ctx, gateway.ToolExecutionParams{
		Name:   name,
		Input:  args,
		User:   sess.User,
		Source: "http_voice",
	})

	if err != nil {
		L_warn("http_voice: tool execution failed", "tool", name, "error", err, "user", sess.UserID)
		return fmt.Sprintf("Error executing %s: %s", name, err.Error())
	}

	resultText := result.GetText()
	L_debug("http_voice: tool result", "tool", name, "resultLen", len(resultText))
	return resultText
}

func (c *VoiceChannel) onProviderError(sess *VoiceSession, err error) {
	L_error("http_voice: provider error", "error", err, "user", sess.UserID)
	sess.sendMessage(ServerMessage{Type: MsgTypeError, Error: err.Error()})
}

// sendMessage sends a JSON message to the browser
func (s *VoiceSession) sendMessage(msg ServerMessage) {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()

	if s.wsConn == nil {
		return
	}

	if err := s.wsConn.WriteJSON(msg); err != nil {
		L_warn("http_voice: failed to send message", "type", msg.Type, "error", err)
	}
}

// sendAudio sends base64-encoded audio to the browser
func (s *VoiceSession) sendAudio(audio []byte) {
	s.sendMessage(ServerMessage{
		Type:  MsgTypeAudioDelta,
		Audio: base64.StdEncoding.EncodeToString(audio),
	})
}

// ConnectionDuration returns how long the voice connection has been active
func (s *VoiceSession) ConnectionDuration() time.Duration {
	return time.Since(s.StartTime)
}
