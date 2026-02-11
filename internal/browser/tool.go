package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-shiori/go-readability"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
)

// Tool provides comprehensive browser automation
type Tool struct {
	manager    *Manager
	mediaStore *media.MediaStore
	timeout    time.Duration

	// Per-session tab tracking
	sessions   map[string]*SessionTabs
	sessionsMu sync.Mutex
}

// SessionTabs tracks tabs for a session
type SessionTabs struct {
	Profile   string
	Headed    bool         // Whether this session uses headed (visible) browser
	Browser   *rod.Browser // Dedicated browser instance for headed sessions
	Tabs      []*TabInfo
	ActiveTab int // Index of active tab
	Closed    bool // Set to true when browser disconnects
	mu        sync.Mutex
}

// TabInfo contains information about a browser tab
type TabInfo struct {
	Index    int                `json:"index"`
	URL      string             `json:"url"`
	Title    string             `json:"title"`
	Page     *rod.Page          `json:"-"`
	Elements map[int]*rod.Element `json:"-"` // Indexed elements for click/type by ref
}

// NewTool creates a new browser tool
func NewTool(manager *Manager, mediaStore *media.MediaStore) *Tool {
	return &Tool{
		manager:    manager,
		mediaStore: mediaStore,
		timeout:    60 * time.Second,
		sessions:   make(map[string]*SessionTabs),
	}
}

func (t *Tool) Name() string {
	return "browser"
}

func (t *Tool) Description() string {
	return `Browser automation for JavaScript-rendered pages or sites with bot protection.

Chrome Extension: If user mentions Chrome extension, Browser Relay, toolbar button, or "attach tab", use profile="chrome" to connect to their existing Chrome tabs. Requires user to click the toolbar icon to attach.

Tab Actions:
- tabs: List all open tabs
- open: Open a new tab (optionally with URL)
- focus: Switch to tab by index
- close: Close a tab (default: current)

Navigation:
- navigate: Go to URL in current tab
- snapshot: Extract readable text (formats: text, ai, aria)
- screenshot: Capture page as image
- pdf: Save page as PDF

Interaction (use ref from snapshot, e.g. "e1", "e12"):
- click: Click element by ref or selector
- type: Type text into element
- press: Press keyboard key(s)
- hover: Hover over element
- scroll: Scroll page or to element
- select: Select option in dropdown
- fill: Fill form field (clears first)
- wait: Wait for element or condition
- evaluate: Run JavaScript code

Advanced:
- console: Get browser console logs
- upload: Upload file to input element
- dialog: Handle alert/confirm/prompt dialogs

Use for interactive automation or when web_fetch doesn't return expected content.`
}

func (t *Tool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"tabs", "open", "focus", "close", "navigate", "snapshot", "screenshot", "pdf", "click", "type", "press", "hover", "scroll", "select", "fill", "wait", "evaluate", "console", "upload", "dialog"},
				"description": "Action to perform",
			},
			"url": map[string]interface{}{
				"type":        "string",
				"description": "URL for navigate/open actions",
			},
			"tabIndex": map[string]interface{}{
				"type":        "integer",
				"description": "Tab index for focus/close actions (0-based)",
			},
			"fullPage": map[string]interface{}{
				"type":        "boolean",
				"description": "Capture full page screenshot (default: false)",
			},
			"maxLength": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum text length for snapshot (default: 15000)",
			},
			"profile": map[string]interface{}{
				"type":        "string",
				"description": "Only use 'chrome' for Chrome extension relay (user's existing tabs). Omit for normal use.",
			},
			"format": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"text", "ai", "aria"},
				"description": "Snapshot format: 'text' (readable content), 'ai' (with numbered elements), 'aria' (accessibility tree). Default: 'ai'",
			},
			"ref": map[string]interface{}{
				"type":        "integer",
				"description": "Element reference number from snapshot (for click/type/hover/fill/select)",
			},
			"selector": map[string]interface{}{
				"type":        "string",
				"description": "CSS selector (alternative to ref for click/type/hover/fill/select/wait)",
			},
			"text": map[string]interface{}{
				"type":        "string",
				"description": "Text to type (for type/fill actions)",
			},
			"key": map[string]interface{}{
				"type":        "string",
				"description": "Key to press (for press action): Enter, Tab, Escape, ArrowDown, etc.",
			},
			"direction": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"up", "down", "top", "bottom"},
				"description": "Scroll direction (for scroll action)",
			},
			"value": map[string]interface{}{
				"type":        "string",
				"description": "Value to select (for select action)",
			},
			"timeout": map[string]interface{}{
				"type":        "integer",
				"description": "Timeout in seconds (for wait action, default: 30)",
			},
			"code": map[string]interface{}{
				"type":        "string",
				"description": "JavaScript code to execute (for evaluate action)",
			},
			"file": map[string]interface{}{
				"type":        "string",
				"description": "File path to upload (for upload action)",
			},
			"dialogAction": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"accept", "dismiss"},
				"description": "How to handle dialog: accept or dismiss (for dialog action)",
			},
			"dialogText": map[string]interface{}{
				"type":        "string",
				"description": "Text to enter in prompt dialog (for dialog action with accept)",
			},
			"headed": map[string]interface{}{
				"type":        "boolean",
				"description": "Run browser in headed (visible) mode for debugging (default: false/headless)",
			},
		},
		"required": []string{"action"},
	}
}

// Execute runs the browser action
func (t *Tool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Action       string `json:"action"`
		URL          string `json:"url"`
		TabIndex     *int   `json:"tabIndex"`
		FullPage     bool   `json:"fullPage"`
		MaxLength    int    `json:"maxLength"`
		Profile      string `json:"profile"`
		Format       string `json:"format"`
		Ref          *int   `json:"ref"`
		Selector     string `json:"selector"`
		Text         string `json:"text"`
		Key          string `json:"key"`
		Direction    string `json:"direction"`
		Value        string `json:"value"`
		Timeout      int    `json:"timeout"`
		Code         string `json:"code"`
		File         string `json:"file"`
		DialogAction string `json:"dialogAction"`
		DialogText   string `json:"dialogText"`
		Headed       bool   `json:"headed"`
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

	if params.Format == "" {
		params.Format = "ai" // Default to AI-friendly format with element refs
	}

	if params.Timeout <= 0 {
		params.Timeout = 30
	}

	// Profile selection precedence:
	// 1. "chrome" always honored (extension relay)
	// 2. Explicit profile honored if allowAgentProfiles is true
	// 3. Otherwise, use profileDomains based on URL (profile ignored with note)
	var ignoredProfile string
	config := t.manager.Config()
	
	if params.Profile == "chrome" {
		// Always honor "chrome" for extension relay
		L_debug("browser: using chrome extension relay")
	} else if params.Profile != "" && config.AllowAgentProfiles {
		// Agent explicitly requested and allowed - honor it
		L_debug("browser: using agent-specified profile", "profile", params.Profile)
	} else if params.Profile != "" {
		// Agent requested but not allowed - ignore with note
		ignoredProfile = params.Profile
		L_warn("browser: profile ignored (allowAgentProfiles=false), using config-driven selection", "requested", params.Profile)
		params.Profile = ""
	}
	// If profile is empty, profileDomains will be used in getOrCreateSession

	// Use default session ID (could be passed from agent context later)
	sessionID := "default"

	L_debug("browser: executing", "action", params.Action, "url", params.URL, "profile", params.Profile, "format", params.Format, "headed", params.Headed)

	// Execute action and potentially append profile note
	result, err := t.executeAction(ctx, sessionID, params)
	
	// If we ignored a profile request, append helpful note to successful results
	if err == nil && ignoredProfile != "" {
		profileNote := fmt.Sprintf("\n\n---\nNote: Requested profile '%s' was ignored. GoClaw uses config-driven profile selection based on URL domain. To allow explicit profiles, set allowAgentProfiles: true in goclaw.json. If authentication fails, run: goclaw browser setup <profile>", ignoredProfile)
		result = result + profileNote
	}
	
	return result, err
}

// executeAction dispatches to the appropriate action handler
func (t *Tool) executeAction(ctx context.Context, sessionID string, params struct {
	Action       string `json:"action"`
	URL          string `json:"url"`
	TabIndex     *int   `json:"tabIndex"`
	FullPage     bool   `json:"fullPage"`
	MaxLength    int    `json:"maxLength"`
	Profile      string `json:"profile"`
	Format       string `json:"format"`
	Ref          *int   `json:"ref"`
	Selector     string `json:"selector"`
	Text         string `json:"text"`
	Key          string `json:"key"`
	Direction    string `json:"direction"`
	Value        string `json:"value"`
	Timeout      int    `json:"timeout"`
	Code         string `json:"code"`
	File         string `json:"file"`
	DialogAction string `json:"dialogAction"`
	DialogText   string `json:"dialogText"`
	Headed       bool   `json:"headed"`
}) (string, error) {
	switch params.Action {
	// Tab management
	case "tabs":
		return t.listTabs(ctx, sessionID, params.Headed)
	case "open":
		return t.openTab(ctx, sessionID, params.URL, params.Profile, params.Headed)
	case "focus":
		if params.TabIndex == nil {
			return "", fmt.Errorf("tabIndex is required for focus action")
		}
		return t.focusTab(ctx, sessionID, *params.TabIndex, params.Headed)
	case "close":
		return t.closeTab(ctx, sessionID, params.TabIndex, params.Headed)

	// Navigation
	case "navigate":
		return t.navigate(ctx, sessionID, params.URL, params.Headed)
	case "snapshot":
		return t.snapshot(ctx, sessionID, params.URL, params.MaxLength, params.Format, params.Headed)
	case "screenshot":
		return t.screenshot(ctx, sessionID, params.URL, params.FullPage, params.Ref, params.Headed)
	case "pdf":
		return t.savePDF(ctx, sessionID, params.Headed)

	// Interaction
	case "click":
		return t.click(ctx, sessionID, params.Ref, params.Selector, params.Headed)
	case "type":
		return t.typeText(ctx, sessionID, params.Ref, params.Selector, params.Text, params.Headed)
	case "press":
		return t.pressKey(ctx, sessionID, params.Key, params.Headed)
	case "hover":
		return t.hover(ctx, sessionID, params.Ref, params.Selector, params.Headed)
	case "scroll":
		return t.scroll(ctx, sessionID, params.Direction, params.Ref, params.Selector, params.Headed)
	case "select":
		return t.selectOption(ctx, sessionID, params.Ref, params.Selector, params.Value, params.Headed)
	case "fill":
		return t.fill(ctx, sessionID, params.Ref, params.Selector, params.Text, params.Headed)
	case "wait":
		return t.wait(ctx, sessionID, params.Selector, time.Duration(params.Timeout)*time.Second, params.Headed)
	case "evaluate":
		return t.evaluate(ctx, sessionID, params.Code, params.Headed)

	// Advanced
	case "console":
		return t.getConsole(ctx, sessionID, params.Headed)
	case "upload":
		return t.uploadFile(ctx, sessionID, params.Ref, params.Selector, params.File, params.Headed)
	case "dialog":
		return t.handleDialog(ctx, sessionID, params.DialogAction, params.DialogText, params.Headed)

	default:
		return "", fmt.Errorf("unknown action: %s", params.Action)
	}
}

// getOrCreateSession gets or creates a session with tabs
func (t *Tool) getOrCreateSession(sessionID string, profile string, headed bool) (*SessionTabs, error) {
	t.sessionsMu.Lock()
	defer t.sessionsMu.Unlock()

	// Determine actual session key
	actualSessionID := sessionID
	if headed {
		// Check if we need a separate headed session
		if session, ok := t.sessions[sessionID]; ok && !session.Headed {
			actualSessionID = sessionID + "-headed"
		}
	}

	// Check for existing session
	if session, ok := t.sessions[actualSessionID]; ok {
		// For headed sessions, verify browser is still connected
		if session.Headed {
			// Quick check: was browser marked as closed by event monitor?
			session.mu.Lock()
			closed := session.Closed
			session.mu.Unlock()

			if closed {
				L_debug("browser: session marked as closed, recreating", "sessionID", actualSessionID)
				t.cleanupSessionLocked(actualSessionID, session)
				// Fall through to create new session
			} else if session.Browser != nil {
				// Verify with actual call
				if _, err := session.Browser.Version(); err != nil {
					L_warn("browser: headed browser disconnected, recreating", "sessionID", actualSessionID, "error", err)
					t.cleanupSessionLocked(actualSessionID, session)
					// Fall through to create new session
				} else {
					return session, nil
				}
			} else {
				return session, nil
			}
		} else {
			return session, nil
		}
	}

	if profile == "" {
		profile = t.manager.Config().DefaultProfile
	}

	session := &SessionTabs{
		Profile:   profile,
		Headed:    headed,
		Tabs:      make([]*TabInfo, 0),
		ActiveTab: -1,
		Closed:    false,
	}

	// If headed mode, get/create a headed browser instance
	if headed {
		browser, err := t.manager.GetBrowser(profile, true)
		if err != nil {
			return nil, fmt.Errorf("failed to get headed browser: %w", err)
		}
		session.Browser = browser
		L_info("browser: using headed browser", "profile", profile, "sessionID", actualSessionID)

		// Monitor browser events for close detection (tab-level events)
		go t.monitorBrowserEvents(actualSessionID, session, browser)

		// Sync tabs with actual browser state (picks up initial about:blank)
		t.sessions[actualSessionID] = session // Store first so syncTabs can find it
		if err := t.syncTabs(session); err != nil {
			L_warn("browser: initial tab sync failed", "error", err)
		}
		return session, nil
	}

	t.sessions[actualSessionID] = session
	return session, nil
}

// monitorBrowserEvents watches for browser disconnect events
// Note: We don't mark session closed when windows close - we can reopen pages on the same browser
func (t *Tool) monitorBrowserEvents(sessionID string, session *SessionTabs, browser *rod.Browser) {
	L_debug("browser: starting event monitor", "sessionID", sessionID)

	// Monitor for target destroyed events (tab/page closed) - just for logging
	go browser.EachEvent(func(e *proto.TargetTargetDestroyed) {
		L_debug("browser: target destroyed", "sessionID", sessionID, "targetID", e.TargetID)
	})()

	// Monitor for target crashed events
	go browser.EachEvent(func(e *proto.TargetTargetCrashed) {
		L_warn("browser: target crashed", "sessionID", sessionID, "targetID", e.TargetID)
	})()

	// Wait for browser process to die (CDP connection lost)
	// Note: We don't mark closed when windows close - browser can reopen pages
	ctx := browser.GetContext()
	<-ctx.Done()

	L_info("browser: process disconnected", "sessionID", sessionID, "reason", ctx.Err())

	// Mark session as closed only when browser process actually dies
	session.mu.Lock()
	session.Closed = true
	session.mu.Unlock()
}

// cleanupSessionLocked cleans up a session's resources (must hold sessionsMu)
func (t *Tool) cleanupSessionLocked(sessionID string, session *SessionTabs) {
	if session == nil {
		return
	}
	session.mu.Lock()
	session.Closed = true
	for _, tab := range session.Tabs {
		if tab.Page != nil {
			tab.Page.Close()
		}
	}
	if session.Browser != nil {
		session.Browser.Close()
	}
	session.mu.Unlock()
	delete(t.sessions, sessionID)
}

// syncTabs reconciles session.Tabs with the actual browser state.
// This ensures our internal tracking matches reality, handling:
// - Initial about:blank tabs from browser launch
// - Tabs opened/closed externally by user
// - State drift from any source
// Must be called with session.mu NOT held (will acquire it).
func (t *Tool) syncTabs(session *SessionTabs) error {
	if session == nil {
		return nil
	}

	// For headless sessions without dedicated browser, skip sync
	if !session.Headed || session.Browser == nil {
		return nil
	}

	// Get actual pages from browser
	pages, err := session.Browser.Pages()
	if err != nil {
		return fmt.Errorf("failed to enumerate browser pages: %w", err)
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	// Build map of existing tracked pages by target ID for matching
	existingByTarget := make(map[string]*TabInfo)
	for _, tab := range session.Tabs {
		if tab.Page != nil {
			if target := tab.Page.TargetID; target != "" {
				existingByTarget[string(target)] = tab
			}
		}
	}

	// Rebuild Tabs array from actual pages
	newTabs := make([]*TabInfo, 0, len(pages))
	var activePageTarget string
	if session.ActiveTab >= 0 && session.ActiveTab < len(session.Tabs) {
		if tab := session.Tabs[session.ActiveTab]; tab.Page != nil {
			activePageTarget = string(tab.Page.TargetID)
		}
	}

	newActiveTab := -1
	for i, page := range pages {
		var tab *TabInfo

		// Try to find existing tab info for this page
		targetID := string(page.TargetID)
		if existing, ok := existingByTarget[targetID]; ok {
			tab = existing
			tab.Index = i
		} else {
			// New page we weren't tracking
			tab = &TabInfo{
				Index:    i,
				Page:     page,
				Elements: make(map[int]*rod.Element),
			}
		}

		// Refresh URL and title
		if info, err := page.Info(); err == nil {
			tab.URL = info.URL
			tab.Title = info.Title
		}

		newTabs = append(newTabs, tab)

		// Track active tab by matching target ID
		if targetID == activePageTarget {
			newActiveTab = i
		}
	}

	oldCount := len(session.Tabs)
	session.Tabs = newTabs

	// Clamp ActiveTab to valid range
	if newActiveTab >= 0 {
		session.ActiveTab = newActiveTab
	} else if len(session.Tabs) > 0 {
		// Previous active tab is gone, default to last tab
		session.ActiveTab = len(session.Tabs) - 1
	} else {
		session.ActiveTab = -1
	}

	if oldCount != len(newTabs) {
		L_debug("browser: syncTabs reconciled",
			"oldCount", oldCount,
			"newCount", len(newTabs),
			"activeTab", session.ActiveTab)
	}

	return nil
}

// isContextCancelledError checks if an error is due to browser disconnect
func isContextCancelledError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "context canceled") ||
		strings.Contains(errStr, "context cancelled") ||
		strings.Contains(errStr, "use of closed network connection") ||
		strings.Contains(errStr, "connection refused")
}

// markSessionClosed marks a session as closed for recovery on next use
func (t *Tool) markSessionClosed(sessionID string, headed bool) {
	actualSessionID := sessionID
	if headed {
		actualSessionID = sessionID + "-headed"
	}

	t.sessionsMu.Lock()
	defer t.sessionsMu.Unlock()

	if session, ok := t.sessions[actualSessionID]; ok {
		session.mu.Lock()
		session.Closed = true
		session.mu.Unlock()
		L_debug("browser: marked session as closed for recovery", "sessionID", actualSessionID)
	}
}

// getActivePage gets the active page for a session, creating one if needed
func (t *Tool) getActivePage(sessionID string, profile string, headed bool) (*rod.Page, error) {
	session, err := t.getOrCreateSession(sessionID, profile, headed)
	if err != nil {
		return nil, err
	}

	// Sync with actual browser state before accessing active page
	if err := t.syncTabs(session); err != nil {
		L_debug("browser: getActivePage sync failed", "error", err)
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	// If we have an active tab, verify it's still valid
	if session.ActiveTab >= 0 && session.ActiveTab < len(session.Tabs) {
		tab := session.Tabs[session.ActiveTab]
		if tab.Page != nil {
			// Quick check: try to get page info to verify it's alive
			if _, err := tab.Page.Info(); err == nil {
				return tab.Page, nil
			}
			// Page is dead, log and fall through to create new one
			L_debug("browser: active page is dead, will create new one", "sessionID", sessionID, "error", err)
		}
	}

	// No valid page, create one (this handles both "no tabs" and "dead page" cases)
	var page *rod.Page
	if session.Headed && session.Browser != nil {
		// For headed sessions, verify browser is still alive first
		if _, err := session.Browser.Version(); err != nil {
			return nil, fmt.Errorf("browser disconnected: %w", err)
		}
		page, err = session.Browser.Page(proto.TargetCreateTarget{})
		if err == nil {
			L_debug("browser: created new page on existing browser", "sessionID", sessionID)
		}
	} else {
		page, err = t.manager.GetStealthPage(session.Profile, session.Headed)
	}
	if err != nil {
		return nil, err
	}

	// If we had dead tabs, clear them and start fresh
	if len(session.Tabs) > 0 {
		L_debug("browser: clearing dead tabs", "sessionID", sessionID, "count", len(session.Tabs))
		session.Tabs = make([]*TabInfo, 0)
	}

	tab := &TabInfo{
		Index: 0,
		URL:   "about:blank",
		Title: "New Tab",
		Page:  page,
	}
	session.Tabs = append(session.Tabs, tab)
	session.ActiveTab = 0

	return page, nil
}

// listTabs returns information about all open tabs
func (t *Tool) listTabs(ctx context.Context, sessionID string, headed bool) (string, error) {
	session, err := t.getOrCreateSession(sessionID, "", headed)
	if err != nil {
		return "", err
	}

	// Sync with actual browser state before listing
	if err := t.syncTabs(session); err != nil {
		L_debug("browser: listTabs sync failed", "error", err)
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	if len(session.Tabs) == 0 {
		return "No tabs open. Use action 'open' to create a tab.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Open tabs (%d):\n\n", len(session.Tabs)))

	for i, tab := range session.Tabs {
		// Refresh tab info
		if tab.Page != nil {
			if info, err := tab.Page.Info(); err == nil {
				tab.URL = info.URL
				tab.Title = info.Title
			}
		}

		marker := "  "
		if i == session.ActiveTab {
			marker = "→ "
		}
		sb.WriteString(fmt.Sprintf("%s[%d] %s\n    %s\n", marker, i, tab.Title, tab.URL))
	}

	return sb.String(), nil
}

// openTab opens a new tab
func (t *Tool) openTab(ctx context.Context, sessionID string, urlStr string, profile string, headed bool) (string, error) {
	session, err := t.getOrCreateSession(sessionID, profile, headed)
	if err != nil {
		return "", err
	}

	// Sync with actual browser state before opening
	if err := t.syncTabs(session); err != nil {
		L_debug("browser: openTab sync failed", "error", err)
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	// Optimization: If there's exactly one about:blank tab and URL is provided,
	// reuse it instead of creating a new tab (mimics normal browser behavior)
	if urlStr != "" && len(session.Tabs) == 1 {
		tab := session.Tabs[0]
		if tab.URL == "about:blank" && tab.Page != nil {
			L_debug("browser: reusing about:blank tab for navigation", "url", urlStr)
			if err := t.navigateToURL(tab.Page, urlStr); err != nil {
				return "", err
			}
			if info, err := tab.Page.Info(); err == nil {
				tab.URL = info.URL
				tab.Title = info.Title
			}
			session.ActiveTab = 0
			mode := "headless"
			if session.Headed {
				mode = "headed"
			}
			return fmt.Sprintf("Navigated in tab [0]: %s\n%s\n[%s mode]", tab.Title, tab.URL, mode), nil
		}
	}

	// Create new page - use session's browser if headed, otherwise use manager's pool
	var page *rod.Page
	if session.Headed && session.Browser != nil {
		page, err = session.Browser.Page(proto.TargetCreateTarget{})
	} else {
		page, err = t.manager.GetStealthPage(session.Profile, session.Headed)
	}
	if err != nil {
		return "", fmt.Errorf("failed to create tab: %w", err)
	}

	newIndex := len(session.Tabs)
	tab := &TabInfo{
		Index: newIndex,
		URL:   "about:blank",
		Title: "New Tab",
		Page:  page,
	}

	// Navigate if URL provided
	if urlStr != "" {
		if err := t.navigateToURL(page, urlStr); err != nil {
			page.Close()
			return "", err
		}
		if info, err := page.Info(); err == nil {
			tab.URL = info.URL
			tab.Title = info.Title
		}
	}

	session.Tabs = append(session.Tabs, tab)
	session.ActiveTab = newIndex

	mode := "headless"
	if session.Headed {
		mode = "headed"
	}
	L_debug("browser: opened tab", "index", newIndex, "url", tab.URL, "mode", mode)
	return fmt.Sprintf("Opened tab [%d]: %s\n%s\n[%s mode]", newIndex, tab.Title, tab.URL, mode), nil
}

// focusTab switches to a tab by index
func (t *Tool) focusTab(ctx context.Context, sessionID string, index int, headed bool) (string, error) {
	session, err := t.getOrCreateSession(sessionID, "", headed)
	if err != nil {
		return "", err
	}

	// Sync with actual browser state before focusing
	if err := t.syncTabs(session); err != nil {
		L_debug("browser: focusTab sync failed", "error", err)
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	if index < 0 || index >= len(session.Tabs) {
		return "", fmt.Errorf("invalid tab index: %d (have %d tabs)", index, len(session.Tabs))
	}

	session.ActiveTab = index
	tab := session.Tabs[index]

	// Refresh tab info
	if info, err := tab.Page.Info(); err == nil {
		tab.URL = info.URL
		tab.Title = info.Title
	}

	L_debug("browser: focused tab", "index", index, "url", tab.URL)
	return fmt.Sprintf("Focused tab [%d]: %s\n%s", index, tab.Title, tab.URL), nil
}

// closeTab closes a tab
func (t *Tool) closeTab(ctx context.Context, sessionID string, index *int, headed bool) (string, error) {
	session, err := t.getOrCreateSession(sessionID, "", headed)
	if err != nil {
		return "", err
	}

	// Sync with actual browser state before closing
	if err := t.syncTabs(session); err != nil {
		L_debug("browser: closeTab sync failed", "error", err)
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	if len(session.Tabs) == 0 {
		return "No tabs to close.", nil
	}

	closeIndex := session.ActiveTab
	if index != nil {
		closeIndex = *index
	}

	if closeIndex < 0 || closeIndex >= len(session.Tabs) {
		return "", fmt.Errorf("invalid tab index: %d", closeIndex)
	}

	tab := session.Tabs[closeIndex]
	title := tab.Title

	// Close the page
	if tab.Page != nil {
		tab.Page.Close()
	}

	// Remove from slice
	session.Tabs = append(session.Tabs[:closeIndex], session.Tabs[closeIndex+1:]...)

	// Re-index remaining tabs
	for i := range session.Tabs {
		session.Tabs[i].Index = i
	}

	// Adjust active tab
	if len(session.Tabs) == 0 {
		session.ActiveTab = -1
	} else if session.ActiveTab >= len(session.Tabs) {
		session.ActiveTab = len(session.Tabs) - 1
	}

	L_debug("browser: closed tab", "index", closeIndex)
	return fmt.Sprintf("Closed tab [%d]: %s", closeIndex, title), nil
}

// navigate goes to a URL in the current tab
func (t *Tool) navigate(ctx context.Context, sessionID string, urlStr string, headed bool) (string, error) {
	if urlStr == "" {
		return "", fmt.Errorf("url is required for navigate action")
	}

	page, err := t.getActivePage(sessionID, "", headed)
	if err != nil {
		return "", err
	}

	if err := t.navigateToURL(page, urlStr); err != nil {
		return "", err
	}

	info, err := page.Info()
	if err != nil {
		return "", fmt.Errorf("failed to get page info: %w", err)
	}

	L_debug("browser: navigated", "url", info.URL, "title", info.Title)
	return fmt.Sprintf("Navigated to: %s\nTitle: %s", info.URL, info.Title), nil
}

// snapshot extracts readable text from the page
func (t *Tool) snapshot(ctx context.Context, sessionID string, urlStr string, maxLength int, format string, headed bool) (string, error) {
	startTotal := time.Now()

	startPage := time.Now()
	page, err := t.getActivePage(sessionID, "", headed)
	if err != nil {
		if isContextCancelledError(err) {
			t.markSessionClosed(sessionID, headed)
			return "", fmt.Errorf("browser disconnected (will recover on retry): %w", err)
		}
		return "", err
	}
	L_debug("browser: snapshot got page", "took", time.Since(startPage))

	// Navigate if URL provided
	if urlStr != "" {
		startNav := time.Now()
		if err := t.navigateToURL(page, urlStr); err != nil {
			if isContextCancelledError(err) {
				t.markSessionClosed(sessionID, headed)
				return "", fmt.Errorf("browser disconnected during navigation (will recover on retry): %w", err)
			}
			return "", err
		}
		L_debug("browser: snapshot navigated", "url", urlStr, "took", time.Since(startNav))
	}

	// Get current URL for readability
	info, err := page.Info()
	if err != nil {
		if isContextCancelledError(err) {
			t.markSessionClosed(sessionID, headed)
			return "", fmt.Errorf("browser disconnected (will recover on retry): %w", err)
		}
		return "", fmt.Errorf("failed to get page info: %w", err)
	}

	L_debug("browser: snapshot extracting", "url", info.URL, "format", format)

	var result string
	startExtract := time.Now()
	switch format {
	case "aria":
		result, err = t.snapshotAria(page, info, maxLength)
	case "ai":
		result, err = t.snapshotAI(sessionID, page, info, maxLength)
	default: // "text"
		result, err = t.snapshotText(page, info, maxLength)
	}

	if err != nil {
		if isContextCancelledError(err) {
			t.markSessionClosed(sessionID, headed)
			return "", fmt.Errorf("browser disconnected during extraction (will recover on retry): %w", err)
		}
		return "", err
	}

	L_info("browser: snapshot complete", "url", info.URL, "format", format, "chars", len(result), "extractTime", time.Since(startExtract), "totalTime", time.Since(startTotal))
	return result, nil
}

// snapshotText extracts readable text content using readability
func (t *Tool) snapshotText(page *rod.Page, info *proto.TargetTargetInfo, maxLength int) (string, error) {
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

// snapshotAI extracts content with numbered interactive elements
func (t *Tool) snapshotAI(sessionID string, page *rod.Page, info *proto.TargetTargetInfo, maxLength int) (string, error) {
	var result strings.Builder
	result.WriteString(fmt.Sprintf("URL: %s\n", info.URL))
	result.WriteString(fmt.Sprintf("Title: %s\n\n", info.Title))

	// Index interactive elements
	startIndex := time.Now()
	elementsIndex, elementsText := t.indexElements(sessionID, page)
	L_debug("browser: indexed elements", "count", elementsIndex, "took", time.Since(startIndex))

	if elementsText != "" {
		result.WriteString("Interactive Elements:\n")
		result.WriteString(elementsText)
		result.WriteString("\n\n---\n\n")
	}

	// Get page text content
	startText := time.Now()
	text, err := page.MustElement("body").Text()
	if err != nil {
		L_warn("browser: failed to get body text", "error", err)
		text = "[Failed to extract page content]"
	}
	L_debug("browser: got body text", "chars", len(text), "took", time.Since(startText))

	result.WriteString("Content:\n")
	result.WriteString(text)

	content := result.String()
	if len(content) > maxLength {
		content = content[:maxLength] + "\n\n[Content truncated...]"
	}

	return content, nil
}

// snapshotAria extracts the accessibility tree
func (t *Tool) snapshotAria(page *rod.Page, info *proto.TargetTargetInfo, maxLength int) (string, error) {
	var result strings.Builder
	result.WriteString(fmt.Sprintf("URL: %s\n", info.URL))
	result.WriteString(fmt.Sprintf("Title: %s\n\n", info.Title))
	result.WriteString("Accessibility Tree:\n\n")

	// Get the accessibility snapshot using CDP
	// This uses the Accessibility.getFullAXTree protocol
	axTree, err := proto.AccessibilityGetFullAXTree{}.Call(page)
	if err != nil {
		L_warn("browser: failed to get accessibility tree", "error", err)
		// Fallback to basic structure
		return t.snapshotAriaFallback(page, info, maxLength)
	}

	// Format the accessibility tree
	for _, node := range axTree.Nodes {
		if node.Role != nil {
			role := node.Role.Value.String()
			name := ""
			if node.Name != nil {
				name = node.Name.Value.String()
			}
			if role != "none" && role != "generic" && role != "" && role != "null" {
				if name != "" && name != "null" {
					result.WriteString(fmt.Sprintf("[%s] %s\n", role, name))
				} else {
					result.WriteString(fmt.Sprintf("[%s]\n", role))
				}
			}
		}
	}

	content := result.String()
	if len(content) > maxLength {
		content = content[:maxLength] + "\n\n[Content truncated...]"
	}

	L_debug("browser: ARIA snapshot complete", "contentLength", len(content))
	return content, nil
}

// snapshotAriaFallback provides a basic structure when full AX tree fails
func (t *Tool) snapshotAriaFallback(page *rod.Page, info *proto.TargetTargetInfo, maxLength int) (string, error) {
	var result strings.Builder
	result.WriteString(fmt.Sprintf("URL: %s\n", info.URL))
	result.WriteString(fmt.Sprintf("Title: %s\n\n", info.Title))
	result.WriteString("Page Structure (fallback):\n\n")

	// Get headings
	headings, _ := page.Elements("h1, h2, h3, h4, h5, h6")
	if len(headings) > 0 {
		result.WriteString("Headings:\n")
		for _, h := range headings {
			tag, _ := h.Property("tagName")
			text, _ := h.Text()
			if text != "" {
				result.WriteString(fmt.Sprintf("  [%s] %s\n", strings.ToLower(tag.String()), truncateString(text, 80)))
			}
		}
		result.WriteString("\n")
	}

	// Get links
	links, _ := page.Elements("a[href]")
	if len(links) > 0 {
		result.WriteString(fmt.Sprintf("Links (%d):\n", len(links)))
		for i, link := range links {
			if i >= 20 {
				result.WriteString(fmt.Sprintf("  ... and %d more\n", len(links)-20))
				break
			}
			text, _ := link.Text()
			href, _ := link.Attribute("href")
			if text != "" && href != nil {
				result.WriteString(fmt.Sprintf("  - %s → %s\n", truncateString(text, 40), truncateString(*href, 60)))
			}
		}
		result.WriteString("\n")
	}

	// Get forms
	forms, _ := page.Elements("form")
	if len(forms) > 0 {
		result.WriteString(fmt.Sprintf("Forms (%d):\n", len(forms)))
		for _, form := range forms {
			inputs, _ := form.Elements("input, select, textarea")
			result.WriteString(fmt.Sprintf("  - Form with %d inputs\n", len(inputs)))
		}
	}

	content := result.String()
	if len(content) > maxLength {
		content = content[:maxLength] + "\n\n[Content truncated...]"
	}

	return content, nil
}

// indexElements indexes interactive elements and returns count and formatted text
// Uses 2 CDP calls instead of thousands: one for elements, one JS call for all info
func (t *Tool) indexElements(sessionID string, page *rod.Page) (int, string) {
	startTotal := time.Now()

	// Get session to store element references
	t.sessionsMu.Lock()
	session := t.sessions[sessionID]
	t.sessionsMu.Unlock()

	if session == nil {
		return 0, ""
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	// Get active tab
	if session.ActiveTab < 0 || session.ActiveTab >= len(session.Tabs) {
		return 0, ""
	}
	tab := session.Tabs[session.ActiveTab]
	tab.Elements = make(map[int]*rod.Element)

	const selector = "a, button, input, select, textarea, [role='button'], [role='link'], [onclick]"
	const maxElements = 200

	// Get element references (1 CDP call)
	startElements := time.Now()
	elements, err := page.Elements(selector)
	if err != nil {
		L_warn("browser: failed to get elements", "error", err)
		return 0, ""
	}
	L_trace("browser: indexElements got refs", "count", len(elements), "took", time.Since(startElements))

	if len(elements) == 0 {
		return 0, ""
	}

	// Limit elements to prevent huge pages
	if len(elements) > maxElements {
		L_debug("browser: indexElements limited", "total", len(elements), "limited", maxElements)
		elements = elements[:maxElements]
	}

	// Get all element info via JavaScript in ONE call (instead of ~10 calls per element)
	// This is the key performance optimization - reduces 5000+ CDP calls to 1
	startJS := time.Now()
	jsResult, err := page.Eval(`() => {
		try {
			const selector = 'a, button, input, select, textarea, [role="button"], [role="link"], [onclick]';
			const nodeList = document.querySelectorAll(selector);
			const results = [];
			const len = Math.min(nodeList.length, 200);
			
			for (let i = 0; i < len; i++) {
				const el = nodeList[i];
				
				// Check visibility
				const rect = el.getBoundingClientRect();
				const style = window.getComputedStyle(el);
				const visible = rect.width > 0 && rect.height > 0 && 
				               style.display !== 'none' && style.visibility !== 'hidden';
				
				// Get label - try text content first
				let label = '';
				try {
					label = (el.textContent || '').trim();
					if (label.length > 100) label = '';
				} catch(e) {}
				
				// Fall back to attributes
				if (!label) {
					label = el.getAttribute('aria-label') || 
					        el.getAttribute('title') || 
					        el.getAttribute('placeholder') || 
					        el.getAttribute('name') || 
					        el.getAttribute('alt') || 
					        el.getAttribute('value') || 
					        (el.id ? '#' + el.id : '') || '';
				}
				
				results.push({
					tag: el.tagName.toLowerCase(),
					label: (label || '').substring(0, 50).replace(/[\n\t]+/g, ' ').trim(),
					visible: visible
				});
			}
			
			return results;
		} catch(e) {
			return { error: e.message };
		}
	}`)
	L_trace("browser: indexElements JS eval", "took", time.Since(startJS))

	if err != nil {
		L_warn("browser: failed to get element info via JS", "error", err)
		return 0, ""
	}

	// Parse JS result
	type elementInfo struct {
		Tag     string `json:"tag"`
		Label   string `json:"label"`
		Visible bool   `json:"visible"`
	}
	var infos []elementInfo
	if err := json.Unmarshal([]byte(jsResult.Value.String()), &infos); err != nil {
		L_warn("browser: failed to parse element info", "error", err)
		return 0, ""
	}

	// Pair element references with info and filter
	var lines []string
	ref := 0
	for i, el := range elements {
		if i >= len(infos) {
			break
		}
		info := infos[i]

		// Skip invisible or unlabeled elements
		if !info.Visible || info.Label == "" {
			continue
		}

		ref++
		tab.Elements[ref] = el
		lines = append(lines, fmt.Sprintf("[%d] %s \"%s\"", ref, info.Tag, info.Label))
	}

	L_debug("browser: indexElements done", "total", len(elements), "visible", ref, "took", time.Since(startTotal))
	return len(tab.Elements), strings.Join(lines, "\n")
}

// getElementLabel extracts a human-readable label for an element
func getElementLabel(el *rod.Element) string {
	// Try text content first
	text, _ := el.Text()
	text = strings.TrimSpace(text)
	if text != "" && len(text) < 100 {
		return text
	}

	// Try common attributes
	for _, attr := range []string{"aria-label", "title", "placeholder", "name", "alt", "value"} {
		val, _ := el.Attribute(attr)
		if val != nil && *val != "" {
			return *val
		}
	}

	// Try id as last resort
	id, _ := el.Attribute("id")
	if id != nil && *id != "" {
		return "#" + *id
	}

	return ""
}

// truncateString truncates a string to maxLen
func truncateString(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	// Collapse multiple spaces
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	if len(s) > maxLen {
		return s[:maxLen-3] + "..."
	}
	return s
}

// screenshot captures the page as an image
func (t *Tool) screenshot(ctx context.Context, sessionID string, urlStr string, fullPage bool, ref *int, headed bool) (string, error) {
	page, err := t.getActivePage(sessionID, "", headed)
	if err != nil {
		return "", err
	}

	// Navigate if URL provided
	if urlStr != "" {
		if err := t.navigateToURL(page, urlStr); err != nil {
			return "", err
		}
	}

	info, err := page.Info()
	if err != nil {
		return "", fmt.Errorf("failed to get page info: %w", err)
	}

	L_debug("browser: taking screenshot", "url", info.URL, "fullPage", fullPage, "ref", ref)

	var imgBytes []byte

	// If ref is provided, screenshot just that element
	if ref != nil {
		el := t.getElementByRef(sessionID, *ref)
		if el == nil {
			return "", fmt.Errorf("element ref [%d] not found (run snapshot first to index elements)", *ref)
		}

		imgBytes, err = el.Screenshot(proto.PageCaptureScreenshotFormatPng, 0)
		if err != nil {
			return "", fmt.Errorf("failed to screenshot element: %w", err)
		}
	} else if fullPage {
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

	// Save to media store
	_, relPath, err := t.mediaStore.Save(imgBytes, "browser", ".png")
	if err != nil {
		return "", fmt.Errorf("failed to save screenshot: %w", err)
	}

	L_debug("browser: screenshot saved", "relPath", relPath, "size", len(imgBytes))

	// Return with MEDIA: prefix for automatic channel delivery
	return fmt.Sprintf("Screenshot saved: %s\nPage: %s\nTitle: %s\nMEDIA:%s", relPath, info.URL, info.Title, relPath), nil
}

// getElementByRef retrieves a previously indexed element by reference number
func (t *Tool) getElementByRef(sessionID string, ref int) *rod.Element {
	t.sessionsMu.Lock()
	session := t.sessions[sessionID]
	t.sessionsMu.Unlock()

	if session == nil {
		return nil
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	if session.ActiveTab < 0 || session.ActiveTab >= len(session.Tabs) {
		return nil
	}

	tab := session.Tabs[session.ActiveTab]
	if tab.Elements == nil {
		return nil
	}

	return tab.Elements[ref]
}

// getElement gets an element by ref or selector
func (t *Tool) getElement(sessionID string, ref *int, selector string, headed bool) (*rod.Element, error) {
	// Prefer ref if provided
	if ref != nil {
		el := t.getElementByRef(sessionID, *ref)
		if el == nil {
			return nil, fmt.Errorf("element ref [%d] not found (run snapshot first)", *ref)
		}
		return el, nil
	}

	// Fall back to selector
	if selector != "" {
		page, err := t.getActivePage(sessionID, "", headed)
		if err != nil {
			return nil, err
		}
		el, err := page.Element(selector)
		if err != nil {
			return nil, fmt.Errorf("element not found: %s", selector)
		}
		return el, nil
	}

	return nil, fmt.Errorf("either ref or selector is required")
}

// click clicks an element
func (t *Tool) click(ctx context.Context, sessionID string, ref *int, selector string, headed bool) (string, error) {
	el, err := t.getElement(sessionID, ref, selector, headed)
	if err != nil {
		return "", err
	}

	// Scroll into view and click
	err = el.ScrollIntoView()
	if err != nil {
		L_warn("browser: failed to scroll into view", "error", err)
	}

	err = el.Click(proto.InputMouseButtonLeft, 1)
	if err != nil {
		return "", fmt.Errorf("click failed: %w", err)
	}

	// Wait for page to stabilize
	page, _ := t.getActivePage(sessionID, "", headed)
	if page != nil {
		page.WaitStable(time.Second)
	}

	// Get element description for feedback
	label := getElementLabel(el)
	L_debug("browser: clicked", "label", label)

	return fmt.Sprintf("Clicked: %s", truncateString(label, 50)), nil
}

// typeText types text into an element (appends to existing)
func (t *Tool) typeText(ctx context.Context, sessionID string, ref *int, selector string, text string, headed bool) (string, error) {
	if text == "" {
		return "", fmt.Errorf("text is required for type action")
	}

	el, err := t.getElement(sessionID, ref, selector, headed)
	if err != nil {
		return "", err
	}

	// Focus and type
	err = el.Click(proto.InputMouseButtonLeft, 1)
	if err != nil {
		return "", fmt.Errorf("failed to focus element: %w", err)
	}

	err = el.Input(text)
	if err != nil {
		return "", fmt.Errorf("type failed: %w", err)
	}

	label := getElementLabel(el)
	L_debug("browser: typed", "label", label, "text", truncateString(text, 20))

	return fmt.Sprintf("Typed into %s: %s", truncateString(label, 30), truncateString(text, 50)), nil
}

// pressKey presses a keyboard key
func (t *Tool) pressKey(ctx context.Context, sessionID string, key string, headed bool) (string, error) {
	if key == "" {
		return "", fmt.Errorf("key is required for press action")
	}

	page, err := t.getActivePage(sessionID, "", headed)
	if err != nil {
		return "", err
	}

	// Map common key names to rod key types
	keyMap := map[string]input.Key{
		"Enter":      input.Enter,
		"Tab":        input.Tab,
		"Escape":     input.Escape,
		"Backspace":  input.Backspace,
		"Delete":     input.Delete,
		"ArrowUp":    input.ArrowUp,
		"ArrowDown":  input.ArrowDown,
		"ArrowLeft":  input.ArrowLeft,
		"ArrowRight": input.ArrowRight,
		"Home":       input.Home,
		"End":        input.End,
		"PageUp":     input.PageUp,
		"PageDown":   input.PageDown,
		"Space":      input.Space,
	}

	if rodKey, ok := keyMap[key]; ok {
		err = page.Keyboard.Press(rodKey)
	} else if len(key) == 1 {
		// Single character - type it directly
		err = page.Keyboard.Type(input.Key(rune(key[0])))
	} else {
		return "", fmt.Errorf("unknown key: %s (use Enter, Tab, Escape, ArrowDown, etc.)", key)
	}

	if err != nil {
		return "", fmt.Errorf("press failed: %w", err)
	}

	L_debug("browser: pressed key", "key", key)
	return fmt.Sprintf("Pressed: %s", key), nil
}

// hover hovers over an element
func (t *Tool) hover(ctx context.Context, sessionID string, ref *int, selector string, headed bool) (string, error) {
	el, err := t.getElement(sessionID, ref, selector, headed)
	if err != nil {
		return "", err
	}

	err = el.ScrollIntoView()
	if err != nil {
		L_warn("browser: failed to scroll into view", "error", err)
	}

	err = el.Hover()
	if err != nil {
		return "", fmt.Errorf("hover failed: %w", err)
	}

	label := getElementLabel(el)
	L_debug("browser: hovered", "label", label)

	return fmt.Sprintf("Hovered: %s", truncateString(label, 50)), nil
}

// scroll scrolls the page
func (t *Tool) scroll(ctx context.Context, sessionID string, direction string, ref *int, selector string, headed bool) (string, error) {
	page, err := t.getActivePage(sessionID, "", headed)
	if err != nil {
		return "", err
	}

	// If ref or selector provided, scroll to element
	if ref != nil || selector != "" {
		el, err := t.getElement(sessionID, ref, selector, headed)
		if err != nil {
			return "", err
		}
		err = el.ScrollIntoView()
		if err != nil {
			return "", fmt.Errorf("scroll to element failed: %w", err)
		}
		label := getElementLabel(el)
		return fmt.Sprintf("Scrolled to: %s", truncateString(label, 50)), nil
	}

	// Otherwise scroll by direction
	switch direction {
	case "down":
		_, err = page.Eval(`window.scrollBy(0, 500)`)
	case "up":
		_, err = page.Eval(`window.scrollBy(0, -500)`)
	case "bottom":
		_, err = page.Eval(`window.scrollTo(0, document.body.scrollHeight)`)
	case "top":
		_, err = page.Eval(`window.scrollTo(0, 0)`)
	default:
		return "", fmt.Errorf("invalid direction: %s (use up, down, top, bottom)", direction)
	}

	if err != nil {
		return "", fmt.Errorf("scroll failed: %w", err)
	}

	L_debug("browser: scrolled", "direction", direction)
	return fmt.Sprintf("Scrolled: %s", direction), nil
}

// selectOption selects an option in a dropdown
func (t *Tool) selectOption(ctx context.Context, sessionID string, ref *int, selector string, value string, headed bool) (string, error) {
	if value == "" {
		return "", fmt.Errorf("value is required for select action")
	}

	el, err := t.getElement(sessionID, ref, selector, headed)
	if err != nil {
		return "", err
	}

	// Use Select method for <select> elements - try by text first
	err = el.Select([]string{value}, true, rod.SelectorTypeText)
	if err != nil {
		// Try by value attribute using CSS selector
		err = el.Select([]string{fmt.Sprintf(`[value="%s"]`, value)}, true, rod.SelectorTypeCSSSector)
		if err != nil {
			return "", fmt.Errorf("select failed: %w", err)
		}
	}

	label := getElementLabel(el)
	L_debug("browser: selected", "label", label, "value", value)

	return fmt.Sprintf("Selected '%s' in %s", value, truncateString(label, 30)), nil
}

// fill clears and fills a form field
func (t *Tool) fill(ctx context.Context, sessionID string, ref *int, selector string, text string, headed bool) (string, error) {
	el, err := t.getElement(sessionID, ref, selector, headed)
	if err != nil {
		return "", err
	}

	// Clear existing content first
	err = el.SelectAllText()
	if err != nil {
		L_warn("browser: failed to select text", "error", err)
	}

	// Input new text (this replaces selected text)
	err = el.Input(text)
	if err != nil {
		return "", fmt.Errorf("fill failed: %w", err)
	}

	label := getElementLabel(el)
	L_debug("browser: filled", "label", label, "text", truncateString(text, 20))

	return fmt.Sprintf("Filled %s with: %s", truncateString(label, 30), truncateString(text, 50)), nil
}

// wait waits for an element or condition
func (t *Tool) wait(ctx context.Context, sessionID string, selector string, timeout time.Duration, headed bool) (string, error) {
	if selector == "" {
		return "", fmt.Errorf("selector is required for wait action")
	}

	page, err := t.getActivePage(sessionID, "", headed)
	if err != nil {
		return "", err
	}

	page = page.Timeout(timeout)

	el, err := page.Element(selector)
	if err != nil {
		return "", fmt.Errorf("timeout waiting for: %s", selector)
	}

	err = el.WaitVisible()
	if err != nil {
		return "", fmt.Errorf("element not visible: %s", selector)
	}

	L_debug("browser: wait complete", "selector", selector)
	return fmt.Sprintf("Element visible: %s", selector), nil
}

// evaluate runs JavaScript code
func (t *Tool) evaluate(ctx context.Context, sessionID string, code string, headed bool) (string, error) {
	if code == "" {
		return "", fmt.Errorf("code is required for evaluate action")
	}

	page, err := t.getActivePage(sessionID, "", headed)
	if err != nil {
		return "", err
	}

	result, err := page.Eval(code)
	if err != nil {
		return "", fmt.Errorf("JavaScript error: %w", err)
	}

	L_debug("browser: evaluated", "code", truncateString(code, 50))

	// Format result
	if result == nil || result.Value.Nil() {
		return "Executed (no return value)", nil
	}

	return fmt.Sprintf("Result: %s", result.Value.String()), nil
}

// navigateToURL handles URL navigation with validation and waiting
func (t *Tool) navigateToURL(page *rod.Page, urlStr string) error {
	start := time.Now()

	// Validate URL for SSRF safety (scheme, private IPs, cloud metadata, etc.)
	if err := ValidateURLSafety(urlStr); err != nil {
		return err
	}

	L_debug("browser: navigating", "url", urlStr)

	// Navigate with timeout
	page = page.Timeout(t.timeout)
	startNav := time.Now()
	if err := page.Navigate(urlStr); err != nil {
		return fmt.Errorf("navigation failed: %w", err)
	}
	L_trace("browser: page.Navigate done", "took", time.Since(startNav))

	// Wait for page load event
	startWait := time.Now()
	if err := page.WaitLoad(); err != nil {
		L_warn("browser: WaitLoad timeout", "url", urlStr, "took", time.Since(startWait))
		// Continue anyway - page might still be usable
	} else {
		L_trace("browser: page loaded", "took", time.Since(startWait))
	}

	// Brief stability wait - 500ms without activity, max 3s total
	// This catches most post-load rendering without blocking on SPAs
	startStable := time.Now()
	stablePage := page.Timeout(3 * time.Second)
	if err := stablePage.WaitStable(500 * time.Millisecond); err != nil {
		L_debug("browser: stability wait timeout (normal for SPAs)", "url", urlStr, "took", time.Since(startStable))
	} else {
		L_debug("browser: page stable", "url", urlStr, "took", time.Since(startStable))
	}

	L_debug("browser: navigation complete", "url", urlStr, "took", time.Since(start))
	return nil
}

// CloseSession closes all tabs for a session
func (t *Tool) CloseSession(sessionID string) {
	t.sessionsMu.Lock()
	defer t.sessionsMu.Unlock()

	session, ok := t.sessions[sessionID]
	if !ok {
		return
	}

	session.mu.Lock()
	for _, tab := range session.Tabs {
		if tab.Page != nil {
			tab.Page.Close()
		}
	}
	// Close headed browser instance if present
	if session.Browser != nil {
		session.Browser.Close()
	}
	session.mu.Unlock()

	delete(t.sessions, sessionID)
	L_debug("browser: closed session", "sessionID", sessionID, "headed", session.Headed)
}

// CloseAll closes all sessions and their tabs
func (t *Tool) CloseAll() {
	t.sessionsMu.Lock()
	defer t.sessionsMu.Unlock()

	for sessionID, session := range t.sessions {
		session.mu.Lock()
		for _, tab := range session.Tabs {
			if tab.Page != nil {
				tab.Page.Close()
			}
		}
		// Close headed browser instance if present
		if session.Browser != nil {
			session.Browser.Close()
		}
		session.mu.Unlock()
		L_debug("browser: closed session", "sessionID", sessionID, "headed", session.Headed)
	}

	t.sessions = make(map[string]*SessionTabs)
}

// savePDF saves the current page as PDF
func (t *Tool) savePDF(ctx context.Context, sessionID string, headed bool) (string, error) {
	page, err := t.getActivePage(sessionID, "", headed)
	if err != nil {
		return "", err
	}

	info, err := page.Info()
	if err != nil {
		return "", fmt.Errorf("failed to get page info: %w", err)
	}

	L_debug("browser: saving PDF", "url", info.URL)

	// Generate PDF
	pdfReader, err := page.PDF(&proto.PagePrintToPDF{
		PrintBackground: true,
	})
	if err != nil {
		return "", fmt.Errorf("failed to generate PDF: %w", err)
	}

	// Read all PDF data from the stream
	pdfData, err := io.ReadAll(pdfReader)
	if err != nil {
		return "", fmt.Errorf("failed to read PDF data: %w", err)
	}

	// Save to media store
	_, relPath, err := t.mediaStore.Save(pdfData, "browser", ".pdf")
	if err != nil {
		return "", fmt.Errorf("failed to save PDF: %w", err)
	}

	L_debug("browser: PDF saved", "relPath", relPath, "size", len(pdfData))

	return fmt.Sprintf("PDF saved: %s\nPage: %s\nTitle: %s\nMEDIA:%s", relPath, info.URL, info.Title, relPath), nil
}

// ConsoleMessage represents a browser console message
type ConsoleMessage struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// getConsole returns recent console messages
func (t *Tool) getConsole(ctx context.Context, sessionID string, headed bool) (string, error) {
	page, err := t.getActivePage(sessionID, "", headed)
	if err != nil {
		return "", err
	}

	// Get console messages by evaluating JavaScript
	result, err := page.Eval(`
		(function() {
			// Try to get console history if available
			if (window.__goclaw_console) {
				return JSON.stringify(window.__goclaw_console);
			}
			return '[]';
		})()
	`)
	if err != nil {
		return "", fmt.Errorf("failed to get console: %w", err)
	}

	// Note: This is a best-effort approach. For real-time console monitoring,
	// we'd need to set up a page event listener before navigation.
	consoleStr := result.Value.String()
	if consoleStr == "[]" || consoleStr == "" {
		return "No console messages captured.\n\nNote: Console messages are only captured if monitoring was set up before page load. Use evaluate action to access console.log() in real-time.", nil
	}

	return fmt.Sprintf("Console messages:\n%s", consoleStr), nil
}

// uploadFile uploads a file to a file input element
func (t *Tool) uploadFile(ctx context.Context, sessionID string, ref *int, selector string, filePath string, headed bool) (string, error) {
	if filePath == "" {
		return "", fmt.Errorf("file path is required for upload action")
	}

	el, err := t.getElement(sessionID, ref, selector, headed)
	if err != nil {
		return "", err
	}

	// Verify it's a file input
	tagName, _ := el.Property("tagName")
	inputType, _ := el.Attribute("type")
	if strings.ToLower(tagName.String()) != "input" || (inputType != nil && strings.ToLower(*inputType) != "file") {
		return "", fmt.Errorf("element is not a file input")
	}

	// Set the file
	err = el.SetFiles([]string{filePath})
	if err != nil {
		return "", fmt.Errorf("upload failed: %w", err)
	}

	L_debug("browser: uploaded file", "path", filePath)
	return fmt.Sprintf("Uploaded: %s", filePath), nil
}

// handleDialog handles JavaScript dialogs (alert, confirm, prompt)
func (t *Tool) handleDialog(ctx context.Context, sessionID string, action string, text string, headed bool) (string, error) {
	page, err := t.getActivePage(sessionID, "", headed)
	if err != nil {
		return "", err
	}

	if action == "" {
		action = "accept"
	}

	// Set up dialog handler
	// Note: This sets up a handler for the NEXT dialog that appears
	wait := page.EachEvent(func(e *proto.PageJavascriptDialogOpening) bool {
		dialogType := string(e.Type)
		dialogMessage := e.Message

		L_debug("browser: dialog opened", "type", dialogType, "message", dialogMessage)

		var handleErr error
		if action == "dismiss" {
			handleErr = proto.PageHandleJavaScriptDialog{
				Accept: false,
			}.Call(page)
		} else {
			handleErr = proto.PageHandleJavaScriptDialog{
				Accept:     true,
				PromptText: text,
			}.Call(page)
		}

		if handleErr != nil {
			L_warn("browser: failed to handle dialog", "error", handleErr)
		}

		return true // Stop after handling one dialog
	})

	// Wait briefly for any pending dialog
	go func() {
		time.Sleep(100 * time.Millisecond)
		wait() // This will block until a dialog is handled or we manually cancel
	}()

	return fmt.Sprintf("Dialog handler set: %s (will handle next dialog)", action), nil
}
