package http

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/roelfdiedericks/goclaw/internal/gateway"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// HTTPChannel implements the channel.Channel interface for HTTP/Web
type HTTPChannel struct {
	server  *Server
	gateway GatewayRunner

	// SSE connections by session ID (from cookie)
	clients   map[string]*SSEClient
	clientsMu sync.RWMutex
}

// GatewayRunner is the interface for running agent requests
type GatewayRunner interface {
	RunAgent(ctx context.Context, req gateway.AgentRequest, events chan<- gateway.AgentEvent) error
}

// SSEClient represents a connected SSE client
type SSEClient struct {
	SessionID string          // Unique session ID from cookie
	UserID    string          // User who owns this session
	User      *user.User      // Full user object
	Events    chan SSEEvent
	Done      chan struct{}
}

// SSEEvent represents an event to send via SSE
type SSEEvent struct {
	Event string      `json:"event"`
	Data  interface{} `json:"data"`
}

// NewHTTPChannel creates a new HTTP channel
func NewHTTPChannel(server *Server) *HTTPChannel {
	return &HTTPChannel{
		server:  server,
		clients: make(map[string]*SSEClient),
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
	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()

	for _, client := range c.clients {
		close(client.Done)
	}
	c.clients = make(map[string]*SSEClient)
	return nil
}

// Send sends a message (not used - HTTP responses go via SSE)
func (c *HTTPChannel) Send(ctx context.Context, msg string) error {
	return nil
}

// SendMirror sends a mirrored conversation to all owner sessions
func (c *HTTPChannel) SendMirror(ctx context.Context, source, userMsg, response string) error {
	c.clientsMu.RLock()
	defer c.clientsMu.RUnlock()

	// Send mirror event to all connected owner sessions
	for _, client := range c.clients {
		if client.User == nil || !client.User.IsOwner() {
			continue
		}
		select {
		case client.Events <- SSEEvent{
			Event: "mirror",
			Data: map[string]string{
				"source":   source,
				"userMsg":  userMsg,
				"response": response,
			},
		}:
		default:
			// Client buffer full, skip
			L_warn("http: mirror event dropped (buffer full)", "session", client.SessionID)
		}
	}
	return nil
}

// HasUser returns true if this channel can reach the given user
func (c *HTTPChannel) HasUser(u *user.User) bool {
	return u != nil && u.HasHTTPAuth()
}

// RegisterClient registers an SSE client by session ID
func (c *HTTPChannel) RegisterClient(sessionID string, u *user.User) *SSEClient {
	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()

	// Close existing client for this session if any (page refresh)
	if existing, ok := c.clients[sessionID]; ok {
		close(existing.Done)
		L_debug("http: SSE client replaced (same session)", "session", sessionID)
	}

	client := &SSEClient{
		SessionID: sessionID,
		UserID:    u.ID,
		User:      u,
		Events:    make(chan SSEEvent, 100), // Buffer for events
		Done:      make(chan struct{}),
	}
	c.clients[sessionID] = client

	L_debug("http: SSE client registered", "session", sessionID, "user", u.ID)
	return client
}

// UnregisterClient removes an SSE client
func (c *HTTPChannel) UnregisterClient(sessionID string, client *SSEClient) {
	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()

	// Only remove if this is still the registered client
	if current, ok := c.clients[sessionID]; ok && current == client {
		delete(c.clients, sessionID)
		L_debug("http: SSE client unregistered", "session", sessionID, "user", client.UserID)
	}
}

// GetClient returns the SSE client for a session
func (c *HTTPChannel) GetClient(sessionID string) *SSEClient {
	c.clientsMu.RLock()
	defer c.clientsMu.RUnlock()
	return c.clients[sessionID]
}

// RunAgentRequest runs an agent request and streams events to the client
func (c *HTTPChannel) RunAgentRequest(ctx context.Context, sessionID string, u *user.User, message string) error {
	if c.gateway == nil {
		return fmt.Errorf("gateway not configured")
	}

	// Get the SSE client for this session
	client := c.GetClient(sessionID)
	if client == nil {
		return fmt.Errorf("no SSE client for session %s", sessionID)
	}

	// Create agent request
	req := gateway.AgentRequest{
		User:    u,
		Source:  "http",
		UserMsg: message,
	}

	// Create events channel
	events := make(chan gateway.AgentEvent, 100)

	// Use background context - the POST request context will be canceled when it returns,
	// but we want the agent to keep running until done or SSE client disconnects
	agentCtx, agentCancel := context.WithCancel(context.Background())

	// Cancel agent if SSE client disconnects
	go func() {
		<-client.Done
		agentCancel()
	}()

	// Run agent in background
	go func() {
		defer agentCancel()
		err := c.gateway.RunAgent(agentCtx, req, events)
		if err != nil {
			L_error("http: agent run failed", "user", u.ID, "error", err)
		}
	}()

	// Stream events to SSE client
	go func() {
		for event := range events {
			sseEvent := c.convertEvent(event)
			if sseEvent != nil {
				select {
				case client.Events <- *sseEvent:
					L_debug("http: SSE event sent", "event", sseEvent.Event, "session", sessionID[:8]+"...")
				case <-client.Done:
					L_debug("http: SSE event dropped (client done)", "session", sessionID[:8]+"...")
					return
				case <-agentCtx.Done():
					L_debug("http: SSE event dropped (agent canceled)", "session", sessionID[:8]+"...")
					return
				}
			}
		}
		L_debug("http: SSE event stream ended", "session", sessionID[:8]+"...")
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
		return &SSEEvent{Event: "tool_use", Data: map[string]interface{}{
			"runId":    e.RunID,
			"toolName": e.ToolName,
			"toolId":   e.ToolID,
			"input":    json.RawMessage(e.Input),
		}}

	case gateway.EventToolEnd:
		return &SSEEvent{Event: "tool_result", Data: map[string]string{
			"runId":    e.RunID,
			"toolName": e.ToolName,
			"toolId":   e.ToolID,
			"result":   e.Result,
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
	a.channel.clientsMu.RLock()
	defer a.channel.clientsMu.RUnlock()

	sent := 0
	for _, client := range a.channel.clients {
		if client.User == nil || !client.User.IsOwner() {
			continue
		}
		select {
		case client.Events <- SSEEvent{
			Event: "agent_message",
			Data: map[string]string{
				"type": "text",
				"text": text,
			},
		}:
			sent++
		default:
			L_warn("http: message dropped (buffer full)", "session", client.SessionID)
		}
	}

	if sent == 0 {
		L_debug("http: no connected owner sessions for text message")
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

	a.channel.clientsMu.RLock()
	defer a.channel.clientsMu.RUnlock()

	sent := 0
	for _, client := range a.channel.clients {
		if client.User == nil || !client.User.IsOwner() {
			continue
		}
		select {
		case client.Events <- SSEEvent{
			Event: "agent_message",
			Data: map[string]interface{}{
				"type":     "media",
				"url":      mediaURL,
				"caption":  caption,
				"filename": filepath.Base(absPath),
			},
		}:
			sent++
		default:
			L_warn("http: media dropped (buffer full)", "session", client.SessionID)
		}
	}

	if sent == 0 {
		L_debug("http: no connected owner sessions for media", "path", absPath)
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
