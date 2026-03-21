package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	toolsconfig "github.com/roelfdiedericks/goclaw/internal/tools/config"
)

type fakeDriver struct {
	id       string
	calls    int
	sequence []error
	result   SearchResponse
}

func (d *fakeDriver) ID() string { return d.id }

func (d *fakeDriver) Search(_ context.Context, _ SearchRequest, _ ProviderConfig) (SearchResponse, error) {
	d.calls++
	idx := d.calls - 1
	if idx < len(d.sequence) && d.sequence[idx] != nil {
		return SearchResponse{}, d.sequence[idx]
	}
	return d.result, nil
}

func baseToolForTest() *Tool {
	return &Tool{
		cfg: toolConfig{
			Web: toolsconfig.WebToolsConfig{
				Search: toolsconfig.WebSearchConfig{
					Enabled:             true,
					Provider:            "auto",
					MaxFallbackAttempts: 4,
					Retry: toolsconfig.WebSearchRetryConfig{
						Enabled:                true,
						MaxAttemptsPerProvider: 2,
						BaseBackoffMs:          1,
						MaxBackoffMs:           2,
					},
					Providers: toolsconfig.WebSearchProvidersConfig{
						Grok:  toolsconfig.WebSearchProviderConfig{APIKey: "grok-key"},
						Brave: toolsconfig.WebSearchProviderConfig{APIKey: "brave-key"},
					},
				},
			},
		},
		providers: map[string]ProviderDriver{},
	}
}

func TestExecuteWithRetry_RetryableThenSuccess(t *testing.T) {
	tool := baseToolForTest()
	driver := &fakeDriver{
		id: providerGrok,
		sequence: []error{
			newProviderHTTPError(providerGrok, 429, "rate limited"),
			nil,
		},
		result: SearchResponse{Provider: providerGrok, Answer: "ok"},
	}

	res, err := tool.executeWithRetry(
		context.Background(),
		driver,
		SearchRequest{Query: "x", Count: 1},
		ProviderConfig{APIKey: "k"},
	)
	if err != nil {
		t.Fatalf("expected success after retry, got error: %v", err)
	}
	if res.Provider != providerGrok {
		t.Fatalf("expected provider grok, got %q", res.Provider)
	}
	if driver.calls != 2 {
		t.Fatalf("expected 2 calls, got %d", driver.calls)
	}
}

func TestExecuteWithRetry_NonRetryableStops(t *testing.T) {
	tool := baseToolForTest()
	driver := &fakeDriver{
		id:       providerGrok,
		sequence: []error{newProviderHTTPError(providerGrok, 401, "unauthorized")},
	}

	_, err := tool.executeWithRetry(
		context.Background(),
		driver,
		SearchRequest{Query: "x", Count: 1},
		ProviderConfig{APIKey: "k"},
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if driver.calls != 1 {
		t.Fatalf("expected single call for non-retryable error, got %d", driver.calls)
	}
}

func TestExecute_FallbackAcrossProviders(t *testing.T) {
	tool := baseToolForTest()
	grok := &fakeDriver{
		id:       providerGrok,
		sequence: []error{newProviderHTTPError(providerGrok, 503, "upstream down"), newProviderHTTPError(providerGrok, 503, "still down")},
	}
	brave := &fakeDriver{
		id: providerBrave,
		result: SearchResponse{
			Provider: providerBrave,
			Items: []SearchItem{
				{Title: "Result", URL: "https://example.com", Snippet: "ok"},
			},
		},
	}
	tool.providers = map[string]ProviderDriver{
		providerGrok:  grok,
		providerBrave: brave,
	}

	raw, _ := json.Marshal(map[string]any{
		"query": "hello",
		"count": 3,
	})
	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("expected fallback success, got error: %v", err)
	}
	if grok.calls != 2 {
		t.Fatalf("expected grok retries to exhaust first, got %d calls", grok.calls)
	}
	if brave.calls != 1 {
		t.Fatalf("expected brave to execute once after fallback, got %d", brave.calls)
	}
	if len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "https://example.com") {
		t.Fatalf("unexpected tool result content: %+v", result.Content)
	}
	if !strings.Contains(result.Content[0].Text, "Meta: provider=brave") {
		t.Fatalf("expected provider metadata in output, got: %s", result.Content[0].Text)
	}
}

func TestIsRetryableProviderError(t *testing.T) {
	if !isRetryableProviderError(newProviderHTTPError(providerBrave, 429, "rate")) {
		t.Fatal("expected 429 to be retryable")
	}
	if isRetryableProviderError(newProviderHTTPError(providerBrave, 403, "forbidden")) {
		t.Fatal("expected 403 to be non-retryable")
	}
	if isRetryableProviderError(errors.New("generic")) {
		t.Fatal("expected generic error to be non-retryable")
	}
}
