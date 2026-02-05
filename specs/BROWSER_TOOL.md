# Browser Tool Spec

## Overview

Headless browser automation for pages that need JavaScript rendering or bypass Cloudflare/bot protection. Browser is **managed by GoClaw** — downloaded, updated, and profiled under our control.

## Philosophy

Browser is the **primary interface to the web**, not a fallback. The internet is Chrome. We own our Chrome.

## Library

**go-rod** (`github.com/go-rod/rod`) — Chromium DevTools protocol wrapper
- High-level API similar to Playwright
- Automatic browser download/management
- Stealth mode for bot detection bypass
- Screenshots, PDF, DOM extraction
- Cross-platform (Linux, macOS, Windows)

## Managed Browser

### Directory Structure

```
~/.openclaw/goclaw/
├── goclaw.json
├── sessions.db
├── cron.json
└── browser/                    # NEW: Managed browser
    ├── bin/                    # Chromium binary (auto-downloaded)
    │   └── chromium-1234567/   # Version-specific
    └── profiles/               # Persistent profiles
        ├── default/            # Default profile (cookies, sessions)
        └── twitter/            # Named profile for Twitter (example)
```

### Configuration

```json
{
  "browser": {
    "dir": "",                   // Empty = ~/.openclaw/goclaw/browser
    "autoDownload": true,        // Download Chromium if missing
    "revision": "",              // Empty = latest, or pin version
    "headless": true,            // Default mode
    "defaultProfile": "default", // Profile to use
    "timeout": "30s",            // Default action timeout
    "stealth": true              // Anti-detection features
  }
}
```

### Startup Initialization

```go
func (g *Gateway) initBrowser() error {
    cfg := g.config.Browser
    browserDir := cfg.Dir
    if browserDir == "" {
        browserDir = filepath.Join(g.dataDir, "browser")
    }
    
    // Ensure directories exist
    os.MkdirAll(filepath.Join(browserDir, "bin"), 0755)
    os.MkdirAll(filepath.Join(browserDir, "profiles", "default"), 0755)
    
    if cfg.AutoDownload {
        // Async download - don't block startup
        go g.ensureBrowser(browserDir)
    }
    
    return nil
}

func (g *Gateway) ensureBrowser(browserDir string) {
    b := launcher.NewBrowser()
    b.Dir = filepath.Join(browserDir, "bin")
    
    if cfg.Revision != "" {
        b.Revision = cfg.Revision
    }
    
    binPath, err := b.Download()
    if err != nil {
        L_warn("browser download failed", "err", err)
        return
    }
    
    g.browserBin = binPath
    L_info("browser ready", "path", binPath)
}
```

### Browser Launch with Profile

```go
func (g *Gateway) launchBrowser(profile string, headless bool) (*rod.Browser, error) {
    if g.browserBin == "" {
        return nil, fmt.Errorf("browser not downloaded yet")
    }
    
    profileDir := filepath.Join(g.browserDir, "profiles", profile)
    
    l := launcher.New().
        Bin(g.browserBin).
        UserDataDir(profileDir).
        Headless(headless)
    
    if g.config.Browser.Stealth {
        l.Set("disable-blink-features", "AutomationControlled")
    }
    
    u, err := l.Launch()
    if err != nil {
        return nil, err
    }
    
    return rod.New().ControlURL(u).Connect()
}
```

## CLI Commands

### `goclaw browser download`

Force download/update browser binary.

```bash
$ goclaw browser download
Downloading Chromium r1234567...
Progress: 100% (156 MB)
Browser ready: ~/.openclaw/goclaw/browser/bin/chromium-1234567/chrome
```

### `goclaw browser setup [profile]`

Interactive session to authenticate sites. Opens browser **non-headless**.

```bash
$ goclaw browser setup twitter
Launching browser (non-headless)...
Sign into any sites you need, then press Enter to save profile.
[Browser window opens at https://twitter.com/login]

> [User signs in manually]
> [User presses Enter]

Profile 'twitter' saved to ~/.openclaw/goclaw/browser/profiles/twitter/
```

```go
func cmdBrowserSetup(profile string) error {
    if profile == "" {
        profile = "default"
    }
    
    browser, err := g.launchBrowser(profile, false) // NON-headless
    if err != nil {
        return err
    }
    defer browser.Close()
    
    // Open a useful starting page
    page := browser.MustPage("https://x.com/login")
    
    fmt.Println("Sign into any sites you need, then press Enter to save profile.")
    bufio.NewReader(os.Stdin).ReadBytes('\n')
    
    // Profile auto-saves to profileDir on browser close
    fmt.Printf("Profile '%s' saved.\n", profile)
    return nil
}
```

### `goclaw browser profiles`

List available profiles.

```bash
$ goclaw browser profiles
NAME      SIZE      LAST USED
default   12 MB     2026-02-04 15:30
twitter   8 MB      2026-02-04 14:22
```

### `goclaw browser clear [profile]`

Clear cookies/session for a profile.

```bash
$ goclaw browser clear twitter
Cleared profile 'twitter'
```

## Tool Interface

```go
type BrowserInput struct {
    Action   string `json:"action"`   // navigate, snapshot, screenshot, click, type, scroll, wait
    URL      string `json:"url"`
    Selector string `json:"selector"`
    Text     string `json:"text"`
    FullPage bool   `json:"fullPage"`
    Profile  string `json:"profile"`  // Which profile to use
}
```

## Actions

### `navigate`
Navigate to URL, return page info with element index.

```go
func (t *BrowserTool) navigate(url string) (string, error) {
    page := t.getPage()
    page.Navigate(url)
    page.WaitStable()
    
    info := page.MustInfo()
    elements := t.indexElements(page)
    
    return fmt.Sprintf("URL: %s\nTitle: %s\n\nElements:\n%s", 
        info.URL, info.Title, elements), nil
}
```

### `snapshot`
Extract readable text/markdown from page.

```go
func (t *BrowserTool) snapshot(url string, maxLength int) (string, error) {
    page := t.getPage()
    if url != "" {
        page.Navigate(url)
    }
    page.WaitStable()
    
    // Get rendered HTML, convert to markdown
    html := page.MustHTML()
    markdown := htmlToMarkdown(html)
    
    if maxLength > 0 && len(markdown) > maxLength {
        markdown = markdown[:maxLength] + "...[truncated]"
    }
    
    return markdown, nil
}
```

### `screenshot`
Capture page as image.

```go
func (t *BrowserTool) screenshot(fullPage bool) (string, error) {
    page := t.getPage()
    
    var imgBytes []byte
    if fullPage {
        imgBytes = page.MustScreenshotFullPage()
    } else {
        imgBytes = page.MustScreenshot()
    }
    
    filename := fmt.Sprintf("browser_%s.png", uuid.New().String()[:8])
    path := filepath.Join(mediaRoot, "browser", filename)
    os.WriteFile(path, imgBytes, 0644)
    
    return fmt.Sprintf("Screenshot saved: %s", path), nil
}
```

### `click`
Click element by index or selector.

```go
func (t *BrowserTool) click(selector string) (string, error) {
    page := t.getPage()
    
    // Support [index] syntax from element list
    if strings.HasPrefix(selector, "[") {
        idx := parseIndex(selector)
        el := t.elementsByIndex[idx]
        el.MustClick()
    } else {
        page.MustElement(selector).MustClick()
    }
    
    page.WaitStable()
    return fmt.Sprintf("Clicked: %s", selector), nil
}
```

### `type`
Type text into element.

```go
func (t *BrowserTool) typeText(selector, text string) (string, error) {
    page := t.getPage()
    page.MustElement(selector).MustInput(text)
    return fmt.Sprintf("Typed into %s: %s", selector, text), nil
}
```

### `scroll`
Scroll page up/down or to element.

```go
func (t *BrowserTool) scroll(direction string) (string, error) {
    page := t.getPage()
    
    switch direction {
    case "down":
        page.Mouse.Scroll(0, 500, 1)
    case "up":
        page.Mouse.Scroll(0, -500, 1)
    case "bottom":
        page.MustEval(`window.scrollTo(0, document.body.scrollHeight)`)
    case "top":
        page.MustEval(`window.scrollTo(0, 0)`)
    }
    
    return fmt.Sprintf("Scrolled: %s", direction), nil
}
```

### `wait`
Wait for element or condition.

```go
func (t *BrowserTool) wait(selector string, timeout time.Duration) (string, error) {
    page := t.getPage()
    
    err := page.Timeout(timeout).Element(selector).WaitVisible()
    if err != nil {
        return "", fmt.Errorf("timeout waiting for: %s", selector)
    }
    
    return fmt.Sprintf("Element visible: %s", selector), nil
}
```

## Element Indexing

Snapshots include indexed elements for easy reference:

```
URL: https://x.com/home
Title: Home / X

Elements:
[1] input "Search"
[2] button "Post"
[3] a "Home"
[4] a "Explore"
[5] a "Notifications"
[6] a "Messages"
...

Content:
[Main timeline content here...]
```

```go
func (t *BrowserTool) indexElements(page *rod.Page) string {
    var lines []string
    
    elements := page.MustElements("a, button, input, select, textarea")
    for i, el := range elements {
        tag := el.MustProperty("tagName").String()
        text := truncate(el.MustText(), 50)
        
        // Get identifying attribute
        id := el.MustAttribute("id")
        name := el.MustAttribute("name")
        placeholder := el.MustAttribute("placeholder")
        
        label := text
        if label == "" {
            label = placeholder
        }
        if label == "" {
            label = name
        }
        
        t.elementsByIndex[i+1] = el
        lines = append(lines, fmt.Sprintf("[%d] %s \"%s\"", i+1, strings.ToLower(tag), label))
    }
    
    return strings.Join(lines, "\n")
}
```

## Stealth Mode

Anti-detection features via go-rod/stealth:

```go
import "github.com/go-rod/stealth"

func (t *BrowserTool) getStealthPage() *rod.Page {
    return stealth.MustPage(t.browser)
}
```

Patches:
- `navigator.webdriver` → false
- Chrome DevTools protocol detection
- Headless user-agent normalization
- WebGL vendor/renderer spoofing

## Browser Pool

Persistent browser instance, reused across tool calls:

```go
type BrowserPool struct {
    browser    *rod.Browser
    pages      map[string]*rod.Page  // session ID → page
    mu         sync.Mutex
    browserBin string
    profileDir string
}

func (p *BrowserPool) GetPage(sessionID, profile string) (*rod.Page, error) {
    p.mu.Lock()
    defer p.mu.Unlock()
    
    // Reuse existing page for this session
    if page, ok := p.pages[sessionID]; ok {
        return page, nil
    }
    
    // Create new page
    page := p.browser.MustPage("")
    p.pages[sessionID] = page
    return page, nil
}

func (p *BrowserPool) ClosePage(sessionID string) {
    p.mu.Lock()
    defer p.mu.Unlock()
    
    if page, ok := p.pages[sessionID]; ok {
        page.Close()
        delete(p.pages, sessionID)
    }
}
```

## Error Handling

Structured errors for better agent feedback:

```go
type BrowserError struct {
    Action  string
    URL     string
    Reason  string
    Timeout bool
    Code    int  // HTTP status if relevant
}

func (e BrowserError) Error() string {
    if e.Timeout {
        return fmt.Sprintf("browser timeout: %s on %s", e.Action, e.URL)
    }
    return fmt.Sprintf("browser error: %s - %s", e.Action, e.Reason)
}
```

## Retry Logic

Automatic retry with backoff:

```go
func (t *BrowserTool) withRetry(fn func() error) error {
    backoff := []time.Duration{1*time.Second, 2*time.Second, 5*time.Second}
    
    var lastErr error
    for i, delay := range backoff {
        if err := fn(); err == nil {
            return nil
        } else {
            lastErr = err
            L_debug("browser retry", "attempt", i+1, "err", err)
            time.Sleep(delay)
        }
    }
    
    return lastErr
}
```

## Dependencies

```go
require (
    github.com/go-rod/rod v0.116.0
    github.com/go-rod/stealth v0.4.9
)
```

## Implementation Phases

### Phase 1: Managed Browser (Priority)
- [ ] Browser download on `goclaw init` or first use
- [ ] Profile directory structure
- [ ] `goclaw browser download` command
- [ ] `goclaw browser setup` command (interactive auth)
- [ ] Use managed browser in existing tool

### Phase 2: Enhanced Actions
- [ ] Element indexing in snapshots
- [ ] `click` action with index support
- [ ] `type` action
- [ ] `scroll` action
- [ ] `wait` action

### Phase 3: Browser-Powered web_fetch
- [ ] Auto-fallback: web_fetch retries with browser on 403/bot-detection
- [ ] HTML-to-markdown extraction from rendered DOM
- [ ] Domain-to-profile mapping for authenticated fetches
- [ ] Configurable: `tools.web.useBrowser: "auto"|"always"|"never"`
- [ ] Metrics: track when browser fallback was needed

### Future: Polish & Extensions
- [ ] Chrome extension relay for native browser integration
- [ ] Stealth mode improvements
- [ ] Retry logic with exponential backoff
- [ ] Memory limits on browser pool
- [ ] Performance metrics

## Security Considerations

- Browser runs in headless mode by default
- Sandboxed Chromium (go-rod default)
- Timeout on all operations (prevent hangs)
- Memory limits on browser pool
- Profiles stored in user directory, not system
- Owner-only tool (can access any URL, stored credentials)
