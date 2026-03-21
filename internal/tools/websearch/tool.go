package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/roelfdiedericks/goclaw/internal/contentguard"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	toolsconfig "github.com/roelfdiedericks/goclaw/internal/tools/config"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// Tool searches the web through configurable providers with retries/fallback.
type Tool struct {
	cfg       toolConfig
	providers map[string]ProviderDriver
}

// NewTool creates a new web search tool with multi-provider configuration.
func NewTool(webCfg toolsconfig.WebToolsConfig, llmCfg map[string]llm.LLMProviderConfig) *Tool {
	llmProviders := make(map[string]llmProviderCredential, len(llmCfg))
	for name, cfg := range llmCfg {
		llmProviders[name] = llmProviderCredential{
			Driver:  cfg.Driver,
			Subtype: cfg.Subtype,
			APIKey:  strings.TrimSpace(cfg.APIKey),
		}
	}

	return &Tool{
		cfg: toolConfig{
			Web:          webCfg,
			LLMProviders: llmProviders,
		},
		providers: map[string]ProviderDriver{
			providerBrave:      &braveDriver{},
			providerGrok:       &grokDriver{},
			providerPerplexity: &perplexityDriver{},
			providerGemini:     &geminiDriver{},
		},
	}
}

func (t *Tool) Name() string {
	return "web_search"
}

func (t *Tool) Description() string {
	return "Search the web for information. Returns titles, URLs, and snippets from search results."
}

func (t *Tool) Schema() map[string]interface{} {
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

func (t *Tool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var params struct {
		Query string `json:"query"`
		Count int    `json:"count"`
	}

	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	if params.Query == "" {
		return nil, fmt.Errorf("query is required")
	}

	if !t.cfg.Web.Search.Enabled {
		return nil, fmt.Errorf("web_search is disabled")
	}

	req := SearchRequest{
		Query: params.Query,
		Count: normalizeCount(params.Count),
	}
	providerChain := t.resolveProviderChain()
	if len(providerChain) == 0 {
		return nil, fmt.Errorf("no web_search providers configured")
	}

	maxFallback := t.cfg.Web.Search.MaxFallbackAttempts
	if maxFallback <= 0 || maxFallback > len(providerChain) {
		maxFallback = len(providerChain)
	}
	providerChain = providerChain[:maxFallback]

	L_debug("web_search: executing", "query", params.Query, "count", req.Count, "chain", strings.Join(providerIDs(providerChain), ","))

	var (
		result    SearchResponse
		lastErr   error
		attempted []string
	)
	for _, attempt := range providerChain {
		driver := t.providers[attempt.ID]
		if driver == nil {
			continue
		}
		attempted = append(attempted, attempt.ID)
		res, err := t.executeWithRetry(ctx, driver, req, attempt.Config)
		if err != nil {
			lastErr = err
			L_warn("web_search: provider failed", "provider", attempt.ID, "error", err)
			continue
		}
		result = res
		lastErr = nil
		break
	}

	if lastErr != nil {
		return nil, fmt.Errorf("web_search failed after providers [%s]: %w", strings.Join(attempted, ", "), lastErr)
	}

	content := formatSearchResponse(result)
	if strings.TrimSpace(content) == "" {
		content = "No results found."
	}
	safe := contentguard.ToolResultText(content)
	if safe.Changed {
		L_warn("web_search: sanitized result", "reason", safe.Reason, "mime", safe.MIME, "bytes", safe.OriginalBytes, "provider", result.Provider)
	}

	L_info("web_search: completed", "provider", result.Provider, "items", len(result.Items), "citations", len(result.Citations))
	return types.ExternalTextResult(safe.Text, "web"), nil
}

func (t *Tool) executeWithRetry(
	ctx context.Context,
	driver ProviderDriver,
	req SearchRequest,
	cfg ProviderConfig,
) (SearchResponse, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return SearchResponse{}, newProviderError(driver.ID(), "API key not configured", false, nil)
	}

	maxAttempts := t.cfg.Web.Search.Retry.MaxAttemptsPerProvider
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	if !t.cfg.Web.Search.Retry.Enabled {
		maxAttempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		res, err := driver.Search(ctx, req, cfg)
		if err == nil {
			return res, nil
		}
		lastErr = err
		if !isRetryableProviderError(err) || attempt == maxAttempts {
			return SearchResponse{}, err
		}

		delay := retryBackoff(t.cfg.Web.Search.Retry.BaseBackoffMs, t.cfg.Web.Search.Retry.MaxBackoffMs, attempt)
		L_warn("web_search: retrying provider", "provider", driver.ID(), "attempt", attempt+1, "delayMs", delay.Milliseconds(), "error", err)
		if err := sleepWithContext(ctx, delay); err != nil {
			return SearchResponse{}, err
		}
	}
	return SearchResponse{}, lastErr
}

func providerIDs(chain []providerAttempt) []string {
	out := make([]string, 0, len(chain))
	for _, p := range chain {
		out = append(out, p.ID)
	}
	return out
}

func formatSearchResponse(resp SearchResponse) string {
	lines := make([]string, 0, 12+len(resp.Items)*4)
	resultType := "results"
	hasAnswer := strings.TrimSpace(resp.Answer) != ""
	hasItems := len(resp.Items) > 0
	switch {
	case hasAnswer && hasItems:
		resultType = "mixed"
	case hasAnswer:
		resultType = "answer"
	case hasItems:
		resultType = "results"
	default:
		resultType = "empty"
	}

	lines = append(lines,
		fmt.Sprintf("Meta: provider=%s resultType=%s count=%d citations=%d", strings.TrimSpace(resp.Provider), resultType, len(resp.Items), len(resp.Citations)),
	)

	if strings.TrimSpace(resp.Answer) != "" {
		lines = append(lines, "", strings.TrimSpace(resp.Answer))
	}

	if len(resp.Items) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		for i, item := range resp.Items {
			title := strings.TrimSpace(item.Title)
			if title == "" {
				title = item.URL
			}
			lines = append(lines, fmt.Sprintf("%d. %s", i+1, title))
			if strings.TrimSpace(item.URL) != "" {
				lines = append(lines, "   URL: "+strings.TrimSpace(item.URL))
			}
			if strings.TrimSpace(item.Snippet) != "" {
				lines = append(lines, "   "+strings.TrimSpace(item.Snippet))
			}
			lines = append(lines, "")
		}
	}

	if len(resp.Citations) > 0 {
		lines = append(lines, "Citations:")
		for _, c := range resp.Citations {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			lines = append(lines, "- "+c)
		}
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}
