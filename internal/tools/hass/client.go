package hass

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	hasspkg "github.com/roelfdiedericks/goclaw/internal/hass"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Client wraps the Home Assistant REST API.
// It handles authentication, TLS configuration, and returns raw JSON responses.
type Client struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewClient creates a new Home Assistant API client.
func NewClient(cfg hasspkg.HomeAssistantConfig) (*Client, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("home assistant URL not configured")
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("home assistant token not configured")
	}

	// Parse timeout
	timeout := 10 * time.Second
	if cfg.Timeout != "" {
		var err error
		timeout, err = time.ParseDuration(cfg.Timeout)
		if err != nil {
			L_warn("hass: invalid timeout, using default", "timeout", cfg.Timeout, "error", err)
			timeout = 10 * time.Second
		}
	}

	// Configure TLS
	transport := &http.Transport{}
	if cfg.Insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // G402: HASS instances may use private SSL certs
		L_debug("hass: TLS verification disabled (insecure mode)")
	}

	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}

	// Normalize base URL (remove trailing slash)
	baseURL := strings.TrimSuffix(cfg.URL, "/")

	L_debug("hass: client created", "url", baseURL, "timeout", timeout)

	return &Client{
		baseURL: baseURL,
		token:   cfg.Token,
		client:  client,
	}, nil
}

// Get performs a GET request and returns raw JSON response.
func (c *Client) Get(ctx context.Context, path string) (json.RawMessage, error) {
	url := c.baseURL + path
	L_debug("hass: GET request", "path", path)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, c.parseError(resp.StatusCode, body)
	}

	L_debug("hass: GET completed", "path", path, "status", resp.StatusCode, "bytes", len(body))
	return json.RawMessage(body), nil
}

// GetBinary performs a GET request and returns raw binary data (for camera).
func (c *Client) GetBinary(ctx context.Context, path string) ([]byte, string, error) {
	url := c.baseURL + path
	L_debug("hass: GET binary request", "path", path)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, "", c.parseError(resp.StatusCode, body)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read response: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")
	L_debug("hass: GET binary completed", "path", path, "status", resp.StatusCode, "bytes", len(body), "contentType", contentType)
	return body, contentType, nil
}

// Post performs a POST request and returns raw JSON response.
func (c *Client) Post(ctx context.Context, path string, body any) (json.RawMessage, error) {
	url := c.baseURL + path
	L_debug("hass: POST request", "path", path)

	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = strings.NewReader(string(jsonBody))
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, c.parseError(resp.StatusCode, respBody)
	}

	L_debug("hass: POST completed", "path", path, "status", resp.StatusCode, "bytes", len(respBody))
	return json.RawMessage(respBody), nil
}

// Error represents an error from the Home Assistant API.
type Error struct {
	StatusCode int
	Status     string
	Message    string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s: %s", e.Status, e.Message)
	}
	return e.Status
}

// parseError creates an Error from an HTTP error response.
func (c *Client) parseError(statusCode int, body []byte) error {
	status := http.StatusText(statusCode)
	if status == "" {
		status = fmt.Sprintf("%d", statusCode)
	} else {
		status = fmt.Sprintf("%d %s", statusCode, status)
	}

	// Try to parse error message from JSON
	var errResp struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Message != "" {
		return &Error{StatusCode: statusCode, Status: status, Message: errResp.Message}
	}

	// Use body as message if it's short enough
	if len(body) > 0 && len(body) < 200 {
		return &Error{StatusCode: statusCode, Status: status, Message: string(body)}
	}

	return &Error{StatusCode: statusCode, Status: status}
}

// IsAvailable checks if the Home Assistant API is reachable.
func (c *Client) IsAvailable(ctx context.Context) bool {
	_, err := c.Get(ctx, "/api/")
	return err == nil
}
