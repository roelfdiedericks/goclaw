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
		model = "grok-4-1-fast-reasoning"
	}

	payload := map[string]any{
		"model": model,
		"input": []map[string]string{
			{"role": "user", "content": req.Query},
		},
		"tools": []map[string]any{
			{"type": "web_search"},
		},
	}
	data, _ := json.Marshal(payload)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/responses", bytes.NewReader(data))
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
		OutputText string   `json:"output_text"`
		Citations  []string `json:"citations"`
		Output     []struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return SearchResponse{}, newProviderError(providerGrok, "failed to parse response", false, err)
	}

	answer := strings.TrimSpace(parsed.OutputText)
	if answer == "" {
		answer = extractXAIOutputText(parsed.Output)
	}

	if strings.TrimSpace(answer) == "" {
		return SearchResponse{}, newProviderError(providerGrok, "empty response from Grok web search", true, nil)
	}

	return SearchResponse{
		Provider:  providerGrok,
		Answer:    answer,
		Citations: normalizeCitations(parsed.Citations),
	}, nil
}

func extractXAIOutputText(outputs []struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}) string {
	for _, out := range outputs {
		if strings.TrimSpace(out.Text) != "" {
			return strings.TrimSpace(out.Text)
		}
		for _, block := range out.Content {
			if strings.TrimSpace(block.Text) != "" {
				return strings.TrimSpace(block.Text)
			}
		}
	}
	return ""
}

func normalizeCitations(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, c := range in {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}
