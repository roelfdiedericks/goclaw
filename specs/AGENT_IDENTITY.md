# Agent Identity Specification

## Overview

Replace hardcoded "Assistant", "Agent", "Human" labels throughout GoClaw with configurable identity. The agent has a name (Ratpup), an emoji (🐀), and personality — the code should reflect that.

## Problem

Currently hardcoded in multiple places:

| Location | Current | Should Be |
|----------|---------|-----------|
| `transcript/indexer.go:298-300` | `"Human"` / `"Assistant"` | `"RoDent"` / `"Ratpup"` |
| `tui/tui.go:232,323,365` | `"Assistant: "` | `"Ratpup: "` or `"🐀 "` |
| `telegram/bot.go:913,930,946` | `"Assistant:"` | `"Ratpup:"` |
| `http/html/chat.html:411` | `"Agent is typing..."` | `"Ratpup is typing..."` |

## Configuration

Add to `goclaw.json`:

```json
{
  "identity": {
    "name": "Ratpup",
    "emoji": "🐀",
    "typing": "Ratpup is typing..."
  }
}
```

### Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | `"Assistant"` | Agent's display name |
| `emoji` | string | `""` | Optional emoji prefix |
| `typing` | string | `"{name} is typing..."` | Typing indicator text |

## Implementation

### Config Struct

```go
type IdentityConfig struct {
    Name   string `json:"name"`
    Emoji  string `json:"emoji"`
    Typing string `json:"typing"`
}

func (c *IdentityConfig) DisplayName() string {
    if c.Emoji != "" {
        return c.Emoji + " " + c.Name
    }
    return c.Name
}

func (c *IdentityConfig) TypingText() string {
    if c.Typing != "" {
        return c.Typing
    }
    return c.Name + " is typing..."
}
```

### Usage

**Transcript indexer:**
```go
func (idx *Indexer) buildChunkContent(messages []*Message) string {
    agentName := idx.config.Identity.Name  // "Ratpup"
    
    for _, msg := range messages {
        var label string
        if role == "user" {
            label = msg.UserName  // Use actual username if available, else "You"
        } else if role == "assistant" {
            label = agentName
        }
        parts = append(parts, fmt.Sprintf("%s: %s", label, cleaned))
    }
}
```

**TUI:**
```go
// In render
agentPrefix := assistantStyle.Render(m.config.Identity.DisplayName() + ": ")
m.currentLine = agentPrefix
```

**Telegram mirror:**
```go
mirror := fmt.Sprintf("📱 <b>%s</b>\n\n<b>You:</b> %s\n\n<b>%s:</b> %s",
    source,
    userMsg,
    t.config.Identity.Name,  // "Ratpup"
    response,
)
```

**HTTP typing indicator:**
```javascript
// Inject from server-side template
const agentName = "{{.Identity.Name}}";  // "Ratpup"
const typingText = "{{.Identity.Typing}}";  // "Ratpup is typing..."

// In SSE handler
typingIndicator.html(`<span class="dots">...</span> ${typingText}`);
```

Or pass via SSE event:
```json
{"type": "config", "identity": {"name": "Ratpup", "emoji": "🐀"}}
```

## User Labels

For the human side:

| Context | Label |
|---------|-------|
| Owner in transcript | Use username from session (`"RoDent"`) |
| Other users | Use their username (`"Alice"`) |
| Generic/unknown | `"You"` |

**In transcript chunks:**
```
RoDent: when last did you bug me about sleep?

Ratpup: Found it! 03:38 SAST this morning...
```

**In mirrors/displays:**
```
You: when last did you bug me about sleep?

Ratpup: Found it! 03:38 SAST this morning...
```

## Migration

1. Add `IdentityConfig` to main config struct with defaults
2. Update each hardcoded location to use config
3. Pass identity through to components that need it (indexer, channels, etc.)

### Files to Update

- [ ] `internal/config/config.go` — Add IdentityConfig struct
- [ ] `internal/transcript/indexer.go` — Use identity.Name instead of "Assistant"
- [ ] `internal/tui/tui.go` — Use identity for display prefix
- [ ] `internal/telegram/bot.go` — Use identity in mirror messages
- [ ] `internal/http/html/chat.html` — Inject identity for typing indicator
- [ ] `internal/http/handlers.go` — Pass identity to templates

## Why Not Parse IDENTITY.md?

Could optionally parse IDENTITY.md for defaults, but:

- IDENTITY.md is freeform markdown, fragile to parse
- Config is explicit, type-safe
- IDENTITY.md is for agent's self-knowledge (loaded into context)
- Config is for system behavior (code references)

**Recommendation:** Keep them separate. Config for code, IDENTITY.md for soul.

## Examples

### Before
```
Human: what time is it?
Assistant: It is 3:30pm

Agent is typing...
```

### After
```
RoDent: what time is it?

Ratpup: It is 3:30pm

🐀 Ratpup is typing...
```

Much more personal. This is a conversation with Ratpup, not "Assistant".

## Summary

| Before | After |
|--------|-------|
| `Human:` | `RoDent:` / `Alice:` / `You:` |
| `Assistant:` | `Ratpup:` |
| `Agent is typing...` | `Ratpup is typing...` |

One config block, multiple touchpoints. Personality everywhere.

