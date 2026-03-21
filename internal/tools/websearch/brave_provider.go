package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type braveDriver struct{}

func (d *braveDriver) ID() string { return providerBrave }

func (d *braveDriver) Search(ctx context.Context, req SearchRequest, cfg ProviderConfig) (SearchResponse, error) {
	if cfg.APIKey == "" {
		return SearchResponse{}, newProviderError(providerBrave, "Brave API key not configured", false, nil)
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.search.brave.com/res/v1/web/search"
	}

	reqURL, err := url.Parse(baseURL)
	if err != nil {
		return SearchResponse{}, newProviderError(providerBrave, "invalid Brave base URL", false, err)
	}
	q := reqURL.Query()
	q.Set("q", req.Query)
	q.Set("count", fmt.Sprintf("%d", normalizeCount(req.Count)))
	reqURL.RawQuery = q.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return SearchResponse{}, newProviderError(providerBrave, "failed to build request", false, err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Accept-Language", "en-US,en;q=0.9")
	httpReq.Header.Set("X-Subscription-Token", cfg.APIKey)

	resp, err := newHTTPClient(30).Do(httpReq)
	if err != nil {
		return SearchResponse{}, newProviderError(providerBrave, "request failed", true, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return SearchResponse{}, newProviderError(providerBrave, "failed to read response", true, err)
	}
	if resp.StatusCode != http.StatusOK {
		return SearchResponse{}, newProviderHTTPError(providerBrave, resp.StatusCode, string(body))
	}

	var parsed struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return SearchResponse{}, newProviderError(providerBrave, "failed to parse response", false, err)
	}

	items := make([]SearchItem, 0, len(parsed.Web.Results))
	for i, r := range parsed.Web.Results {
		if i >= normalizeCount(req.Count) {
			break
		}
		items = append(items, SearchItem{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Description,
		})
	}

	return SearchResponse{
		Provider: providerBrave,
		Items:    items,
	}, nil
}
