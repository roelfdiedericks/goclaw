---
title: "Browser"
description: "Managed Chromium for web automation and authenticated browsing"
section: "Tools"
weight: 20
---

# Browser Tool

GoClaw includes a managed browser for JavaScript-rendered pages, sites with bot protection, and authenticated web automation.

## Overview

The browser tool provides:
- **Managed Chromium** - Auto-downloaded and updated
- **Profile persistence** - Cookies, sessions, and auth survive restarts
- **Stealth mode** - Anti-bot detection features
- **Chrome extension relay** - Connect to your existing Chrome tabs
- **Full automation** - Snapshot, screenshot, click, type, and more

## Directory Structure

```
~/.openclaw/goclaw/browser/
├── bin/                      # Chromium binary (auto-downloaded)
│   └── chromium-XXXXXX/
└── profiles/
    ├── default/              # Default profile
    ├── twitter/              # Named profiles
    └── ...
```

## Configuration

In `goclaw.json`:

```json
{
  "tools": {
    "browser": {
      "enabled": true,
      "autoDownload": true,
      "headless": true,
      "noSandbox": false,
      "stealth": true,
      "device": "clear",
      "defaultProfile": "default",
      "timeout": "60s",
      "profileDomains": {
        "*.twitter.com": "twitter",
        "*.x.com": "twitter",
        "*": "default"
      },
      "chromeCDP": "ws://localhost:9222",
      "allowAgentProfiles": false
    }
  }
}
```

### Configuration Options

| Option | Default | Description |
|--------|---------|-------------|
| `enabled` | `true` | Enable/disable browser tool |
| `autoDownload` | `true` | Auto-download Chromium on first use |
| `headless` | `true` | Run browser without visible window |
| `noSandbox` | `false` | Disable sandbox (needed for Docker/root) |
| `stealth` | `true` | Enable anti-bot detection features |
| `device` | `"clear"` | Device emulation: `"clear"`, `"laptop"`, `"iphone-x"`, etc. |
| `defaultProfile` | `"default"` | Default profile name |
| `timeout` | `"60s"` | Default action timeout |
| `profileDomains` | `{}` | Domain → profile mapping (supports wildcards) |
| `chromeCDP` | `"ws://localhost:9222"` | CDP endpoint for Chrome extension relay |
| `allowAgentProfiles` | `false` | Allow agent to specify any profile |

## Profile System

### Automatic Profile Selection

When the agent makes a browser request, GoClaw automatically selects a profile based on the URL:

```json
"profileDomains": {
  "*.twitter.com": "twitter",
  "*.x.com": "twitter",
  "github.com": "work",
  "*": "default"
}
```

- `*.twitter.com` → Uses "twitter" profile
- `github.com` → Uses "work" profile  
- Everything else → Uses "default" profile

### Profile Selection Precedence

1. `profile="chrome"` → Always honored (Chrome extension relay)
2. Explicit profile + `allowAgentProfiles: true` → Honored
3. Explicit profile + `allowAgentProfiles: false` → Ignored (with note in response)
4. Profile omitted → Use `profileDomains` based on URL
5. No domain match → Use `defaultProfile`

### Chrome Extension Relay

To use your existing Chrome with logged-in sessions:

1. Install the OpenClaw browser extension in Chrome
2. Click the toolbar icon to attach a tab
3. Agent uses `profile="chrome"` to access that tab

The agent will automatically use `profile="chrome"` when users mention:
- "Chrome extension"
- "Browser relay"
- "Attach tab"
- "My browser"

## CLI Commands

### `goclaw browser download`

Download or update the managed Chromium browser.

```bash
goclaw browser download
goclaw browser download --force  # Force re-download
```

### `goclaw browser setup [profile] [url]`

Launch a visible browser for profile setup (login, set cookies, etc.).

```bash
goclaw browser setup                    # Setup default profile
goclaw browser setup twitter            # Setup "twitter" profile
goclaw browser setup twitter x.com      # Open x.com for twitter profile
```

Close the browser when done. Cookies and sessions are saved to the profile.

### `goclaw browser profiles`

List all browser profiles with size and last-used info.

```bash
goclaw browser profiles
```

### `goclaw browser clear <profile>`

Clear all data for a profile (cookies, cache, sessions).

```bash
goclaw browser clear twitter
goclaw browser clear twitter --force  # Skip confirmation
```

### `goclaw browser status`

Show browser status (download state, running instances, profiles).

```bash
goclaw browser status
```

### `goclaw browser migrate`

Import profiles from OpenClaw.

```bash
goclaw browser migrate
```

This will:
1. Find profiles in `~/.openclaw/browser/profiles/`
2. Offer to copy each to GoClaw
3. Suggest renaming "openclaw" → "default"
4. Preserve all cookies and sessions

## Tool Actions

### Tab Management

| Action | Description |
|--------|-------------|
| `tabs` | List all open tabs |
| `open` | Open a new tab (optionally with URL) |
| `focus` | Switch to tab by index |
| `close` | Close a tab (default: current) |

### Navigation

| Action | Description |
|--------|-------------|
| `navigate` | Go to URL in current tab |
| `snapshot` | Extract readable text (formats: text, ai, aria) |
| `screenshot` | Capture page as image |
| `pdf` | Save page as PDF |

### Interaction

| Action | Description |
|--------|-------------|
| `click` | Click element by ref or selector |
| `type` | Type text into element |
| `press` | Press keyboard key(s) |
| `hover` | Hover over element |
| `scroll` | Scroll page or to element |
| `select` | Select option in dropdown |
| `fill` | Fill form field (clears first) |
| `wait` | Wait for element or condition |
| `evaluate` | Run JavaScript code |

### Advanced

| Action | Description |
|--------|-------------|
| `console` | Get browser console logs |
| `upload` | Upload file to input element |
| `dialog` | Handle alert/confirm/prompt dialogs |

## Element References

Snapshots with `format="ai"` include numbered element references:

```
URL: https://x.com/home
Title: Home / X

[e1] Search [input]
[e2] Home [link]
[e3] Notifications [link]
[e4] Post [button]
...

[Main timeline content...]
```

Use these refs in subsequent actions:

```json
{"action": "click", "ref": 4}
{"action": "type", "ref": 1, "text": "hello world"}
```

## Migration from OpenClaw

If you're migrating from OpenClaw:

### 1. Import Profiles

```bash
goclaw browser migrate
```

This copies your existing profiles (with logged-in sessions) to GoClaw.

### 2. Configure Profile Selection

**Option A: Config-driven (recommended)**

Set up `profileDomains` to automatically select profiles by domain:

```json
"profileDomains": {
  "*.twitter.com": "twitter",
  "*.x.com": "twitter",
  "*": "default"
}
```

**Option B: Agent-driven (OpenClaw compatible)**

Allow the agent to specify profiles directly:

```json
"allowAgentProfiles": true
```

### 3. Differences from OpenClaw

| Aspect | OpenClaw | GoClaw |
|--------|----------|--------|
| Default profile | `openclaw` | `default` |
| Profile selection | Agent specifies | Config-driven (default) |
| Chrome relay | `profile="chrome"` | `profile="chrome"` (same) |

### Profile Parameter Handling

When `allowAgentProfiles: false` (default):
- Only `profile="chrome"` is honored
- Other profiles are ignored with a helpful note in the response:
  ```json
  {
    "url": "https://x.com/...",
    "profileUsed": "twitter",
    "profileNote": "Requested profile 'openclaw' ignored. Using 'twitter' based on domain config. If auth fails, run: goclaw browser setup twitter"
  }
  ```
- Profiles are selected automatically based on URL domain

When `allowAgentProfiles: true`:
- Any profile can be specified by the agent
- Explicit profile overrides `profileDomains`
- Compatible with OpenClaw prompts and skills

## Troubleshooting

### Browser won't start after crash

GoClaw automatically cleans up stale lock files. If issues persist:

```bash
rm ~/.openclaw/goclaw/browser/profiles/*/SingletonLock
```

### Authentication not working

1. Run `goclaw browser setup <profile>` and log in manually
2. Verify the correct profile is being selected (check logs)
3. Ensure `profileDomains` maps the domain to the right profile

### Chrome extension not connecting

1. Ensure Chrome is running with the extension installed
2. Click the toolbar icon to attach the tab (badge should be ON)
3. Check that `chromeCDP` points to the correct endpoint

### Bot detection / Cloudflare blocks

1. Ensure `stealth: true` in config
2. Use `goclaw browser setup` to complete any CAPTCHAs
3. Consider using a logged-in profile for the site
