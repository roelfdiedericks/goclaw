---
title: "Browser"
description: "Managed Chromium for web automation and authenticated browsing"
section: "Tools"
weight: 20
---

# Browser Tool

GoClaw includes a managed browser for JavaScript-rendered pages, sites with bot protection, authenticated web automation, and named remote CDP browser connections.

## Overview

The browser tool provides:
- **Managed Chromium** - Auto-downloaded and updated
- **Profile persistence** - Cookies, sessions, and auth survive restarts
- **Stealth mode** - Anti-bot detection features
- **Chrome extension relay** - Connect to your existing Chrome tabs
- **Named remote browsers** - Connect to Chrome instances running on other machines over CDP
- **Full automation** - Snapshot, screenshot, click, type, and more

## Directory Structure

```
~/.goclaw/browser/
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
      "allowAgentProfiles": false,
      "remote": {
        "enabled": true,
        "profilesText": "workstation=ws://192.168.1.50:9222/devtools/browser/abc123\nstaging=http://10.0.0.20:9222",
        "allowedHosts": ["192.168.1.50", "10.0.0.0/24"],
        "allowDirectEndpoints": false,
        "allowHTTPDiscovery": true,
        "connectionTimeout": "10s"
      },
      "advanced": {
        "networkCaptureEnabled": true,
        "networkCaptureMax": 200,
        "consoleCaptureEnabled": true,
        "consoleCaptureMax": 200,
        "traceRetention": 20
      }
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
| `remote.enabled` | `true` | Enable support for named remote browser profiles |
| `remote.profilesText` | `""` | One profile per line: `name=endpoint` for browsers on other machines |
| `remote.allowedHosts` | `[]` | Optional safety allowlist of remote hosts or CIDRs |
| `remote.allowDirectEndpoints` | `false` | Future-facing option for raw CDP URLs instead of only named remote profiles |
| `remote.allowHTTPDiscovery` | `true` | Allow simple `http://host:port` browser addresses and auto-discover the websocket CDP URL |
| `remote.connectionTimeout` | `"10s"` | Max time to spend connecting to or discovering a remote browser |
| `advanced.networkCaptureEnabled` | `true` | Capture request, response, and failure events for observability actions |
| `advanced.networkCaptureMax` | `200` | Maximum network entries to retain when capture is enabled |
| `advanced.consoleCaptureEnabled` | `true` | Capture native console and exception events for observability actions |
| `advanced.consoleCaptureMax` | `200` | Maximum console entries to retain when capture is enabled |
| `advanced.traceDir` | `""` | Optional override for performance trace artifact directory |
| `advanced.traceRetention` | `20` | Maximum trace artifacts to retain |

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
2. Explicit profile such as `remote:workstation` + `allowAgentProfiles: true` → Honored if configured
3. Other explicit profile + `allowAgentProfiles: true` → Honored
4. Explicit profile + `allowAgentProfiles: false` → Ignored (with note in response)
5. Profile omitted → Use `profileDomains` based on URL
6. No domain match → Use `defaultProfile`

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

### Remote Browser Profiles

Named remote browsers let GoClaw connect to a Chrome instance running on another machine over the DevTools Protocol.

Configure them with one entry per line:

```json
"remote": {
  "enabled": true,
  "profilesText": "workstation=ws://192.168.1.50:9222/devtools/browser/abc123\nstaging=http://10.0.0.20:9222",
  "allowedHosts": ["192.168.1.50", "10.0.0.0/24"],
  "allowHTTPDiscovery": true,
  "connectionTimeout": "10s"
}
```

Examples:
- `remote:workstation` → Connect directly to a websocket CDP endpoint
- `remote:staging` → Resolve an HTTP discovery endpoint and then attach to the returned websocket URL

Operator notes:
- `profile="chrome"` is still reserved for the local Chrome relay path on this machine
- Remote browsers are configured explicitly and are treated as external browsers
- If you use `allowedHosts`, the remote host must match an entry in that list
- Exposing CDP over a network is powerful; only expose trusted Chrome instances on trusted networks

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
| `drag` | Drag from one element to another |
| `scroll` | Scroll page or to element |
| `select` | Select option in dropdown |
| `fill` | Fill form field (clears first) |
| `wait` | Wait for element or condition |
| `evaluate` | Run JavaScript code |

### Advanced

| Action | Description |
|--------|-------------|
| `console` | Get browser console logs |
| `console_list` | List captured console and exception messages |
| `network_list` | List captured network requests |
| `network_get` | Show one captured network request in detail |
| `performance_trace_start` | Start a performance trace for the active tab |
| `performance_trace_stop` | Stop the active performance trace and save the artifact |
| `performance_metrics` | Get a first-pass runtime performance summary |
| `resize` | Set an explicit viewport width and height for the active tab |
| `emulate_device` | Apply a named device profile such as `iphone-x` or `laptop` |
| `emulate_cpu` | Apply CPU throttling to the active tab |
| `emulate_network` | Apply a network preset such as `slow-3g`, `fast-3g`, or `offline` |
| `emulation_reset` | Clear emulation overrides for the active tab |
| `capture_reset` | Clear captured console and network state for the active tab |
| `upload` | Upload file to input element |
| `dialog` | Handle alert/confirm/prompt dialogs |

## Observability and Performance

The browser tool now supports built-in capture for:
- console messages and JavaScript exceptions
- network requests, responses, and failures
- performance trace lifecycle
- first-pass runtime performance metrics

### Console and Network Inspection

Use:
- `console` or `console_list` to inspect captured console output
- `network_list` to review recent requests
- `network_get` with a request ID from `network_list` to inspect one request in detail
- `capture_reset` to clear captured console/network state for the active tab

### Performance Tracing

Use:
1. `performance_trace_start`
2. interact with the page or reproduce the issue
3. `performance_trace_stop`

The trace artifact is saved either to:
- `advanced.traceDir` if configured, or
- the media store under `./media/browser/traces/`

`performance_metrics` provides a lighter-weight summary without requiring a full trace artifact.

## Emulation Controls

The browser tool also supports page-scoped emulation controls:
- `resize` for explicit viewport size
- `emulate_device` for named device profiles
- `emulate_cpu` for CPU slowdown
- `emulate_network` for simple network presets
- `emulation_reset` to clear overrides

Supported network presets:
- `clear`
- `fast-3g`
- `slow-3g`
- `offline`

These overrides are intended to be reversible and safe to combine with performance tracing. A good pattern is:
1. `emulate_device` or `resize`
2. `emulate_network` and optionally `emulate_cpu`
3. `performance_trace_start`
4. reproduce the problem
5. `performance_trace_stop`
6. `emulation_reset`

## MCP-Style Aliases

For compatibility with Chrome DevTools MCP-style naming, the browser tool also accepts these aliases:

- `new_page` → `open`
- `navigate_page` → `navigate`
- `take_snapshot` → `snapshot`
- `take_screenshot` → `screenshot`
- `evaluate_script` → `evaluate`
- `list_console_messages` → `console_list`
- `list_network_requests` → `network_list`
- `get_network_request` → `network_get`
- `performance_start_trace` → `performance_trace_start`
- `performance_stop_trace` → `performance_trace_stop`
- `performance_analyze_insight` → `performance_metrics`
- `resize_page` → `resize`
- `wait_for` → `wait`
- `handle_dialog` → `dialog`
- `upload_file` → `upload`
- `fill_form` → `fill`

These aliases are intended to make the tool easier to use from MCP-oriented prompts without changing the native GoClaw action names.

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
