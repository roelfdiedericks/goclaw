package http

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/gateway"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/session"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// HTTPChannel implements the channel.Channel interface for HTTP/Web
type HTTPChannel struct {
	server  *Server
	gateway GatewayRunner

	// Sessions persist across SSE reconnects (keyed by cookie session ID)
	sessions   map[string]*SSESession
	sessionsMu sync.RWMutex
}

// GatewayRunner is the interface for running agent requests
type GatewayRunner interface {
	RunAgent(ctx context.Context, req gateway.AgentRequest, events chan<- gateway.AgentEvent) error
	AgentIdentity() *config.AgentIdentityConfig
	SupervisionConfig() *config.SupervisionConfig
}

const maxEventBuffer = 200 // Keep last N events per session for replay

// SSESession persists across SSE reconnects - tied to cookie session ID
type SSESession struct {
	SessionID string
	UserID    string
	User      *user.User

	// Event buffer for replay on reconnect
	eventBuffer []BufferedEvent
	nextEventID int
	bufferMu    sync.Mutex

	// Active connection (nil if disconnected)
	activeConn *SSEConnection
	connMu     sync.Mutex

	// Preferences
	ShowThinking bool // Show tool calls and thinking output (default: false)
}

// SSEConnection represents an active SSE connection
type SSEConnection struct {
	Events chan SSEEvent
	Done   chan struct{}
}

// BufferedEvent stores an event with its ID for replay
type BufferedEvent struct {
	ID    int
	Event SSEEvent
}

// SSEEvent represents an event to send via SSE
type SSEEvent struct {
	Event string      `json:"event"`
	Data  interface{} `json:"data"`
}

// NewHTTPChannel creates a new HTTP channel
func NewHTTPChannel(server *Server) *HTTPChannel {
	return &HTTPChannel{
		server:   server,
		sessions: make(map[string]*SSESession),
	}
}

// SetGateway sets the gateway for running agent requests
func (c *HTTPChannel) SetGateway(gw GatewayRunner) {
	c.gateway = gw
}

// Name returns the channel identifier
func (c *HTTPChannel) Name() string {
	return "http"
}

// Start begins processing messages (no-op for HTTP - server handles this)
func (c *HTTPChannel) Start(ctx context.Context) error {
	return nil
}

// Stop gracefully shuts down the channel
func (c *HTTPChannel) Stop() error {
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()

	for _, sess := range c.sessions {
		sess.connMu.Lock()
		if sess.activeConn != nil {
			close(sess.activeConn.Done)
		}
		sess.connMu.Unlock()
	}
	c.sessions = make(map[string]*SSESession)
	return nil
}

// Send sends a message (not used - HTTP responses go via SSE)
func (c *HTTPChannel) Send(ctx context.Context, msg string) error {
	return nil
}

// SendMirror sends a mirrored conversation to all owner sessions
func (c *HTTPChannel) SendMirror(ctx context.Context, source, userMsg, response string) error {
	c.sessionsMu.RLock()
	defer c.sessionsMu.RUnlock()

	event := SSEEvent{
		Event: "mirror",
		Data: map[string]string{
			"source":   source,
			"userMsg":  userMsg,
			"response": response,
		},
	}

	// Send mirror event to all connected owner sessions
	for _, sess := range c.sessions {
		if sess.User == nil || !sess.User.IsOwner() {
			continue
		}
		sess.SendEvent(event)
	}
	return nil
}

// HasUser returns true if this channel can reach the given user
func (c *HTTPChannel) HasUser(u *user.User) bool {
	return u != nil && u.HasHTTPAuth()
}

// getSessionsForUser returns all SSE sessions for a given user
func (c *HTTPChannel) getSessionsForUser(u *user.User) []*SSESession {
	if u == nil {
		return nil
	}

	c.sessionsMu.RLock()
	defer c.sessionsMu.RUnlock()

	var sessions []*SSESession
	for _, sess := range c.sessions {
		if sess.User != nil && sess.User.ID == u.ID {
			sessions = append(sessions, sess)
		}
	}
	return sessions
}

// InjectMessage handles message injection for supervision (guidance/ghostwriting).
//
// If invokeLLM is true (guidance):
//   - Triggers agent run, streams events through normal SSE path
//   - User sees typing indicator, streaming text, tool calls, final response
//
// If invokeLLM is false (ghostwrite):
//   - Delivers message directly with typing delay
//   - No LLM invocation
func (c *HTTPChannel) InjectMessage(ctx context.Context, u *user.User, sessionKey, message string, invokeLLM bool) error {
	if c.gateway == nil {
		return fmt.Errorf("gateway not configured")
	}
	if u == nil {
		return nil
	}

	// Find all SSE sessions for this user
	sessions := c.getSessionsForUser(u)
	if len(sessions) == 0 {
		L_debug("http: inject: no active sessions for user", "user", u.ID, "invokeLLM", invokeLLM)
		return nil // No active sessions, nothing to deliver
	}

	L_info("http: inject message", "user", u.ID, "sessionKey", sessionKey, "invokeLLM", invokeLLM, "sessions", len(sessions), "messageLen", len(message))

	if invokeLLM {
		// Guidance: run agent and stream events to user's sessions
		// The message is already in the session context (added by gateway)
		events := make(chan gateway.AgentEvent, 100)

		// Create agent request - no UserMsg since it's already in session
		req := gateway.AgentRequest{
			User:           u,
			Source:         "http",
			SessionID:      sessionKey,      // Explicit session key
			SkipAddMessage: true,            // Message already added by gateway.InjectMessage
			EnableThinking: u.Thinking,      // Extended thinking based on user preference
			// UserMsg intentionally empty - message already in session
		}

		// Use background context so agent keeps running after this returns
		agentCtx, agentCancel := context.WithCancel(context.Background())

		// Run agent in background
		go func() {
			defer agentCancel()
			err := c.gateway.RunAgent(agentCtx, req, events)
			if err != nil {
				L_error("http: inject agent run failed", "user", u.ID, "error", err)
			}
		}()

		// Stream events to all user's sessions
		go func() {
			for event := range events {
				sseEvent := c.convertEvent(event)
				if sseEvent != nil {
					for _, sess := range sessions {
						sess.SendEvent(*sseEvent)
					}
				}
			}
			L_debug("http: inject event stream ended", "user", u.ID)
		}()

	} else {
		// Ghostwrite: deliver message directly with typing simulation
		runID := fmt.Sprintf("run_%d", time.Now().UnixNano())

		// Get typing delay from config
		typingDelay := 500 * time.Millisecond // default
		if cfg := c.gateway.SupervisionConfig(); cfg != nil && cfg.Ghostwriting.TypingDelayMs > 0 {
			typingDelay = time.Duration(cfg.Ghostwriting.TypingDelayMs) * time.Millisecond
		}

		// Send start event (typing indicator)
		startEvent := SSEEvent{
			Event: "start",
			Data: gateway.EventAgentStart{
				RunID:      runID,
				Source:     "http",
				SessionKey: sessionKey,
			},
		}
		for _, sess := range sessions {
			sess.SendEvent(startEvent)
		}

		// Wait for typing delay (simulates thinking/typing)
		time.Sleep(typingDelay)

		// Send done event with message
		doneEvent := SSEEvent{
			Event: "done",
			Data: gateway.EventAgentEnd{
				RunID:     runID,
				FinalText: message,
			},
		}
		for _, sess := range sessions {
			sess.SendEvent(doneEvent)
		}

		L_info("http: inject ghostwrite delivered", "user", u.ID, "sessions", len(sessions), "messageLen", len(message))
	}

	return nil
}

// getOrCreateSession gets existing session or creates new one
func (c *HTTPChannel) getOrCreateSession(sessionID string, u *user.User) *SSESession {
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()

	if sess, ok := c.sessions[sessionID]; ok {
		// Update user in case it changed
		sess.User = u
		sess.UserID = u.ID
		return sess
	}

	sess := &SSESession{
		SessionID:    sessionID,
		UserID:       u.ID,
		User:         u,
		eventBuffer:  make([]BufferedEvent, 0, maxEventBuffer),
		nextEventID:  1,
		ShowThinking: u.Thinking, // Initialize from user preference
	}
	c.sessions[sessionID] = sess
	L_debug("http: session created", "session", sessionID, "user", u.ID, "showThinking", u.Thinking)
	return sess
}

// GetSession returns the session for a session ID
func (c *HTTPChannel) GetSession(sessionID string) *SSESession {
	c.sessionsMu.RLock()
	defer c.sessionsMu.RUnlock()
	return c.sessions[sessionID]
}

// RegisterConnection registers a new SSE connection for a session
// Returns the connection and events to replay (if any)
func (c *HTTPChannel) RegisterConnection(sessionID string, u *user.User, lastEventID int) (*SSESession, *SSEConnection, []BufferedEvent) {
	sess := c.getOrCreateSession(sessionID, u)

	sess.connMu.Lock()
	defer sess.connMu.Unlock()

	// Close existing connection if any (page refresh/reconnect)
	if sess.activeConn != nil {
		close(sess.activeConn.Done)
		L_debug("http: SSE connection replaced", "session", sessionID)
	}

	conn := &SSEConnection{
		Events: make(chan SSEEvent, 100),
		Done:   make(chan struct{}),
	}
	sess.activeConn = conn

	// Get events to replay
	var replay []BufferedEvent
	if lastEventID > 0 {
		replay = sess.GetEventsSince(lastEventID)
		L_debug("http: SSE replay events", "session", sessionID, "lastEventID", lastEventID, "replayCount", len(replay))
	}

	L_debug("http: SSE connection registered", "session", sessionID, "user", u.ID)
	return sess, conn, replay
}

// UnregisterConnection removes an SSE connection (but keeps session)
func (c *HTTPChannel) UnregisterConnection(sessionID string, conn *SSEConnection) {
	sess := c.GetSession(sessionID)
	if sess == nil {
		return
	}

	sess.connMu.Lock()
	defer sess.connMu.Unlock()

	// Only clear if this is still the active connection
	if sess.activeConn == conn {
		sess.activeConn = nil
		L_debug("http: SSE connection unregistered", "session", sessionID)
	}
}

// SendEvent sends an event to the session (buffers it and sends to active connection)
func (s *SSESession) SendEvent(event SSEEvent) {
	s.bufferMu.Lock()
	eventID := s.nextEventID
	s.nextEventID++

	// Add to buffer
	buffered := BufferedEvent{ID: eventID, Event: event}
	s.eventBuffer = append(s.eventBuffer, buffered)

	// Trim buffer if too large
	if len(s.eventBuffer) > maxEventBuffer {
		s.eventBuffer = s.eventBuffer[len(s.eventBuffer)-maxEventBuffer:]
	}
	s.bufferMu.Unlock()

	// Send to active connection if any
	s.connMu.Lock()
	conn := s.activeConn
	s.connMu.Unlock()

	if conn != nil {
		select {
		case conn.Events <- event:
		default:
			L_warn("http: event dropped (buffer full)", "session", s.SessionID, "eventID", eventID)
		}
	}
}

// GetEventsSince returns buffered events since the given ID
func (s *SSESession) GetEventsSince(lastEventID int) []BufferedEvent {
	s.bufferMu.Lock()
	defer s.bufferMu.Unlock()

	var result []BufferedEvent
	for _, ev := range s.eventBuffer {
		if ev.ID > lastEventID {
			result = append(result, ev)
		}
	}
	return result
}

// GetActiveConnection returns the active connection (for compatibility)
func (s *SSESession) GetActiveConnection() *SSEConnection {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	return s.activeConn
}

// SessionInfo contains information about an active session for display
type SessionInfo struct {
	SessionID   string `json:"sessionId"`
	UserID      string `json:"userId"`
	UserName    string `json:"userName"`
	IsOwner     bool   `json:"isOwner"`
	IsConnected bool   `json:"isConnected"`
	EventCount  int    `json:"eventCount"`
}

// GetSessionsInfo returns info about all active sessions
func (c *HTTPChannel) GetSessionsInfo() []SessionInfo {
	c.sessionsMu.RLock()
	defer c.sessionsMu.RUnlock()

	var sessions []SessionInfo
	for _, sess := range c.sessions {
		sess.connMu.Lock()
		isConnected := sess.activeConn != nil
		sess.connMu.Unlock()

		sess.bufferMu.Lock()
		eventCount := sess.nextEventID - 1
		sess.bufferMu.Unlock()

		name := sess.UserID
		if sess.User != nil && sess.User.Name != "" {
			name = sess.User.Name
		}

		sessions = append(sessions, SessionInfo{
			SessionID:   sess.SessionID[:8] + "...",
			UserID:      sess.UserID,
			UserName:    name,
			IsOwner:     sess.User != nil && sess.User.IsOwner(),
			IsConnected: isConnected,
			EventCount:  eventCount,
		})
	}
	return sessions
}

// RunAgentRequest runs an agent request and streams events to the session
func (c *HTTPChannel) RunAgentRequest(ctx context.Context, sessionID string, u *user.User, message string, images []session.ImageAttachment) error {
	if c.gateway == nil {
		return fmt.Errorf("gateway not configured")
	}

	// Get the session (must exist - created during SSE connect)
	sess := c.GetSession(sessionID)
	if sess == nil {
		return fmt.Errorf("no session for %s", sessionID)
	}

	// Create agent request
	req := gateway.AgentRequest{
		User:           u,
		Source:         "http",
		UserMsg:        message,
		Images:         images,
		EnableThinking: u.Thinking, // Extended thinking based on user preference
	}

	// Create events channel
	events := make(chan gateway.AgentEvent, 100)

	// Use background context - the POST request context will be canceled when it returns,
	// but we want the agent to keep running until done
	agentCtx, agentCancel := context.WithCancel(context.Background())

	// Run agent in background
	go func() {
		defer agentCancel()
		err := c.gateway.RunAgent(agentCtx, req, events)
		if err != nil {
			L_error("http: agent run failed", "user", u.ID, "error", err)
		}
	}()

	// Stream events to session (buffers for replay on reconnect)
	go func() {
		for event := range events {
			sseEvent := c.convertEvent(event)
			if sseEvent != nil {
				sess.SendEvent(*sseEvent)
			}
		}
		L_debug("http: event stream ended", "session", sessionID[:8]+"...")
	}()

	return nil
}

// convertEvent converts a gateway event to an SSE event
func (c *HTTPChannel) convertEvent(event gateway.AgentEvent) *SSEEvent {
	switch e := event.(type) {
	case gateway.EventAgentStart:
		return &SSEEvent{Event: "start", Data: e}

	case gateway.EventTextDelta:
		return &SSEEvent{Event: "message", Data: map[string]string{
			"runId":   e.RunID,
			"content": e.Delta,
		}}

	case gateway.EventToolStart:
		// Truncate input for display (1024 chars max)
		inputStr := string(e.Input)
		if len(inputStr) > 1024 {
			inputStr = inputStr[:1024] + "..."
		}
		return &SSEEvent{Event: "tool_start", Data: map[string]interface{}{
			"runId":    e.RunID,
			"toolName": e.ToolName,
			"toolId":   e.ToolID,
			"input":    inputStr,
		}}

	case gateway.EventToolEnd:
		// Truncate result for display (1024 chars max)
		result := e.Result
		if len(result) > 1024 {
			result = result[:1024] + "..."
		}
		return &SSEEvent{Event: "tool_end", Data: map[string]interface{}{
			"runId":      e.RunID,
			"toolName":   e.ToolName,
			"toolId":     e.ToolID,
			"result":     result,
			"error":      e.Error,
			"durationMs": e.DurationMs,
		}}

	case gateway.EventAgentEnd:
		return &SSEEvent{Event: "done", Data: map[string]string{
			"runId":     e.RunID,
			"finalText": e.FinalText,
		}}

	case gateway.EventAgentError:
		return &SSEEvent{Event: "agent_error", Data: map[string]string{
			"runId": e.RunID,
			"error": e.Error,
		}}

	case gateway.EventThinking:
		return &SSEEvent{Event: "thinking", Data: map[string]string{
			"runId":   e.RunID,
			"content": e.Content,
		}}

	default:
		return nil
	}
}

// MessageChannelAdapter implements the tools.MessageChannel interface for HTTP.
// It sends messages/media to all connected owner sessions via SSE.
type MessageChannelAdapter struct {
	channel   *HTTPChannel
	mediaBase string // Base URL path for media (e.g., "/api/media")
}

// NewMessageChannelAdapter creates a new HTTP message channel adapter.
func NewMessageChannelAdapter(channel *HTTPChannel, mediaBase string) *MessageChannelAdapter {
	return &MessageChannelAdapter{
		channel:   channel,
		mediaBase: mediaBase,
	}
}

// SendText sends a text message to all connected owner sessions.
func (a *MessageChannelAdapter) SendText(chatID string, text string) (string, error) {
	a.channel.sessionsMu.RLock()
	defer a.channel.sessionsMu.RUnlock()

	event := SSEEvent{
		Event: "agent_message",
		Data: map[string]string{
			"type": "text",
			"text": text,
		},
	}

	sent := 0
	for _, sess := range a.channel.sessions {
		if sess.User == nil || !sess.User.IsOwner() {
			continue
		}
		sess.SendEvent(event)
		sent++
	}

	if sent == 0 {
		L_debug("http: no owner sessions for text message")
		return "http-0 (no sessions)", nil // Don't fail - best effort delivery
	}

	L_debug("http: sent text message", "sessions", sent)
	return fmt.Sprintf("http-%d", sent), nil
}

// SendMedia sends a media file to all connected owner sessions.
// The file is served via /api/media and the URL is sent via SSE.
func (a *MessageChannelAdapter) SendMedia(chatID string, filePath string, caption string) (string, error) {
	// Verify file exists
	absPath := filePath
	if !filepath.IsAbs(filePath) {
		// Resolve relative paths
		var err error
		absPath, err = filepath.Abs(filePath)
		if err != nil {
			return "", fmt.Errorf("invalid path: %w", err)
		}
	}

	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return "", fmt.Errorf("file not found: %s", filePath)
	}

	// Generate media URL - the server will serve files from allowed paths
	// We pass the absolute path as a query param (server validates it)
	mediaURL := fmt.Sprintf("%s?path=%s", a.mediaBase, absPath)

	event := SSEEvent{
		Event: "agent_message",
		Data: map[string]interface{}{
			"type":     "media",
			"url":      mediaURL,
			"caption":  caption,
			"filename": filepath.Base(absPath),
		},
	}

	a.channel.sessionsMu.RLock()
	defer a.channel.sessionsMu.RUnlock()

	sent := 0
	for _, sess := range a.channel.sessions {
		if sess.User == nil || !sess.User.IsOwner() {
			continue
		}
		sess.SendEvent(event)
		sent++
	}

	if sent == 0 {
		L_debug("http: no owner sessions for media", "path", absPath)
		return "http-0 (no sessions)", nil // Don't fail - best effort delivery
	}

	L_debug("http: sent media", "path", absPath, "sessions", sent)
	return fmt.Sprintf("http-%d", sent), nil
}

// EditMessage is not supported for HTTP (messages are ephemeral SSE events).
func (a *MessageChannelAdapter) EditMessage(chatID string, messageID string, text string) error {
	return fmt.Errorf("edit not supported for HTTP channel (SSE messages are ephemeral)")
}

// DeleteMessage is not supported for HTTP (messages are ephemeral SSE events).
func (a *MessageChannelAdapter) DeleteMessage(chatID string, messageID string) error {
	return fmt.Errorf("delete not supported for HTTP channel (SSE messages are ephemeral)")
}

// React is not supported for HTTP channel.
func (a *MessageChannelAdapter) React(chatID string, messageID string, emoji string) error {
	return fmt.Errorf("reactions not supported for HTTP channel")
}
