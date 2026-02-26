package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

const (
	oaiWSEndpoint   = "wss://api.openai.com/v1/responses"
	oaiWSWriteWait  = 30 * time.Second
	oaiWSPongWait   = 60 * time.Second
	oaiWSPingPeriod = 30 * time.Second
)

// oaiWSConn manages a persistent WebSocket connection to OpenAI's Responses API.
// Thread-safe: all public methods are mutex-protected.
type oaiWSConn struct {
	mu     sync.Mutex
	conn   *websocket.Conn
	apiKey string

	connected bool
	connTime  time.Time // when the current connection was established
}

// newOaiWSConn creates a new WebSocket connection manager.
// The actual connection is established lazily on first use.
func newOaiWSConn(apiKey string) *oaiWSConn {
	return &oaiWSConn{
		apiKey: apiKey,
	}
}

// ensureConnected establishes a WebSocket connection if not already connected.
func (ws *oaiWSConn) ensureConnected(ctx context.Context) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ws.connected && ws.conn != nil {
		return nil
	}

	return ws.connectLocked(ctx)
}

// connectLocked establishes a new WebSocket connection. Must be called with mu held.
func (ws *oaiWSConn) connectLocked(ctx context.Context) error {
	if ws.conn != nil {
		ws.conn.Close()
		ws.conn = nil
		ws.connected = false
	}

	header := http.Header{}
	header.Set("Authorization", "Bearer "+ws.apiKey)

	dialer := websocket.Dialer{
		HandshakeTimeout: 30 * time.Second,
	}

	L_debug("oai-next: connecting websocket", "endpoint", oaiWSEndpoint)

	conn, resp, err := dialer.DialContext(ctx, oaiWSEndpoint, header)
	if err != nil {
		if resp != nil {
			L_error("oai-next: websocket dial failed",
				"status", resp.StatusCode,
				"error", err,
			)
			resp.Body.Close()
			if resp.StatusCode == 401 || resp.StatusCode == 403 {
				return fmt.Errorf("authentication failed (HTTP %d): %w", resp.StatusCode, err)
			}
			return fmt.Errorf("websocket dial failed (HTTP %d): %w", resp.StatusCode, err)
		}
		L_error("oai-next: websocket dial failed", "error", err)
		return fmt.Errorf("websocket dial failed: %w", err)
	}

	ws.conn = conn
	ws.connected = true
	ws.connTime = time.Now()

	L_info("oai-next: websocket connected", "endpoint", oaiWSEndpoint)

	return nil
}

// reconnect drops the current connection and establishes a new one.
func (ws *oaiWSConn) reconnect(ctx context.Context) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	L_debug("oai-next: reconnecting websocket")
	return ws.connectLocked(ctx)
}

// sendRequest sends a response.create event over the WebSocket connection.
func (ws *oaiWSConn) sendRequest(ctx context.Context, req *oaiRequest) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if !ws.connected || ws.conn == nil {
		return fmt.Errorf("websocket not connected")
	}

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	if err := ws.conn.SetWriteDeadline(time.Now().Add(oaiWSWriteWait)); err != nil {
		return fmt.Errorf("failed to set write deadline: %w", err)
	}
	err = ws.conn.WriteMessage(websocket.TextMessage, data)
	if err != nil {
		ws.connected = false
		return fmt.Errorf("websocket write failed: %w", err)
	}

	L_trace("oai-next: sent request", "type", req.Type, "sizeBytes", len(data))

	return nil
}

// readEvent reads and parses the next event from the WebSocket.
// Blocks until an event is received or the context is cancelled.
// Returns the parsed event or an error.
func (ws *oaiWSConn) readEvent(ctx context.Context) (*oaiEvent, error) {
	if !ws.connected || ws.conn == nil {
		return nil, fmt.Errorf("websocket not connected")
	}

	// Use a goroutine + channel so we can respect context cancellation
	type result struct {
		event *oaiEvent
		err   error
	}
	ch := make(chan result, 1)

	go func() {
		_, data, err := ws.conn.ReadMessage()
		if err != nil {
			ch <- result{nil, err}
			return
		}

		var event oaiEvent
		if err := json.Unmarshal(data, &event); err != nil {
			ch <- result{nil, fmt.Errorf("failed to parse event: %w (raw: %s)", err, truncate(string(data), 200))}
			return
		}

		ch <- result{&event, nil}
	}()

	select {
	case <-ctx.Done():
		// Context cancelled — close the connection to unblock the reader goroutine
		ws.mu.Lock()
		if ws.conn != nil {
			ws.conn.Close()
		}
		ws.connected = false
		ws.mu.Unlock()
		return nil, ctx.Err()
	case r := <-ch:
		if r.err != nil {
			ws.mu.Lock()
			ws.connected = false
			ws.mu.Unlock()
		}
		return r.event, r.err
	}
}

// Close closes the WebSocket connection.
func (ws *oaiWSConn) Close() {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ws.conn != nil {
		ws.conn.Close()
		ws.conn = nil
	}
	ws.connected = false
	L_debug("oai-next: websocket closed")
}

// isConnected returns true if the connection is established.
func (ws *oaiWSConn) isConnected() bool {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return ws.connected && ws.conn != nil
}

// truncate shortens a string to maxLen, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
