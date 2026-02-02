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

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// WebSearchTool searches the web using Brave Search API
type WebSearchTool struct {
	apiKey string
	client *http.Client
}

// NewWebSearchTool creates a new web search tool
func NewWebSearchTool(apiKey string) *WebSearchTool {
	return &WebSearchTool{
		apiKey: apiKey,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (t *WebSearchTool) Name() string {
	return "web_search"
}

func (t *WebSearchTool) Description() string {
	return "Search the web for information. Returns titles, URLs, and snippets from search results."
}

func (t *WebSearchTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "The search query",
			},
			"count": map[string]interface{}{
				"type":        "integer",
				"description": "Number of results to return (default: 5, max: 20)",
			},
		},
		"required": []string{"query"},
	}
}

func (t *WebSearchTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Query string `json:"query"`
		Count int    `json:"count"`
	}

	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if params.Query == "" {
		return "", fmt.Errorf("query is required")
	}

	if t.apiKey == "" {
		return "", fmt.Errorf("Brave API key not configured")
	}

	count := params.Count
	if count <= 0 {
		count = 5
	}
	if count > 20 {
		count = 20
	}

	L_debug("web_search: executing", "query", params.Query, "count", count)

	// Build request URL
	baseURL := "https://api.search.brave.com/res/v1/web/search"
	reqURL, _ := url.Parse(baseURL)
	q := reqURL.Query()
	q.Set("q", params.Query)
	q.Set("count", fmt.Sprintf("%d", count))
	reqURL.RawQuery = q.Encode()

	// Create request
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL.String(), nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("X-Subscription-Token", t.apiKey)

	// Execute request
	resp, err := t.client.Do(req)
	if err != nil {
		L_error("web_search: request failed", "error", err)
		return "", fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		L_error("web_search: API error", "status", resp.StatusCode, "body", string(body))
		return "", fmt.Errorf("search API error: %s", resp.Status)
	}

	// Parse response
	var searchResp BraveSearchResponse
	if err := json.Unmarshal(body, &searchResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	// Format results
	var results []string
	for i, result := range searchResp.Web.Results {
		if i >= count {
			break
		}
		results = append(results, fmt.Sprintf(
			"%d. %s\n   URL: %s\n   %s",
			i+1, result.Title, result.URL, result.Description,
		))
	}

	if len(results) == 0 {
		return "No results found.", nil
	}

	L_debug("web_search: completed", "results", len(results))
	return strings.Join(results, "\n\n"), nil
}

// BraveSearchResponse represents the Brave Search API response
type BraveSearchResponse struct {
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"results"`
	} `json:"web"`
}
