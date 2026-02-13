package hass

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/roelfdiedericks/goclaw/internal/config"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// WSClient performs one-shot WebSocket queries to Home Assistant.
// Each query opens a fresh connection, authenticates, sends the command,
// waits for the response, and closes. This avoids interference with
// the persistent subscription WebSocket.
type WSClient struct {
	cfg config.HomeAssistantConfig
}

// NewWSClient creates a new WebSocket client for sync registry queries.
func NewWSClient(cfg config.HomeAssistantConfig) *WSClient {
	return &WSClient{cfg: cfg}
}

// ListDevices retrieves all devices from the device registry.
func (c *WSClient) ListDevices(ctx context.Context) (json.RawMessage, error) {
	return c.query(ctx, "config/device_registry/list")
}

// ListAreas retrieves all areas from the area registry.
func (c *WSClient) ListAreas(ctx context.Context) (json.RawMessage, error) {
	return c.query(ctx, "config/area_registry/list")
}

// ListEntities retrieves all entities from the entity registry.
func (c *WSClient) ListEntities(ctx context.Context) (json.RawMessage, error) {
	return c.query(ctx, "config/entity_registry/list")
}

// query performs a single WebSocket command and returns the result.
func (c *WSClient) query(ctx context.Context, cmdType string) (json.RawMessage, error) {
	// Build WebSocket URL from REST URL
	wsURL := c.buildWebSocketURL()
	L_debug("hass: ws query starting", "type", cmdType, "url", wsURL)

	// Parse timeout
	timeout := 10 * time.Second
	if c.cfg.Timeout != "" {
		if d, err := time.ParseDuration(c.cfg.Timeout); err == nil {
			timeout = d
		}
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Configure dialer
	dialer := websocket.Dialer{
		HandshakeTimeout: timeout,
	}
	if c.cfg.Insecure {
		dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // G402: HASS instances may use private SSL certs
	}

	// Connect
	//nolint:bodyclose // WebSocket upgrade - response body handled by gorilla/websocket
	conn, _, err := dialer.DialContext(ctx, wsURL, http.Header{})
	if err != nil {
		L_error("hass: ws connect failed", "error", err)
		return nil, fmt.Errorf("websocket connect: %w", err)
	}
	defer conn.Close()

	// Read auth_required message
	var authReq HAMessage
	if err := conn.ReadJSON(&authReq); err != nil {
		return nil, fmt.Errorf("read auth_required: %w", err)
	}
	if authReq.Type != "auth_required" {
		return nil, fmt.Errorf("unexpected message type: %s (expected auth_required)", authReq.Type)
	}

	// Send auth
	authMsg := HAAuthMessage{
		Type:        "auth",
		AccessToken: c.cfg.Token,
	}
	if err := conn.WriteJSON(authMsg); err != nil {
		return nil, fmt.Errorf("send auth: %w", err)
	}

	// Read auth result
	var authResult HAMessage
	if err := conn.ReadJSON(&authResult); err != nil {
		return nil, fmt.Errorf("read auth result: %w", err)
	}
	if authResult.Type != "auth_ok" {
		if authResult.Type == "auth_invalid" {
			return nil, fmt.Errorf("authentication failed: invalid token")
		}
		return nil, fmt.Errorf("auth failed: %s", authResult.Type)
	}

	// Send command
	cmd := HACommandMessage{
		ID:   1,
		Type: cmdType,
	}
	if err := conn.WriteJSON(cmd); err != nil {
		return nil, fmt.Errorf("send command: %w", err)
	}

	// Read result
	var result HAMessage
	if err := conn.ReadJSON(&result); err != nil {
		return nil, fmt.Errorf("read result: %w", err)
	}

	// Check for error
	if result.Success != nil && !*result.Success {
		errMsg := "unknown error"
		if result.Error != nil {
			errMsg = result.Error.Message
		}
		return nil, fmt.Errorf("command failed: %s", errMsg)
	}

	L_debug("hass: ws query completed", "type", cmdType, "resultSize", len(result.Result))
	return result.Result, nil
}

// buildWebSocketURL converts the REST URL to a WebSocket URL.
// https://example.com:8123 -> wss://example.com:8123/api/websocket
// http://example.com:8123 -> ws://example.com:8123/api/websocket
func (c *WSClient) buildWebSocketURL() string {
	url := c.cfg.URL
	url = strings.TrimSuffix(url, "/")

	// Convert scheme
	if strings.HasPrefix(url, "https://") {
		url = "wss://" + strings.TrimPrefix(url, "https://")
	} else if strings.HasPrefix(url, "http://") {
		url = "ws://" + strings.TrimPrefix(url, "http://")
	}

	return url + "/api/websocket"
}
