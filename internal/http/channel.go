package http

import (
	"context"
	"encoding/json"
	"fmt"
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
