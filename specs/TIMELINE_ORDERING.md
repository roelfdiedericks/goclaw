# Timeline Event Ordering Specification

## Overview

Define consistent chronological ordering of events across all channels. Chat is a timeline — events should display in the order they actually occurred.

## Principle

**Timeline is truth.** All channels should render events in chronological order:

1. Tool call starts → show tool call
2. Tool call completes → show result
3. Agent responds → show response

This matches reality: the agent does work, *then* responds based on that work.

## Current State

### Telegram ⚠️ (Intermittent)
```
# Sometimes WRONG (response before tools):
01:22  Weather: Sunny, 30°C, 35% humidity ☀️   ← Response arrives FIRST
01:23  ⚙️ exec                                  ← Tool call arrives AFTER
01:23  ✓ Completed (11ms)

# Sometimes CORRECT (tools before response):
01:30  ⚙️ edit                                  ← Tool call FIRST
01:30  ✓ Completed (13ms)
01:30  Updated the TIMELINE_ORDERING.md...      ← Response AFTER
```

**Behavior is intermittent** — sometimes correct, sometimes wrong. Both examples had fast tool execution (~11-13ms), so duration alone doesn't explain it.

**Possible causes:**
- Race condition in async message sending
- No guaranteed ordering between tool events and response text
- Telegram API batching/timing differences
- Order depends on which coroutine/goroutine finishes first

**Investigation needed:**
- Add timestamps to logging for tool events vs response send
- Check if events are sent via same channel or separate paths
- Verify SSE event emission order matches Telegram send order

### HTTP ❌ (Incorrect)
Currently renders response bubble with tool calls collapsed *below* the response:

```
16:54  Weather: Sunny, 30°C, 35% humidity ☀️
       ┌─────────────────────────────┐
       │ ⚙️ exec (collapsed)         │
       └─────────────────────────────┘
```

This is visually clean but temporally backwards.

## Target State

### HTTP (Fixed)
Should match Telegram — tool events in timeline, before response:

```
16:54  ⚙️ exec
       {"command":"curl -s \"wttr.in/...\""}
       ✓ Completed (2744ms)
       STDOUT: Sunny +30°C 35%

16:54  Weather: Sunny, 30°C, 35% humidity ☀️
```

## Implementation

### SSE Event Stream

Events are already emitted in correct order:

```json
{"type": "tool_start", "tool": "exec", "input": "curl..."}
{"type": "tool_end", "tool": "exec", "duration_ms": 2744, "output": "Sunny..."}
{"type": "text", "content": "Weather: Sunny, 30°C..."}
```

No change needed to backend.

### HTTP Frontend

**Current (wrong):** Buffer tool events, render inside/below response bubble.

**Target (correct):** Render tool events immediately as separate timeline entries.

```javascript
eventSource.addEventListener('message', (e) => {
    const data = JSON.parse(e.data);
    
    switch (data.type) {
        case 'tool_start':
            // Render immediately in timeline
            appendToolStart(data);
            break;
            
        case 'tool_end':
            // Update the tool entry with result
            updateToolEnd(data);
            break;
            
        case 'text':
            // Render response as new timeline entry
            appendAssistantMessage(data.content);
            break;
    }
});
```

### Styling

Tool events should be visually distinct from chat messages but still timeline entries:

```css
/* Tool events - lighter, monospace, indented slightly */
.timeline-tool {
    background: #f8f9fa;
    border-left: 3px solid #6c757d;
    padding: 8px 12px;
    margin: 4px 0 4px 20px;
    font-family: monospace;
    font-size: 0.85em;
}

/* Chat messages - normal bubbles */
.timeline-message {
    /* existing chat bubble styles */
}
```

### Visual Hierarchy

Tool events should feel like "supporting detail" while responses are primary:

```
┌─────────────────────────────────────────┐
│ 16:54                                   │
│                                         │
│     ⚙️ exec                             │  ← subdued, smaller
│     curl -s "wttr.in/..."               │
│     ✓ 2744ms | Sunny +30°C 35%          │
│                                         │
│ ┌─────────────────────────────────┐     │
│ │ Weather: Sunny, 30°C, 35%  ☀️   │     │  ← primary bubble
│ │ humidity in Johannesburg.       │     │
│ └─────────────────────────────────┘     │
└─────────────────────────────────────────┘
```

## Channel Summary

| Channel | Tool Events | Order | Notes |
|---------|-------------|-------|-------|
| HTTP | Timeline entries (styled cards) | Chronological | Tool → Result → Response |
| Telegram | Separate messages | Chronological | Already correct |
| TUI | Dimmed text lines | Chronological | Tool → Result → Response |

## Thinking Toggle Interaction

When thinking is **OFF**:
- Tool events still occur but are not rendered
- Only `text` events appear in timeline
- User sees clean chat without implementation details

When thinking is **ON**:
- All events render in timeline order
- Tools before responses (chronological truth)

## Edge Cases

### Multiple Tool Calls
```
⚙️ exec (weather)
✓ Completed
⚙️ exec (calendar)
✓ Completed
⚙️ read (file)
✓ Completed

"Here's your briefing: Weather is sunny..."
```

All tools in order, then final response.

### Parallel Tool Calls
If tools run in parallel, show them in the order results arrive:
```
⚙️ weather started
⚙️ calendar started
✓ calendar completed (faster)
✓ weather completed

"Here's what I found..."
```

### Streaming Response
Response streams after all tools complete:
```
⚙️ exec completed

"Weather: Sunny..." [streaming...]
```

The response bubble appears and grows as tokens stream in.

## Summary

- Chat = timeline
- Timeline = chronological
- All channels follow same order: **Tool → Result → Response**
- Only difference is *styling*, not *sequence*
