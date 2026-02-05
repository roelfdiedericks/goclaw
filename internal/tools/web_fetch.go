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

	htmltomd "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/go-rod/rod"
	"github.com/go-shiori/go-readability"
	"github.com/roelfdiedericks/goclaw/internal/browser"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// WebFetchTool fetches and extracts readable content from URLs
type WebFetchTool struct {
	client     *http.Client
	useBrowser string // "auto", "always", "never"
	profile    string // browser profile for rendering
	headless   bool   // run browser in headless mode
}

// NewWebFetchTool creates a new web fetch tool
func NewWebFetchTool() *WebFetchTool {
	return &WebFetchTool{
		client:     &http.Client{Timeout: 30 * time.Second},
		useBrowser: "auto",
		profile:    "default",
		headless:   true,
	}
}

// WebFetchConfig holds configuration for web fetch tool
type WebFetchConfig struct {
	UseBrowser string // "auto", "always", "never"
	Profile    string // browser profile for rendering
	Headless   bool   // run browser in headless mode (default: true)
}

// NewWebFetchToolWithConfig creates a web fetch tool with config
func NewWebFetchToolWithConfig(cfg WebFetchConfig) *WebFetchTool {
	useBrowser := cfg.UseBrowser
	if useBrowser == "" {
		useBrowser = "auto"
	}
	profile := cfg.Profile
	if profile == "" {
		profile = "default"
	}
	return &WebFetchTool{
		client:     &http.Client{Timeout: 30 * time.Second},
		useBrowser: useBrowser,
		profile:    profile,
		headless:   cfg.Headless,
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

	L_debug("web_fetch: fetching", "url", params.URL, "maxLength", maxLen, "useBrowser", t.useBrowser)

	// If "never" use browser, use HTTP only
	if t.useBrowser == "never" {
		return t.fetchWithHTTP(ctx, params.URL, maxLen, parsedURL)
	}

	// Default behavior: try browser first (better for modern JS sites)
	// Fall back to HTTP if browser unavailable or fails
	mgr := browser.GetManager()
	if mgr != nil {
		content, err := t.fetchWithBrowser(ctx, params.URL, maxLen)
		if err == nil {
			return content, nil
		}
		// Browser failed - fall back to HTTP unless "always" mode
		if t.useBrowser == "always" {
			return "", err // Don't fall back
		}
		L_warn("web_fetch: browser failed, falling back to HTTP", "url", params.URL, "error", err)
	} else {
		L_debug("web_fetch: browser not available, using HTTP", "url", params.URL)
	}

	// HTTP fallback
	return t.fetchWithHTTP(ctx, params.URL, maxLen, parsedURL)
}

// fetchWithHTTP performs a standard HTTP fetch
func (t *WebFetchTool) fetchWithHTTP(ctx context.Context, urlStr string, maxLen int, parsedURL *url.URL) (string, error) {
	// Create request
	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
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
		L_error("web_fetch: request failed", "error", err, "url", urlStr)
		return "", &fetchError{code: 0, msg: fmt.Sprintf("failed to fetch URL: %v", err), retryable: true}
	}
	defer resp.Body.Close()

	// Check for bot-protection status codes
	if resp.StatusCode == 403 || resp.StatusCode == 503 {
		L_warn("web_fetch: bot protection detected", "status", resp.StatusCode, "url", urlStr)
		return "", &fetchError{code: resp.StatusCode, msg: fmt.Sprintf("HTTP %d - likely bot protection", resp.StatusCode), retryable: true}
	}

	if resp.StatusCode != http.StatusOK {
		L_warn("web_fetch: non-200 status", "status", resp.StatusCode, "url", urlStr)
		return "", &fetchError{code: resp.StatusCode, msg: fmt.Sprintf("HTTP error: %s", resp.Status), retryable: false}
	}

	// Read body
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxLen*2))) // Read extra for detection
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	// Check for Cloudflare challenge in body
	bodyStr := string(body)
	L_debug("web_fetch: raw HTML received", "url", urlStr, "htmlLength", len(bodyStr))
	
	if t.isCloudflareChallenge(bodyStr) {
		L_warn("web_fetch: Cloudflare challenge detected in body", "url", urlStr)
		return "", &fetchError{code: 403, msg: "Cloudflare challenge page", retryable: true}
	}

	// Check content type
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/html") && !strings.Contains(contentType, "application/xhtml") {
		// For non-HTML content, just return the raw text (truncated)
		if len(bodyStr) > maxLen {
			bodyStr = bodyStr[:maxLen]
		}
		L_debug("web_fetch: non-HTML content", "contentType", contentType, "length", len(bodyStr))
		return bodyStr, nil
	}

	// Parse with readability
	article, err := readability.FromReader(strings.NewReader(bodyStr), parsedURL)
	if err != nil {
		L_error("web_fetch: readability parse failed", "error", err, "url", urlStr)
		return "", fmt.Errorf("failed to parse page: %w", err)
	}

	// Check for minimal content (likely JS-rendered SPA)
	textLen := len(strings.TrimSpace(article.TextContent))
	L_debug("web_fetch: readability extracted", "url", urlStr, "textLength", textLen, "title", article.Title)
	
	if textLen < 200 {
		L_warn("web_fetch: minimal content extracted (likely JS SPA)", "url", urlStr, "textLength", textLen)
		return "", &fetchError{code: 0, msg: fmt.Sprintf("minimal content (%d chars) - likely JS-rendered page", textLen), retryable: true}
	}

	// Build result
	content := t.formatArticle(article, urlStr, maxLen)
	L_debug("web_fetch: completed via HTTP", "url", urlStr, "contentLength", len(content), "title", article.Title)
	return content, nil
}

// fetchWithBrowser uses the browser to render and fetch the page
func (t *WebFetchTool) fetchWithBrowser(ctx context.Context, urlStr string, maxLen int) (string, error) {
	startTotal := time.Now()

	mgr := browser.GetManager()
	if mgr == nil {
		return "", fmt.Errorf("browser not available (not initialized)")
	}

	// Use configured profile
	profile := t.profile
	mode := "headless"
	if !t.headless {
		mode = "headed"
	}
	L_debug("web_fetch: using browser", "url", urlStr, "profile", profile, "mode", mode)

	// Create page (headless or headed based on config)
	startPage := time.Now()
	var page *rod.Page
	var browserInstance *rod.Browser
	var err error

	if t.headless {
		// Normal headless mode - use pooled stealth page
		page, err = mgr.GetStealthPage(profile)
		if err != nil {
			return "", fmt.Errorf("failed to create page: %w", err)
		}
		defer page.Close()
	} else {
		// Headed mode for debugging - launch visible browser
		browserInstance, page, err = mgr.LaunchHeaded(profile, urlStr)
		if err != nil {
			return "", fmt.Errorf("failed to launch headed browser: %w", err)
		}
		defer page.Close()
		// Note: browserInstance stays alive for potential reuse
		_ = browserInstance
	}
	L_trace("web_fetch: page created", "mode", mode, "took", time.Since(startPage))

	// Set timeout for page operations
	page = page.Timeout(60 * time.Second)

	// Navigate (only needed for headless - LaunchHeaded already navigated)
	if t.headless {
		startNav := time.Now()
		if err := page.Navigate(urlStr); err != nil {
			return "", fmt.Errorf("browser navigation failed: %w", err)
		}
		L_trace("web_fetch: navigate done", "took", time.Since(startNav))
	}

	// Wait for page load event
	startWait := time.Now()
	if err := page.WaitLoad(); err != nil {
		L_warn("web_fetch: WaitLoad timeout", "url", urlStr, "took", time.Since(startWait))
	} else {
		L_trace("web_fetch: page loaded", "took", time.Since(startWait))
	}

	// Brief stability wait - 500ms without activity, max 3s total
	startStable := time.Now()
	stablePage := page.Timeout(3 * time.Second)
	if err := stablePage.WaitStable(500 * time.Millisecond); err != nil {
		L_debug("web_fetch: stability timeout (normal for SPAs)", "url", urlStr, "took", time.Since(startStable))
	} else {
		L_debug("web_fetch: page stable", "url", urlStr, "took", time.Since(startStable))
	}

	// Get page info
	info, err := page.Info()
	if err != nil {
		return "", fmt.Errorf("failed to get page info: %w", err)
	}

	// Get rendered HTML
	startHTML := time.Now()
	html, err := page.HTML()
	if err != nil {
		return "", fmt.Errorf("failed to get page HTML: %w", err)
	}
	L_debug("web_fetch: browser got HTML", "url", urlStr, "htmlLength", len(html), "took", time.Since(startHTML))

	// Convert HTML to Markdown
	startConvert := time.Now()
	markdown, err := htmltomd.ConvertString(html)
	if err != nil {
		L_warn("web_fetch: html-to-markdown failed, falling back to readability", "error", err)
		// Fallback to readability
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

	// Build result with header
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

// formatArticle formats a readability article into output string
func (t *WebFetchTool) formatArticle(article readability.Article, urlStr string, maxLen int) string {
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

	// Truncate if needed
	if len(content) > maxLen {
		content = content[:maxLen] + "\n\n[Content truncated...]"
	}

	return content
}

// isCloudflareChallenge detects Cloudflare challenge pages
func (t *WebFetchTool) isCloudflareChallenge(body string) bool {
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

// fetchError represents a fetch error with retry information
type fetchError struct {
	code      int
	msg       string
	retryable bool
}

func (e *fetchError) Error() string {
	return e.msg
}
