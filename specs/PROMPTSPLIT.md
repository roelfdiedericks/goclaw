# Prompt Split: Static vs Dynamic Content

**Status:** Draft  
**Date:** 2026-02-14  
**Context:** Discovered during xAI provider planning

## Problem

The current system prompt includes dynamic content (time, channel, model) that changes every turn. This defeats prompt caching for all providers:

- **Anthropic:** Cache invalidated when system prompt changes
- **xAI:** `previous_response_id` inherits system prompt from first request — time becomes stale
- **OpenAI:** Similar caching implications

## What's Actually Dynamic?

| Item | Changes when? | Needs live? | Current location |
|------|---------------|-------------|------------------|
| Time | Every turn | Yes | `buildTimeSection()` in system prompt |
| Channel | Per message | Maybe | `buildRuntimeSection()` in system prompt |
| Model | Per session | No | `buildRuntimeSection()` in system prompt |
| Hostname/OS | Never | No | `buildRuntimeSection()` in system prompt |

**Only time truly needs to be dynamic every turn.** Channel might change mid-session (telegram → tui) but is usually stable.

## Current Architecture

```
BuildSystemPrompt() → single system prompt string
  ├── Identity (static)
  ├── Skills (static-ish, changes on skill reload)
  ├── Memory/Workspace (static-ish)
  ├── Time section (DYNAMIC - changes every turn)
  └── Runtime section (DYNAMIC - time, channel, model)
```

All of this goes into the system prompt, defeating caching.

## Proposed Options

### Option 1: Time in User Message Prefix (Recommended)

```
System: [static identity, skills, workspace - cacheable]

User: [2024-02-14 14:00 SAST] turn on the lights
```

- Simple, works with all providers
- LLM understands timestamp context naturally
- Keeps system prompt static and cacheable
- Minor pollution of user message

### Option 2: Provider-Specific Handling

- **Anthropic:** Use cache breakpoints — static cached, dynamic appended
- **xAI:** Static in system prompt, dynamic in new messages
- **OpenAI:** Similar

More complex, but optimized per provider.

### Option 3: Time Tool

```go
// Agent calls get_time when needed
func (t *TimeTool) Execute(ctx, input) string {
    return time.Now().Format("2006-01-02 15:04:05 MST")
}
```

- No prompt pollution
- Tokens only when needed
- Risk: Agent might not call it for implicit time-sensitive tasks

### Option 4: Clear Separation in System Prompt

```
## Identity
[static - cacheable]

## Runtime Context (refreshed each turn)
Time: 2024-02-14 14:00 SAST
```

Still defeats caching because system prompt changes.

## Questions to Resolve

1. **Is channel/model info useful to the agent?** Could we remove it entirely?

2. **How to handle Anthropic cache breakpoints?** Does GoClaw already support this?

3. **Should time format be configurable?** Different contexts might need different granularity.

4. **What about timezone handling?** Currently uses system timezone, `UserTimezone` param is unused.

## Implementation Considerations

### For Option 1 (Time in User Message)

```go
// In gateway, before adding user message
timestamp := time.Now().Format("2006-01-02 15:04")
userMsgWithTime := fmt.Sprintf("[%s] %s", timestamp, req.UserMsg)
sess.AddUserMessage(userMsgWithTime, req.Source)
```

### Changes to BuildSystemPrompt

- Remove `buildTimeSection()` from system prompt
- Keep `buildRuntimeSection()` but remove time from it (or remove entirely)
- Static parts remain: identity, skills, memory, workspace

## Impact

- **Token savings:** Significant — system prompt cached instead of resent
- **Anthropic:** Better cache hit rate
- **xAI:** Enables efficient `previous_response_id` usage with current time
- **All providers:** Reduced token usage per turn

## Related

- [XAI_PROVIDER.md](./XAI_PROVIDER.md) — xAI integration where this was discovered
- `internal/context/prompt.go` — Current prompt building logic
- `internal/gateway/gateway.go` — Where BuildSystemPrompt is called
