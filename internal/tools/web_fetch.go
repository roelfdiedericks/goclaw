package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-shiori/go-readability"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// WebFetchTool fetches and extracts readable content from URLs
type WebFetchTool struct {
	client *http.Client
}

// NewWebFetchTool creates a new web fetch tool
func NewWebFetchTool() *WebFetchTool {
	return &WebFetchTool{
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (t *WebFetchTool) Name() string {
	return "web_fetch"
}

func (t *WebFetchTool) Description() string {
	return "Fetch a web page and extract its readable text content. Some sites with bot protection (Cloudflare) may block requests - use web_search as fallback."
}

func (t *WebFetchTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{
				"type":        "string",
				"description": "The URL to fetch",
			},
			"max_length": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum content length to return (default: 10000)",
			},
		},
		"required": []string{"url"},
	}
}

func (t *WebFetchTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params struct {
		URL       string `json:"url"`
		MaxLength int    `json:"max_length"`
	}

	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if params.URL == "" {
		return "", fmt.Errorf("url is required")
	}

	// Validate URL
	parsedURL, err := url.Parse(params.URL)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return "", fmt.Errorf("URL must use http or https scheme")
	}

	maxLen := params.MaxLength
	if maxLen <= 0 {
		maxLen = 10000
	}

	L_debug("web_fetch: fetching", "url", params.URL, "maxLength", maxLen)

	// Create request
	req, err := http.NewRequestWithContext(ctx, "GET", params.URL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Use a real browser User-Agent to avoid bot detection
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "identity") // Don't ask for compression, simplifies handling
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Sec-Ch-Ua", `"Chromium";v="122", "Not(A:Brand";v="24", "Google Chrome";v="122"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"Windows"`)
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	// Execute request
	resp, err := t.client.Do(req)
	if err != nil {
		L_error("web_fetch: request failed", "error", err, "url", params.URL)
		return "", fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		L_warn("web_fetch: non-200 status", "status", resp.StatusCode, "url", params.URL)
		return "", fmt.Errorf("HTTP error: %s", resp.Status)
	}

	// Check content type
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/html") && !strings.Contains(contentType, "application/xhtml") {
		// For non-HTML content, just return the raw text (truncated)
		body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxLen)))
		if err != nil {
			return "", fmt.Errorf("failed to read response: %w", err)
		}
		L_debug("web_fetch: non-HTML content", "contentType", contentType, "length", len(body))
		return string(body), nil
	}

	// Parse with readability
	article, err := readability.FromReader(resp.Body, parsedURL)
	if err != nil {
		L_error("web_fetch: readability parse failed", "error", err, "url", params.URL)
		return "", fmt.Errorf("failed to parse page: %w", err)
	}

	// Build result
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Title: %s\n", article.Title))
	if article.Byline != "" {
		result.WriteString(fmt.Sprintf("Author: %s\n", article.Byline))
	}
	if article.SiteName != "" {
		result.WriteString(fmt.Sprintf("Site: %s\n", article.SiteName))
	}
	result.WriteString(fmt.Sprintf("URL: %s\n\n", params.URL))
	result.WriteString("---\n\n")
	result.WriteString(article.TextContent)

	content := result.String()

	// Truncate if needed
	if len(content) > maxLen {
		content = content[:maxLen] + "\n\n[Content truncated...]"
	}

	L_debug("web_fetch: completed", "url", params.URL, "contentLength", len(content), "title", article.Title)
	return content, nil
}
