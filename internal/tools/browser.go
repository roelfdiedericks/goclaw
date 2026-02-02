package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
	"github.com/go-shiori/go-readability"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
)

// BrowserTool provides headless browser automation for JS-rendered pages
type BrowserTool struct {
	pool       *BrowserPool
	timeout    time.Duration
	mediaStore *media.MediaStore
}

// BrowserPoolConfig contains browser pool configuration
type BrowserPoolConfig struct {
	Headless  bool   // Run in headless mode
	NoSandbox bool   // Disable Chrome sandbox (needed for Docker/root)
	Profile   string // Browser profile name
}

// BrowserPool manages the browser instance and pages
type BrowserPool struct {
	browser *rod.Browser
	pages   map[string]*rod.Page
	mu      sync.Mutex
	config  BrowserPoolConfig
}

// NewBrowserPool creates a browser pool with stealth mode
func NewBrowserPool(cfg BrowserPoolConfig) (*BrowserPool, error) {
	L_debug("browser: launching browser", "headless", cfg.Headless, "noSandbox", cfg.NoSandbox, "profile", cfg.Profile)

	// Launch browser with stealth options
	path, _ := launcher.LookPath()
	l := launcher.New().
		Bin(path).
		Headless(cfg.Headless).
		Set("disable-blink-features", "AutomationControlled").
		Set("disable-dev-shm-usage") // For Docker/limited memory

	// Only set no-sandbox if configured (security consideration)
	if cfg.NoSandbox {
		l = l.Set("no-sandbox")
	}

	controlURL, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("failed to launch browser: %w", err)
	}

	browser := rod.New().ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		return nil, fmt.Errorf("failed to connect to browser: %w", err)
	}

	L_debug("browser: connected", "controlURL", controlURL)

	return &BrowserPool{
		browser: browser,
		pages:   make(map[string]*rod.Page),
		config:  cfg,
	}, nil
}

// GetPage gets or creates a page for the given session ID
func (p *BrowserPool) GetPage(sessionID string) (*rod.Page, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if page, ok := p.pages[sessionID]; ok {
		return page, nil
	}

	// Create stealth page
	page, err := stealth.Page(p.browser)
	if err != nil {
		return nil, fmt.Errorf("failed to create stealth page: %w", err)
	}

	p.pages[sessionID] = page
	L_debug("browser: created new page", "sessionID", sessionID)
	return page, nil
}

// ClosePage closes and removes a page
func (p *BrowserPool) ClosePage(sessionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if page, ok := p.pages[sessionID]; ok {
		page.Close()
		delete(p.pages, sessionID)
		L_debug("browser: closed page", "sessionID", sessionID)
	}
}

// Close closes the browser and all pages
func (p *BrowserPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for id, page := range p.pages {
		page.Close()
		delete(p.pages, id)
	}

	if p.browser != nil {
		p.browser.Close()
		L_debug("browser: closed browser")
	}
}

// NewBrowserTool creates a new browser tool
func NewBrowserTool(pool *BrowserPool, mediaStore *media.MediaStore) *BrowserTool {
	return &BrowserTool{
		pool:       pool,
		timeout:    60 * time.Second,
		mediaStore: mediaStore,
	}
}

func (t *BrowserTool) Name() string {
	return "browser"
}

func (t *BrowserTool) Description() string {
	return "Headless browser for JavaScript-rendered pages or sites with bot protection (Cloudflare). Actions: snapshot (extract text), screenshot (capture image), navigate (go to URL). Use when web_fetch returns 403 or empty content."
}

func (t *BrowserTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"snapshot", "screenshot", "navigate"},
				"description": "Action to perform: snapshot (extract text), screenshot (capture image), navigate (go to URL)",
			},
			"url": map[string]interface{}{
				"type":        "string",
				"description": "URL to navigate to (required for navigate and snapshot with new URL)",
			},
			"fullPage": map[string]interface{}{
				"type":        "boolean",
				"description": "Capture full page screenshot (default: false, viewport only)",
			},
			"maxLength": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum text length for snapshot (default: 15000)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *BrowserTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Action    string `json:"action"`
		URL       string `json:"url"`
		FullPage  bool   `json:"fullPage"`
		MaxLength int    `json:"maxLength"`
	}

	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if params.Action == "" {
		return "", fmt.Errorf("action is required")
	}

	if params.MaxLength <= 0 {
		params.MaxLength = 15000
	}

	L_debug("browser: executing", "action", params.Action, "url", params.URL)

	// Use a default session ID for now (could be passed from agent context later)
	sessionID := "default"

	switch params.Action {
	case "snapshot":
		return t.snapshot(ctx, sessionID, params.URL, params.MaxLength)
	case "screenshot":
		return t.screenshot(ctx, sessionID, params.URL, params.FullPage)
	case "navigate":
		return t.navigate(ctx, sessionID, params.URL)
	default:
		return "", fmt.Errorf("unknown action: %s", params.Action)
	}
}

// snapshot extracts readable text from the current or specified page
func (t *BrowserTool) snapshot(ctx context.Context, sessionID, urlStr string, maxLength int) (string, error) {
	page, err := t.pool.GetPage(sessionID)
	if err != nil {
		return "", err
	}

	// Navigate if URL provided
	if urlStr != "" {
		if err := t.navigateTo(page, urlStr); err != nil {
			return "", err
		}
	}

	// Get current URL for readability
	info, err := page.Info()
	if err != nil {
		return "", fmt.Errorf("failed to get page info: %w", err)
	}

	L_debug("browser: getting snapshot", "url", info.URL, "title", info.Title)

	// Get rendered HTML
	html, err := page.HTML()
	if err != nil {
		return "", fmt.Errorf("failed to get page HTML: %w", err)
	}

	// Parse URL for readability
	parsedURL, _ := url.Parse(info.URL)

	// Use readability to extract content
	article, err := readability.FromReader(strings.NewReader(html), parsedURL)
	if err != nil {
		L_warn("browser: readability failed, using raw text", "error", err)
		// Fallback to raw text
		text, err := page.MustElement("body").Text()
		if err != nil {
			return "", fmt.Errorf("failed to get page text: %w", err)
		}
		if len(text) > maxLength {
			text = text[:maxLength] + "\n\n[Content truncated...]"
		}
		return fmt.Sprintf("Title: %s\nURL: %s\n\n---\n\n%s", info.Title, info.URL, text), nil
	}

	// Build result
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Title: %s\n", article.Title))
	if article.Byline != "" {
		result.WriteString(fmt.Sprintf("Author: %s\n", article.Byline))
	}
	result.WriteString(fmt.Sprintf("URL: %s\n\n---\n\n", info.URL))
	result.WriteString(article.TextContent)

	content := result.String()
	if len(content) > maxLength {
		content = content[:maxLength] + "\n\n[Content truncated...]"
	}

	L_debug("browser: snapshot complete", "contentLength", len(content))
	return content, nil
}

// screenshot captures the page as an image
func (t *BrowserTool) screenshot(ctx context.Context, sessionID, urlStr string, fullPage bool) (string, error) {
	page, err := t.pool.GetPage(sessionID)
	if err != nil {
		return "", err
	}

	// Navigate if URL provided
	if urlStr != "" {
		if err := t.navigateTo(page, urlStr); err != nil {
			return "", err
		}
	}

	info, err := page.Info()
	if err != nil {
		return "", fmt.Errorf("failed to get page info: %w", err)
	}

	L_debug("browser: taking screenshot", "url", info.URL, "fullPage", fullPage)

	var imgBytes []byte
	if fullPage {
		imgBytes, err = page.Screenshot(true, &proto.PageCaptureScreenshot{
			Format:      proto.PageCaptureScreenshotFormatPng,
			FromSurface: true,
		})
	} else {
		imgBytes, err = page.Screenshot(false, nil)
	}

	if err != nil {
		return "", fmt.Errorf("failed to take screenshot: %w", err)
	}

	// Save to media store with relative path
	_, relPath, err := t.mediaStore.Save(imgBytes, "browser", ".png")
	if err != nil {
		return "", fmt.Errorf("failed to save screenshot: %w", err)
	}

	L_debug("browser: screenshot saved", "relPath", relPath, "size", len(imgBytes))

	// Return with MEDIA: prefix for automatic channel delivery
	// The relative path format ./media/browser/xxx.png matches OpenClaw's security requirements
	return fmt.Sprintf("Screenshot saved: %s\nPage: %s\nTitle: %s\nMEDIA:%s", relPath, info.URL, info.Title, relPath), nil
}

// navigate goes to a URL and returns page state
func (t *BrowserTool) navigate(ctx context.Context, sessionID, urlStr string) (string, error) {
	if urlStr == "" {
		return "", fmt.Errorf("url is required for navigate action")
	}

	page, err := t.pool.GetPage(sessionID)
	if err != nil {
		return "", err
	}

	if err := t.navigateTo(page, urlStr); err != nil {
		return "", err
	}

	info, err := page.Info()
	if err != nil {
		return "", fmt.Errorf("failed to get page info: %w", err)
	}

	L_debug("browser: navigated", "url", info.URL, "title", info.Title)
	return fmt.Sprintf("Navigated to: %s\nTitle: %s", info.URL, info.Title), nil
}

// navigateTo handles navigation with timeout and waiting
func (t *BrowserTool) navigateTo(page *rod.Page, urlStr string) error {
	// Validate URL
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("URL must use http or https scheme")
	}

	L_debug("browser: navigating", "url", urlStr)

	// Navigate with timeout
	page = page.Timeout(t.timeout)
	if err := page.Navigate(urlStr); err != nil {
		return fmt.Errorf("navigation failed: %w", err)
	}

	// Wait for page to be stable (no network activity)
	if err := page.WaitStable(2 * time.Second); err != nil {
		L_warn("browser: page not stable after timeout", "url", urlStr)
		// Continue anyway - page might still be usable
	}

	return nil
}
