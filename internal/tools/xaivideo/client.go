// Package xaivideo provides video generation using xAI's grok-imagine-video model.
package xaivideo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	defaultBaseURL = "https://api.x.ai/v1"
)

// videoClient wraps the xAI video generation REST API.
type videoClient struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

// newVideoClient creates a new xAI video API client.
func newVideoClient(apiKey string) *videoClient {
	return &videoClient{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// VideoRequest represents a video generation request.
type VideoRequest struct {
	Model       string `json:"model"`
	Prompt      string `json:"prompt"`
	Duration    int    `json:"duration,omitempty"`
	AspectRatio string `json:"aspect_ratio,omitempty"`
	Resolution  string `json:"resolution,omitempty"`
	ImageURL    string `json:"image_url,omitempty"`
	VideoURL    string `json:"video_url,omitempty"`
}

// StartResponse is returned when starting video generation.
type StartResponse struct {
	RequestID string `json:"request_id"`
}

// VideoResult contains the generated video details.
type VideoResult struct {
	URL               string `json:"url"`
	Duration          int    `json:"duration"`
	RespectModeration bool   `json:"respect_moderation"`
}

// StatusResponse is returned when polling for video status.
type StatusResponse struct {
	Status string       `json:"status"` // pending, done, expired
	Video  *VideoResult `json:"video,omitempty"`
	Model  string       `json:"model,omitempty"`
}

// APIError represents an error from the xAI API.
type APIError struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// Start initiates a video generation request.
// Returns the request_id for polling.
func (c *videoClient) Start(ctx context.Context, req VideoRequest) (string, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/videos/generations", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		var apiErr APIError
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Error.Message != "" {
			return "", fmt.Errorf("API error: %s", apiErr.Error.Message)
		}
		return "", fmt.Errorf("API error: %s", resp.Status)
	}

	var startResp StartResponse
	if err := json.Unmarshal(respBody, &startResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if startResp.RequestID == "" {
		return "", fmt.Errorf("no request_id in response")
	}

	return startResp.RequestID, nil
}

// GetStatus polls for the status of a video generation request.
func (c *videoClient) GetStatus(ctx context.Context, requestID string) (*StatusResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/videos/"+requestID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// 200 = done, 202 = still processing (pending)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		var apiErr APIError
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Error.Message != "" {
			return nil, fmt.Errorf("API error: %s", apiErr.Error.Message)
		}
		return nil, fmt.Errorf("API error: %s", resp.Status)
	}

	var status StatusResponse
	if err := json.Unmarshal(respBody, &status); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// If 202 and no explicit status, set to pending
	if resp.StatusCode == http.StatusAccepted && status.Status == "" {
		status.Status = "pending"
	}

	return &status, nil
}
