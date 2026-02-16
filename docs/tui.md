---
title: "Interactive TUI"
description: "Terminal-based user interface for direct agent interaction"
section: "Channels"
weight: 30
---

# Interactive TUI

GoClaw includes a terminal-based user interface (TUI) for direct interaction with your agent.

## Starting the TUI

```bash
goclaw gateway -i
```

The `-i` flag starts the gateway with the interactive TUI enabled.

## Interface Layout

The TUI has a split-pane layout:

```
┌─────────────────────────────────┬────────────────────┐
│ Chat                            │ Logs               │
│                                 │                    │
│ You: switch on the lights       │ [INFO] tool call   │
│ Agent: Done — lights are on.    │ [INFO] hass: call  │
│                                 │                    │
│ > Type a message...             │                    │
├─────────────────────────────────┴────────────────────┤
│ ✓ Ready │ User: TheRoDent │ Tab: focus │ Enter: send │
└──────────────────────────────────────────────────────┘
```

### Panels

- **Chat Panel** — Conversation with your agent
- **Logs Panel** — Real-time gateway logs

### Status Bar

Shows connection status, current user, and keyboard shortcuts.

## Keyboard Shortcuts

| Key | Action |
|-----|--------|
| `Enter` | Send message |
| `Tab` | Switch focus between panels |
| `Ctrl+L` | Toggle layout (horizontal/vertical split) |
| `Ctrl+C` | Exit |
| `Up/Down` | Scroll through message history |
| `PgUp/PgDn` | Scroll logs |

## Configuration

The TUI is enabled by the `-i` flag; no additional configuration needed.

### Log Level

Control log verbosity:

```bash
# Normal logging
goclaw gateway -i

# Debug logging (more verbose)
goclaw gateway -i -d

# Trace logging (very verbose)
goclaw gateway -i -t
```

## Tips

- The TUI is great for development and testing
- Logs panel shows tool calls, API requests, and errors in real-time
- Use `Tab` to switch between chat and logs for scrolling
- The gateway continues running all channels (Telegram, HTTP) alongside the TUI

## Limitations

- Requires a terminal with color support
- Minimum terminal size: 80x24 characters
- Some terminals may not render Unicode characters correctly
