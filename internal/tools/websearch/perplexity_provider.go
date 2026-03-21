package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

type perplexityDriver struct{}

func (d *perplexityDriver) ID() string { return providerPerplexity }

func (d *perplexityDriver) Search(ctx context.Context, req SearchRequest, cfg ProviderConfig) (SearchResponse, error) {
	if cfg.APIKey == "" {
		return SearchResponse{}, newProviderError(providerPerplexity, "Perplexity API key not configured", false, nil)
	}

	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.perplexity.ai"
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = "sonar"
	}

	payload := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": "Use web search and provide concise, sourced results."},
			{"role": "user", "content": req.Query},
		},
	}
	data, _ := json.Marshal(payload)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return SearchResponse{}, newProviderError(providerPerplexity, "failed to build request", false, err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := newHTTPClient(45).Do(httpReq)
	if err != nil {
		return SearchResponse{}, newProviderError(providerPerplexity, "request failed", true, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return SearchResponse{}, newProviderError(providerPerplexity, "failed to read response", true, err)
	}
	if resp.StatusCode != http.StatusOK {
		return SearchResponse{}, newProviderHTTPError(providerPerplexity, resp.StatusCode, string(body))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Citations []string `json:"citations"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return SearchResponse{}, newProviderError(providerPerplexity, "failed to parse response", false, err)
	}

	answer := ""
	if len(parsed.Choices) > 0 {
		answer = strings.TrimSpace(parsed.Choices[0].Message.Content)
	}
	if answer == "" && len(parsed.Citations) == 0 {
		return SearchResponse{}, newProviderError(providerPerplexity, "empty response from Perplexity", true, nil)
	}

	return SearchResponse{
		Provider:  providerPerplexity,
		Answer:    answer,
		Citations: parsed.Citations,
	}, nil
}
