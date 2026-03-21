package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type geminiDriver struct{}

func (d *geminiDriver) ID() string { return providerGemini }

func (d *geminiDriver) Search(ctx context.Context, req SearchRequest, cfg ProviderConfig) (SearchResponse, error) {
	if cfg.APIKey == "" {
		return SearchResponse{}, newProviderError(providerGemini, "Gemini API key not configured", false, nil)
	}

	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com/v1beta"
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = "gemini-2.5-flash"
	}

	endpoint := fmt.Sprintf("%s/models/%s:generateContent", baseURL, url.PathEscape(model))
	endpoint += "?key=" + url.QueryEscape(cfg.APIKey)

	payload := map[string]any{
		"contents": []map[string]any{
			{
				"role": "user",
				"parts": []map[string]string{
					{"text": req.Query},
				},
			},
		},
		"tools": []map[string]any{
			{
				"google_search": map[string]any{},
			},
		},
	}
	data, _ := json.Marshal(payload)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return SearchResponse{}, newProviderError(providerGemini, "failed to build request", false, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := newHTTPClient(45).Do(httpReq)
	if err != nil {
		return SearchResponse{}, newProviderError(providerGemini, "request failed", true, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return SearchResponse{}, newProviderError(providerGemini, "failed to read response", true, err)
	}
	if resp.StatusCode != http.StatusOK {
		return SearchResponse{}, newProviderHTTPError(providerGemini, resp.StatusCode, string(body))
	}

	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			GroundingMetadata struct {
				GroundingChunks []struct {
					Web struct {
						URI   string `json:"uri"`
						Title string `json:"title"`
					} `json:"web"`
				} `json:"groundingChunks"`
			} `json:"groundingMetadata"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return SearchResponse{}, newProviderError(providerGemini, "failed to parse response", false, err)
	}

	answer := ""
	citations := make([]string, 0)
	items := make([]SearchItem, 0)

	if len(parsed.Candidates) > 0 {
		for _, p := range parsed.Candidates[0].Content.Parts {
			if strings.TrimSpace(p.Text) == "" {
				continue
			}
			if answer != "" {
				answer += "\n"
			}
			answer += strings.TrimSpace(p.Text)
		}
		for _, c := range parsed.Candidates[0].GroundingMetadata.GroundingChunks {
			if strings.TrimSpace(c.Web.URI) == "" {
				continue
			}
			citations = append(citations, c.Web.URI)
			items = append(items, SearchItem{
				Title: c.Web.Title,
				URL:   c.Web.URI,
			})
		}
	}

	if answer == "" && len(items) == 0 {
		return SearchResponse{}, newProviderError(providerGemini, "empty response from Gemini", true, nil)
	}

	return SearchResponse{
		Provider:  providerGemini,
		Answer:    answer,
		Items:     items,
		Citations: citations,
	}, nil
}
