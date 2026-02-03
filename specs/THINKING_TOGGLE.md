# Thinking/Tool Output Toggle Specification

## Overview

Allow users to toggle visibility of assistant "thinking" - tool calls, intermediate steps, and working output. When enabled, shows what the agent is doing behind the scenes. When disabled, only shows final responses.

## User Control

### Commands

```
/thinking on      - Show tool calls and working output
/thinking off     - Hide tool calls, show only final responses
/thinking toggle  - Switch between on/off
/thinking         - Show current status
```

### Persistence

- Setting stored per-user in session/config
- Persists across sessions and restarts
- Default: OFF (cleaner experience for most users)

## What "Thinking" Includes

When thinking is ON, show:

| Event | Display |
|-------|---------|
| Tool start | Tool name + arguments (truncated if long) |
| Tool running | Spinner/animation |
| Tool complete | Status (✓/✗) + duration |
| Tool result | Output preview (collapsible for long results) |
| Extended thinking | Claude's reasoning (if using thinking models) |

When thinking is OFF:
- None of the above
- Only final text responses
- Cleaner, chat-like experience

## SSE Events

Gateway emits events for tool activity:

```json
{"type": "tool_start", "id": "toolu_123", "tool": "exec", "input": "curl -s https://..."}

{"type": "tool_end", "id": "toolu_123", "tool": "exec", "status": "completed", "duration_ms": 234, "output_preview": "HTTP/1.1 200 OK..."}

{"type": "tool_error", "id": "toolu_123", "tool": "exec", "error": "command failed: exit 1"}

{"type": "thinking", "content": "I should check the camera first..."}

{"type": "text", "content": "Here's what I found..."}
```

Frontend filters events based on thinking toggle state.

## Channel Implementations

### HTTP Web UI

**Thinking ON:**
```html
<div class="tool-call">
  <div class="tool-header">
    <span class="tool-icon">⚙️</span>
    <span class="tool-name">exec</span>
    <span class="tool-status completed">✓</span>
  </div>
  <div class="tool-input">curl -s -H "Authorization: Bearer ey..."</div>
  <div class="tool-output collapsible">
    <pre>{"result": "..."}</pre>
  </div>
</div>
```

CSS:
```css
.tool-call {
    background: #f5f5f5;
    border-radius: 8px;
    padding: 8px 12px;
    margin: 4px 0;
    font-family: monospace;
    font-size: 0.85em;
}

.tool-header {
    display: flex;
    align-items: center;
    gap: 8px;
}

.tool-status.completed { color: green; }
.tool-status.error { color: red; }
.tool-status.running { animation: pulse 1s infinite; }

.tool-output.collapsible {
    max-height: 100px;
    overflow: hidden;
    cursor: pointer;
}

.tool-output.collapsible.expanded {
    max-height: none;
}
```

**Thinking OFF:**
- Tool events received but not rendered
- Only `{"type": "text"}` events displayed

**Toggle Button:**

Placement: Header bar, near user badge / session info

```html
<button id="thinking-toggle" class="btn btn-outline-secondary btn-sm" 
        data-bs-toggle="tooltip" 
        title="Thinking OFF (click to toggle)">
  <i class="bi bi-brain"></i>
</button>
```

States:
- **OFF (default):** Outline style, dimmed - `btn-outline-secondary`
- **ON:** Filled style, highlighted - `btn-primary`

**State Management:**

Server is source of truth. No localStorage needed.

1. SSE connects → server sends current state
2. Button click → sends `/thinking` command
3. Server toggles → emits `preference` SSE event
4. UI updates button based on event
5. Reconnect/refresh → server sends state again

```javascript
// SSE handler for preferences
eventSource.addEventListener('preference', (e) => {
    const data = JSON.parse(e.data);
    if (data.key === 'thinking') {
        updateThinkingButton(data.value);
    }
});

// Button sends toggle command
$('#thinking-toggle').click(() => sendMessage('/thinking'));

// Update button appearance
function updateThinkingButton(enabled) {
    $('#thinking-toggle')
        .toggleClass('btn-outline-secondary', !enabled)
        .toggleClass('btn-primary', enabled)
        .attr('title', `Thinking ${enabled ? 'ON' : 'OFF'} (click to toggle)`);
}
```

**SSE Events:**

On connect and on change, server emits:
```json
{"type": "preference", "key": "thinking", "value": true}
```

Bootstrap Icons required: `bi-brain` from Bootstrap Icons CDN.

### Telegram

**Thinking ON:**
```
⚙️ exec
`curl -s https://...`
✓ Completed (234ms)

⚙️ read
`/tmp/driveway2.jpg`
✓ Completed

Here's what I found...
```

Or using spoiler tags (user taps to reveal):
```
||⚙️ exec: curl -s https://...||

Here's what I found...
```

**Thinking OFF:**
- Only send final text response
- Tool calls happen silently

### TUI

**Thinking ON:**
```
[dim]⚙ exec: curl -s https://...[/dim]
[dim]  ✓ completed (234ms)[/dim]
[dim]⚙ read: /tmp/driveway2.jpg[/dim]
[dim]  ✓ completed[/dim]

Here's what I found...
```

**Thinking OFF:**
- Only show final response

## Storage

Add to user config:

```json
{
  "users": {
    "rodent": {
      "preferences": {
        "showThinking": false
      }
    }
  }
}
```

Or session-level:

```go
type Session struct {
    // ...
    Preferences SessionPrefs `json:"preferences"`
}

type SessionPrefs struct {
    ShowThinking bool `json:"showThinking"`
}
```

## Gateway Implementation

```go
func (g *Gateway) handleToolUse(ctx context.Context, tool ToolCall) {
    session := getSession(ctx)
    
    // Always emit events - channels filter based on preference
    g.emit(ctx, Event{
        Type: "tool_start",
        ID:   tool.ID,
        Tool: tool.Name,
        Input: truncate(tool.Input, 200),
    })
    
    result, err := g.executeTool(ctx, tool)
    
    if err != nil {
        g.emit(ctx, Event{
            Type:  "tool_error",
            ID:    tool.ID,
            Tool:  tool.Name,
            Error: err.Error(),
        })
    } else {
        g.emit(ctx, Event{
            Type:     "tool_end",
            ID:       tool.ID,
            Tool:     tool.Name,
            Status:   "completed",
            Duration: duration.Milliseconds(),
            Preview:  truncate(result, 100),
        })
    }
}
```

## Command Handler

```go
func (g *Gateway) handleThinkingCommand(ctx context.Context, args string) string {
    session := getSession(ctx)
    
    switch strings.ToLower(strings.TrimSpace(args)) {
    case "on":
        session.Preferences.ShowThinking = true
        return "Thinking output enabled. You'll now see tool calls and working output."
    
    case "off":
        session.Preferences.ShowThinking = false
        return "Thinking output disabled. You'll only see final responses."
    
    case "toggle":
        session.Preferences.ShowThinking = !session.Preferences.ShowThinking
        if session.Preferences.ShowThinking {
            return "Thinking output enabled."
        }
        return "Thinking output disabled."
    
    case "", "status":
        if session.Preferences.ShowThinking {
            return "Thinking output is currently ON."
        }
        return "Thinking output is currently OFF."
    
    default:
        return "Usage: /thinking [on|off|toggle]"
    }
}
```

## Security Considerations

1. **Sensitive data in tool args**: Some tool calls contain secrets (API tokens, passwords)
   - Consider redacting known sensitive patterns
   - Or warn users that thinking mode may expose secrets

2. **Long outputs**: Tool results can be huge
   - Always truncate in events
   - Collapsible UI for full output

## Implementation Phases

### Phase 1: Core
- [ ] SSE events for tool_start, tool_end, tool_error
- [ ] /thinking command handler
- [ ] User preference storage
- [ ] HTTP frontend conditional rendering

### Phase 2: Channels
- [ ] Telegram formatting (code blocks or spoilers)
- [ ] TUI dimmed output
- [ ] Toggle button in HTTP UI

### Phase 3: Polish
- [ ] Collapsible long outputs
- [ ] Timing display
- [ ] Sensitive data redaction
- [ ] Extended thinking support (Claude thinking models)

## See Also

- [HTTP_API.md](./HTTP_API.md) - SSE event streaming
- [Session management](../docs/session-management.md) - User preferences
