package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

type grokDriver struct{}

func (d *grokDriver) ID() string { return providerGrok }

func (d *grokDriver) Search(ctx context.Context, req SearchRequest, cfg ProviderConfig) (SearchResponse, error) {
	if cfg.APIKey == "" {
		return SearchResponse{}, newProviderError(providerGrok, "Grok API key not configured", false, nil)
	}

	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.x.ai/v1"
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = "grok-4-0709"
	}

	payload := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": "Answer using web search and include sources when available."},
			{"role": "user", "content": req.Query},
		},
		"tools": []map[string]string{
			{"type": "web_search"},
		},
	}
	data, _ := json.Marshal(payload)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return SearchResponse{}, newProviderError(providerGrok, "failed to build request", false, err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := newHTTPClient(45).Do(httpReq)
	if err != nil {
		return SearchResponse{}, newProviderError(providerGrok, "request failed", true, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return SearchResponse{}, newProviderError(providerGrok, "failed to read response", true, err)
	}
	if resp.StatusCode != http.StatusOK {
		return SearchResponse{}, newProviderHTTPError(providerGrok, resp.StatusCode, string(body))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content any `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return SearchResponse{}, newProviderError(providerGrok, "failed to parse response", false, err)
	}

	answer := ""
	if len(parsed.Choices) > 0 {
		switch content := parsed.Choices[0].Message.Content.(type) {
		case string:
			answer = content
		case []any:
			parts := make([]string, 0, len(content))
			for _, part := range content {
				if block, ok := part.(map[string]any); ok {
					if txt, ok := block["text"].(string); ok && strings.TrimSpace(txt) != "" {
						parts = append(parts, strings.TrimSpace(txt))
					}
				}
			}
			answer = strings.Join(parts, "\n")
		}
	}

	if strings.TrimSpace(answer) == "" {
		return SearchResponse{}, newProviderError(providerGrok, "empty response from Grok web search", true, nil)
	}

	return SearchResponse{
		Provider: providerGrok,
		Answer:   answer,
	}, nil
}
