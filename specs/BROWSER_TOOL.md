# Browser Tool Spec

## Overview

Headless browser automation for pages that need JavaScript rendering or bypass Cloudflare/bot protection. Fallback when `web_fetch` fails.

## Library

**go-rod** (`github.com/go-rod/rod`) — Chromium DevTools protocol wrapper
- High-level API similar to Playwright
- Automatic browser download/management
- Stealth mode for bot detection bypass
- Screenshots, PDF, DOM extraction

Alternative: `chromedp` (lower level, more verbose)

## Tool Interface

```go
// internal/tools/browser.go
type BrowserTool struct {
    browser *rod.Browser
    timeout time.Duration
}

func (t *BrowserTool) Name() string { return "browser" }

func (t *BrowserTool) Schema() map[string]any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{
            "action": map[string]any{
                "type": "string",
                "enum": []string{"snapshot", "screenshot", "navigate", "click", "type"},
                "description": "Action to perform",
            },
            "url": map[string]any{
                "type": "string",
                "description": "URL to navigate to (for navigate/snapshot actions)",
            },
            "selector": map[string]any{
                "type": "string",
                "description": "CSS selector for click/type actions",
            },
            "text": map[string]any{
                "type": "string",
                "description": "Text to type (for type action)",
            },
            "fullPage": map[string]any{
                "type": "boolean",
                "description": "Full page screenshot (default: false)",
            },
        },
        "required": []string{"action"},
    }
}
```

## Actions

### `snapshot`
Extract readable text content from page (after JS renders).

```go
func (t *BrowserTool) snapshot(url string) (string, error) {
    page := t.browser.MustPage(url).MustWaitStable()
    
    // Get main content text
    text := page.MustElement("body").MustText()
    
    // Or use readability on rendered HTML
    html := page.MustHTML()
    article, _ := readability.FromReader(strings.NewReader(html), url)
    
    return article.TextContent, nil
}
```

### `screenshot`
Capture page as image, return path.

```go
func (t *BrowserTool) screenshot(url string, fullPage bool) (string, error) {
    page := t.browser.MustPage(url).MustWaitStable()
    
    imgBytes := page.MustScreenshot(fullPage)
    
    path := filepath.Join(mediaDir, uuid.New().String()+".png")
    os.WriteFile(path, imgBytes, 0644)
    
    return fmt.Sprintf("Screenshot saved: %s", path), nil
}
```

### `navigate`
Navigate to URL, return page state.

```go
func (t *BrowserTool) navigate(url string) (string, error) {
    page := t.browser.MustPage(url).MustWaitLoad()
    return fmt.Sprintf("Navigated to: %s\nTitle: %s", page.MustInfo().URL, page.MustInfo().Title), nil
}
```

### `click`
Click element by CSS selector.

```go
func (t *BrowserTool) click(selector string) (string, error) {
    t.currentPage.MustElement(selector).MustClick()
    t.currentPage.MustWaitStable()
    return fmt.Sprintf("Clicked: %s", selector), nil
}
```

### `type`
Type text into element.

```go
func (t *BrowserTool) typeText(selector, text string) (string, error) {
    t.currentPage.MustElement(selector).MustInput(text)
    return fmt.Sprintf("Typed into: %s", selector), nil
}
```

## Browser Lifecycle

```go
type BrowserPool struct {
    browser *rod.Browser
    mu      sync.Mutex
    pages   map[string]*rod.Page  // keyed by session or tab ID
}

func NewBrowserPool() *BrowserPool {
    // Launch browser with stealth options
    launcher := launcher.New().
        Headless(true).
        Set("disable-blink-features", "AutomationControlled")
    
    browser := rod.New().ControlURL(launcher.MustLaunch()).MustConnect()
    
    return &BrowserPool{browser: browser, pages: make(map[string]*rod.Page)}
}

func (p *BrowserPool) GetPage(id string) *rod.Page {
    p.mu.Lock()
    defer p.mu.Unlock()
    
    if page, ok := p.pages[id]; ok {
        return page
    }
    
    page := p.browser.MustPage("")
    p.pages[id] = page
    return page
}

func (p *BrowserPool) Close() {
    p.browser.MustClose()
}
```

## Stealth Mode

go-rod has stealth plugin to avoid bot detection:

```go
import "github.com/go-rod/stealth"

page := stealth.MustPage(browser)
```

This patches common detection vectors:
- `navigator.webdriver`
- Chrome DevTools protocol detection
- Headless user-agent strings
- WebGL vendor/renderer

## Integration with web_fetch

Two approaches:

**A) Separate tools** (current plan)
- `web_fetch` — fast HTTP fetch + readability
- `browser` — full browser when needed

Agent decides which to use based on experience (Cloudflare = use browser).

**B) Smart fallback in web_fetch**

```go
func (t *WebFetchTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
    // Try HTTP first
    resp, err := t.httpFetch(url)
    if err != nil || t.looksLikeCloudflare(resp) {
        L_debug("HTTP fetch failed, falling back to browser")
        return t.browserFetch(url)
    }
    return resp, nil
}
```

**Recommendation:** Start with separate tools (A). Simpler, more explicit. Agent learns when to use which. Can add smart fallback later.

## File Structure

```
internal/tools/
├── browser.go       # Browser tool implementation
├── browser_pool.go  # Browser lifecycle management
└── web_fetch.go     # HTTP fetch (existing)
```

## Dependencies

Add to `go.mod`:
```
github.com/go-rod/rod v0.116.0
github.com/go-rod/stealth v0.4.9
```

## MVP Scope

- [x] `snapshot` — extract text from JS-rendered page
- [x] `screenshot` — capture page image
- [x] `navigate` — go to URL
- [ ] `click` — click elements (post-MVP)
- [ ] `type` — fill forms (post-MVP)

MVP focuses on **reading pages**, not interacting. Interaction can come later.

## Security Considerations

- Browser runs in headless mode, no GUI exposure
- Sandboxed Chromium (default go-rod behavior)
- Timeout on all operations (prevent hangs)
- Memory limits on browser pool (don't leak pages)
- Owner-only tool (sensitive — can access any URL)
