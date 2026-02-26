package webfetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	htmltomd "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/go-shiori/go-readability"
	"github.com/roelfdiedericks/goclaw/internal/browser"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// Tool fetches and extracts readable content from URLs
type Tool struct {
	client     *http.Client
	useBrowser string // "auto", "always", "never"
	profile    string // browser profile for rendering
	headless   bool   // run browser in headless mode
}

// NewTool creates a new web fetch tool
func NewTool() *Tool {
	return &Tool{
		client:     &http.Client{Timeout: 30 * time.Second},
		useBrowser: "auto",
		profile:    "default",
		headless:   true,
	}
}

// ToolConfig holds configuration for web fetch tool
type ToolConfig struct {
	UseBrowser string // "auto", "always", "never"
	Profile    string // browser profile for rendering
	Headless   bool   // run browser in headless mode (default: true)
}

// NewToolWithConfig creates a web fetch tool with config
func NewToolWithConfig(cfg ToolConfig) *Tool {
	useBrowser := cfg.UseBrowser
	if useBrowser == "" {
		useBrowser = "auto"
	}
	profile := cfg.Profile
	if profile == "" {
		profile = "default"
	}
	return &Tool{
		client:     &http.Client{Timeout: 30 * time.Second},
		useBrowser: useBrowser,
		profile:    profile,
		headless:   cfg.Headless,
	}
}

func (t *Tool) Name() string {
	return "web_fetch"
}

func (t *Tool) Description() string {
	return "Fetch a web page and extract its readable text content. Some sites with bot protection (Cloudflare) may block requests - use web_search as fallback."
}

func (t *Tool) Schema() map[string]interface{} {
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

func (t *Tool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var params struct {
		URL       string `json:"url"`
		MaxLength int    `json:"max_length"`
	}

	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	if params.URL == "" {
		return nil, fmt.Errorf("url is required")
	}

	// Validate URL for SSRF safety
	if err := browser.ValidateURLSafety(params.URL); err != nil {
		return nil, err
	}

	parsedURL, err := url.Parse(params.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	maxLen := params.MaxLength
	if maxLen <= 0 {
		maxLen = 10000
	}

	L_debug("web_fetch: fetching", "url", params.URL, "maxLength", maxLen, "useBrowser", t.useBrowser)

	if t.useBrowser == "never" {
		content, err := t.fetchWithHTTP(ctx, params.URL, maxLen, parsedURL)
		if err != nil {
			return nil, err
		}
		return types.ExternalTextResult(content, "web"), nil
	}

	mgr := browser.GetManager()
	if mgr != nil {
		content, err := t.fetchWithBrowser(ctx, params.URL, maxLen)
		if err == nil {
			return types.ExternalTextResult(content, "web"), nil
		}
		if t.useBrowser == "always" {
			return nil, err
		}
		L_warn("web_fetch: browser failed, falling back to HTTP", "url", params.URL, "error", err)
	} else {
		L_debug("web_fetch: browser not available, using HTTP", "url", params.URL)
	}

	content, err := t.fetchWithHTTP(ctx, params.URL, maxLen, parsedURL)
	if err != nil {
		return nil, err
	}
	return types.ExternalTextResult(content, "web"), nil
}

func (t *Tool) fetchWithHTTP(ctx context.Context, urlStr string, maxLen int, parsedURL *url.URL) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Sec-Ch-Ua", `"Chromium";v="122", "Not(A:Brand";v="24", "Google Chrome";v="122"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"Windows"`)
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	resp, err := t.client.Do(req)
	if err != nil {
		L_error("web_fetch: request failed", "error", err, "url", urlStr)
		return "", &fetchError{code: 0, msg: fmt.Sprintf("failed to fetch URL: %v", err), retryable: true}
	}
	defer resp.Body.Close()

	if resp.StatusCode == 403 || resp.StatusCode == 503 {
		L_warn("web_fetch: bot protection detected", "status", resp.StatusCode, "url", urlStr)
		return "", &fetchError{code: resp.StatusCode, msg: fmt.Sprintf("HTTP %d - likely bot protection", resp.StatusCode), retryable: true}
	}

	if resp.StatusCode != http.StatusOK {
		L_warn("web_fetch: non-200 status", "status", resp.StatusCode, "url", urlStr)
		return "", &fetchError{code: resp.StatusCode, msg: fmt.Sprintf("HTTP error: %s", resp.Status), retryable: false}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxLen*2)))
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	bodyStr := string(body)
	L_debug("web_fetch: raw HTML received", "url", urlStr, "htmlLength", len(bodyStr))

	if t.isCloudflareChallenge(bodyStr) {
		L_warn("web_fetch: Cloudflare challenge detected in body", "url", urlStr)
		return "", &fetchError{code: 403, msg: "Cloudflare challenge page", retryable: true}
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/html") && !strings.Contains(contentType, "application/xhtml") {
		if len(bodyStr) > maxLen {
			bodyStr = bodyStr[:maxLen]
		}
		L_debug("web_fetch: non-HTML content", "contentType", contentType, "length", len(bodyStr))
		return bodyStr, nil
	}

	article, err := readability.FromReader(strings.NewReader(bodyStr), parsedURL)
	if err != nil {
		L_error("web_fetch: readability parse failed", "error", err, "url", urlStr)
		return "", fmt.Errorf("failed to parse page: %w", err)
	}

	textLen := len(strings.TrimSpace(article.TextContent))
	L_debug("web_fetch: readability extracted", "url", urlStr, "textLength", textLen, "title", article.Title)

	if textLen < 200 {
		L_warn("web_fetch: minimal content extracted (likely JS SPA)", "url", urlStr, "textLength", textLen)
		return "", &fetchError{code: 0, msg: fmt.Sprintf("minimal content (%d chars) - likely JS-rendered page", textLen), retryable: true}
	}

	content := t.formatArticle(article, urlStr, maxLen)
	L_debug("web_fetch: completed via HTTP", "url", urlStr, "contentLength", len(content), "title", article.Title)
	return content, nil
}

func (t *Tool) fetchWithBrowser(ctx context.Context, urlStr string, maxLen int) (string, error) {
	startTotal := time.Now()

	mgr := browser.GetManager()
	if mgr == nil {
		return "", fmt.Errorf("browser not available (not initialized)")
	}

	profile := t.profile
	L_debug("web_fetch: using browser", "url", urlStr, "profile", profile)

	startPage := time.Now()
	page, err := mgr.GetBackgroundStealthPage(profile, false)
	if err != nil {
		return "", fmt.Errorf("failed to create page: %w", err)
	}
	defer page.Close()
	L_trace("web_fetch: page created", "took", time.Since(startPage))

	page = page.Timeout(60 * time.Second)

	startNav := time.Now()
	if err := page.Navigate(urlStr); err != nil {
		return "", fmt.Errorf("browser navigation failed: %w", err)
	}
	L_trace("web_fetch: navigate done", "took", time.Since(startNav))

	startWait := time.Now()
	if err := page.WaitLoad(); err != nil {
		L_warn("web_fetch: WaitLoad timeout", "url", urlStr, "took", time.Since(startWait))
	} else {
		L_trace("web_fetch: page loaded", "took", time.Since(startWait))
	}

	startStable := time.Now()
	stablePage := page.Timeout(3 * time.Second)
	if err := stablePage.WaitStable(500 * time.Millisecond); err != nil {
		L_debug("web_fetch: stability timeout (normal for SPAs)", "url", urlStr, "took", time.Since(startStable))
	} else {
		L_debug("web_fetch: page stable", "url", urlStr, "took", time.Since(startStable))
	}

	info, err := page.Info()
	if err != nil {
		return "", fmt.Errorf("failed to get page info: %w", err)
	}

	startHTML := time.Now()
	html, err := page.HTML()
	if err != nil {
		return "", fmt.Errorf("failed to get page HTML: %w", err)
	}
	L_debug("web_fetch: browser got HTML", "url", urlStr, "htmlLength", len(html), "took", time.Since(startHTML))

	startConvert := time.Now()
	markdown, err := htmltomd.ConvertString(html)
	if err != nil {
		L_warn("web_fetch: html-to-markdown failed, falling back to readability", "error", err)
		parsedURL, _ := url.Parse(urlStr)
		article, readErr := readability.FromReader(strings.NewReader(html), parsedURL)
		if readErr != nil {
			return "", fmt.Errorf("failed to extract content: %w", readErr)
		}
		content := t.formatArticle(article, urlStr, maxLen)
		content = strings.Replace(content, "URL: "+urlStr, "URL: "+urlStr+"\n[Fetched via browser]", 1)
		L_debug("web_fetch: browser fallback to readability", "url", urlStr, "contentLength", len(content))
		return content, nil
	}
	L_debug("web_fetch: markdown converted", "url", urlStr, "markdownLength", len(markdown), "took", time.Since(startConvert))

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Title: %s\n", info.Title))
	result.WriteString(fmt.Sprintf("URL: %s\n", urlStr))
	result.WriteString("[Fetched via browser]\n\n---\n\n")
	result.WriteString(markdown)

	content := result.String()
	if len(content) > maxLen {
		content = content[:maxLen] + "\n\n[Content truncated...]"
	}

	L_info("web_fetch: browser fetch complete", "url", urlStr, "title", info.Title, "chars", len(content), "took", time.Since(startTotal))
	return content, nil
}

func (t *Tool) formatArticle(article readability.Article, urlStr string, maxLen int) string {
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Title: %s\n", article.Title))
	if article.Byline != "" {
		result.WriteString(fmt.Sprintf("Author: %s\n", article.Byline))
	}
	if article.SiteName != "" {
		result.WriteString(fmt.Sprintf("Site: %s\n", article.SiteName))
	}
	result.WriteString(fmt.Sprintf("URL: %s\n\n", urlStr))
	result.WriteString("---\n\n")
	result.WriteString(article.TextContent)

	content := result.String()
	if len(content) > maxLen {
		content = content[:maxLen] + "\n\n[Content truncated...]"
	}
	return content
}

func (t *Tool) isCloudflareChallenge(body string) bool {
	indicators := []string{
		"cf-browser-verification",
		"cf_chl_opt",
		"__cf_chl_f_tk",
		"challenge-platform",
		"Checking your browser",
		"DDoS protection by Cloudflare",
		"Ray ID:",
		"cf-spinner",
	}

	bodyLower := strings.ToLower(body)
	for _, indicator := range indicators {
		if strings.Contains(bodyLower, strings.ToLower(indicator)) {
			return true
		}
	}
	return false
}

type fetchError struct {
	code      int
	msg       string
	retryable bool
}

func (e *fetchError) Error() string {
	return e.msg
}
