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

	// SSE connections by user ID
	clients   map[string]*SSEClient
	clientsMu sync.RWMutex
}

// GatewayRunner is the interface for running agent requests
type GatewayRunner interface {
	RunAgent(ctx context.Context, req gateway.AgentRequest, events chan<- gateway.AgentEvent) error
}

// SSEClient represents a connected SSE client
type SSEClient struct {
	UserID string
	Events chan SSEEvent
	Done   chan struct{}
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

// SendMirror sends a mirrored conversation (for owner)
func (c *HTTPChannel) SendMirror(ctx context.Context, source, userMsg, response string) error {
	c.clientsMu.RLock()
	defer c.clientsMu.RUnlock()

	// Send mirror event to all connected clients (owner only uses HTTP for now)
	for _, client := range c.clients {
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
		}
	}
	return nil
}

// HasUser returns true if this channel can reach the given user
func (c *HTTPChannel) HasUser(u *user.User) bool {
	return u != nil && u.HasHTTPAuth()
}

// RegisterClient registers an SSE client
func (c *HTTPChannel) RegisterClient(userID string) *SSEClient {
	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()

	// Close existing client if any (new connection supersedes old)
	if existing, ok := c.clients[userID]; ok {
		close(existing.Done)
		L_debug("http: SSE client superseded", "user", userID)
	}

	client := &SSEClient{
		UserID: userID,
		Events: make(chan SSEEvent, 100), // Buffer for events
		Done:   make(chan struct{}),
	}
	c.clients[userID] = client

	L_debug("http: SSE client registered", "user", userID)
	return client
}

// UnregisterClient removes an SSE client (only if it's the same instance)
func (c *HTTPChannel) UnregisterClient(userID string, client *SSEClient) {
	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()

	// Only remove if this is still the registered client (not superseded by a new one)
	if current, ok := c.clients[userID]; ok && current == client {
		delete(c.clients, userID)
		L_debug("http: SSE client unregistered", "user", userID)
	}
}

// RunAgentRequest runs an agent request and streams events to the client
func (c *HTTPChannel) RunAgentRequest(ctx context.Context, u *user.User, message string) error {
	if c.gateway == nil {
		return fmt.Errorf("gateway not configured")
	}

	// Get the SSE client for this user
	c.clientsMu.RLock()
	client, ok := c.clients[u.ID]
	c.clientsMu.RUnlock()

	if !ok {
		return fmt.Errorf("no SSE client for user %s", u.ID)
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
				case <-client.Done:
					return
				case <-ctx.Done():
					return
				}
			}
		}
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
		return &SSEEvent{Event: "error", Data: map[string]string{
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
