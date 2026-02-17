# Browser Tab Tracking Sync Spec

## Problem Statement

The browser tool's internal tab tracking (`session.Tabs`) gets out of sync with the actual browser state. This causes:

1. **Phantom `about:blank`** — Tool thinks it's on `about:blank` while user sees the actual page
2. **Tab count mismatch** — Tool reports 1 tab, browser actually has 3
3. **Snapshot hits wrong page** — Snapshots `about:blank` instead of the intended page
4. **Confusing agent behavior** — Agent sees stale state, makes wrong decisions

## Root Cause

When a headed browser launches, go-rod creates an initial `about:blank` page. Our code doesn't enumerate existing pages — it only tracks pages WE create via `open` or `getActivePage`.

**Current flow:**
```
1. LaunchHeaded() → browser opens with about:blank (tab 0)
2. session.Tabs = [] (empty, we don't track the initial tab)
3. open(url="x.com") → creates new page, adds to Tabs as index 0
4. BUT browser has: [about:blank, x.com] — our index 0 is browser's index 1
5. snapshot() → might hit wrong page due to index mismatch
6. navigate() → reports success but we're looking at wrong tab
```

**Observed symptoms:**
- `tabs` action shows 1 tab (`about:blank`)
- Actual browser shows 3 tabs (about:blank, x.com/home, x.com/home)
- `navigate` reports success with correct title but `snapshot` returns blank
- Agent thinks page didn't load, retries, creates more orphan tabs

## Code Locations

### internal/browser/tool.go

- `getOrCreateSession()` — Creates session but doesn't populate Tabs from existing pages
- `getActivePage()` — Creates new page if `session.Tabs` is empty, adds to Tabs
- `openTab()` — Adds to Tabs array but doesn't account for pre-existing browser tabs
- `listTabs()` — Returns our tracked Tabs, not actual browser state
- `focusTab()` — Uses our index which may not match browser's actual index

### internal/browser/manager.go

- `LaunchHeaded()` / `LaunchHeadless()` — Launch browser but don't return initial page list

## Proposed Solution

### Option A: Sync on Session Creation (Recommended)

After browser connects, enumerate existing pages and populate `Tabs`:

```go
func (t *BrowserTool) getOrCreateSession(profile string, headed bool) (*browserSession, error) {
    // ... existing launch logic ...
    
    // Sync tabs with actual browser state
    pages, err := browser.Pages()
    if err == nil {
        for i, page := range pages {
            info, _ := page.Info()
            session.Tabs = append(session.Tabs, &TabInfo{
                Index: i,
                Page:  page,
                URL:   info.URL,
                Title: info.Title,
            })
        }
        if len(pages) > 0 {
            session.ActiveTab = 0
        }
    }
    
    return session, nil
}
```

**Pros:**
- Always start with accurate state
- Simple, one-time sync
- No ongoing overhead

**Cons:**
- Doesn't handle external tab creation (user opens tab manually)

### Option B: Sync Before Each Tab Operation

Add a `syncTabs()` helper that reconciles `session.Tabs` with actual browser pages:

```go
func (t *BrowserTool) syncTabs(session *browserSession) error {
    pages, err := session.Browser.Pages()
    if err != nil {
        return err
    }
    
    // Rebuild Tabs array from actual pages
    session.Tabs = nil
    for i, page := range pages {
        info, _ := page.Info()
        session.Tabs = append(session.Tabs, &TabInfo{
            Index: i,
            Page:  page,
            URL:   info.URL,
            Title: info.Title,
        })
    }
    
    // Clamp ActiveTab to valid range
    if session.ActiveTab >= len(session.Tabs) {
        session.ActiveTab = len(session.Tabs) - 1
    }
    if session.ActiveTab < 0 {
        session.ActiveTab = 0
    }
    
    return nil
}
```

Call before `listTabs`, `snapshot`, `navigate`, etc.

**Pros:**
- Always accurate, handles external changes
- Resilient to any state drift

**Cons:**
- Extra API calls on every operation
- Slight performance overhead

### Option C: Close Initial about:blank

After launch, if first page is `about:blank` and we're about to open a URL, close it:

```go
pages, _ := browser.Pages()
if len(pages) == 1 {
    info, _ := pages[0].Info()
    if info.URL == "about:blank" {
        pages[0].Close()
    }
}
```

**Pros:**
- No tracking complexity
- Clean slate

**Cons:**
- Might break if about:blank is needed
- Doesn't solve the general sync problem

## Recommendation

**Option A** as baseline — sync on session creation to start with accurate state.

Consider **Option B** as enhancement if external tab manipulation becomes common (user clicks links, opens tabs manually).

**Option C** can be added opportunistically — when opening first URL in a session with only about:blank, navigate in that tab instead of creating new one.

## Additional Considerations

### Active Tab Tracking

When we create a new tab via `open`, we set `session.ActiveTab = newIndex`. But if the browser has pre-existing tabs we didn't track, our index is wrong.

After sync, need to determine which page is actually focused:
```go
// go-rod might have a way to get the focused page
// or we track by matching the page we just created
```

### Tab Close Cleanup

When `close` action is called, we remove from `Tabs` and shift indices. Need to ensure browser's actual tab is closed too (this probably works, but verify).

### Headless vs Headed

Headless browsers might behave differently — may not create initial about:blank. Test both modes.

## Test Cases

1. **Fresh headed launch** — `tabs` should show initial about:blank
2. **Open URL on fresh session** — Should navigate in existing tab or create new one cleanly
3. **Multiple opens** — Tab count should match actual browser
4. **Navigate then snapshot** — Should return content from correct page
5. **Close tab** — Should close correct tab, update indices
6. **Focus by index** — Should focus correct tab in browser

---

## Implementation Status

**Implemented (2026-02-08):**

1. **`syncTabs()` helper** — Reconciles `session.Tabs` with actual browser pages via `browser.Pages()`. Preserves existing `TabInfo` by matching target IDs, discovers new pages, removes stale entries.

2. **Sync on session creation** — After `LaunchHeaded()`, immediately sync to pick up the initial `about:blank` tab.

3. **Sync before every tab operation** — Added sync calls to:
   - `getActivePage()` — covers navigate, snapshot, screenshot, etc.
   - `listTabs()`
   - `openTab()`
   - `focusTab()`
   - `closeTab()`

4. **Reuse about:blank optimization** — In `openTab()`, if there's exactly one `about:blank` tab and URL is provided, navigate in that tab instead of creating a new one.

**Result:** Tab tracking now stays in sync with actual browser state. No more phantom tabs, correct indices, snapshots hit the right page.

---

*Status: Implemented*
*Author: Ratpup*
*Date: 2026-02-08*
