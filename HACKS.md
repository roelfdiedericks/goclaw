# GoClaw Architectural Hacks

This file documents architectural shortcuts and hacks that exist in the codebase. These are known technical debt items that work but violate clean architecture principles.

---

## SendAgentResponse - Direct Channel Injection

**Location:** `gateway.Channel` interface, implemented in:
- `internal/http/channel.go`
- `internal/telegram/bot.go`
- `internal/tui/tui.go`

**What it does:**

`SendAgentResponse(ctx, user, response)` bypasses the normal event flow and injects a response directly into a user's channel(s). It manually sends typing indicators and the final response, mimicking what would happen if events flowed through the normal `RunAgent` → events → channel pipeline.

**Why it exists:**

The supervision feature (guidance + ghostwriting) needed to:
1. Let a supervisor inject guidance into a user's session
2. Trigger an agent response to that guidance
3. Deliver the response to the supervised user's channels (Telegram, HTTP, etc.)

Both features use the same hack:
- **Guidance**: `handleSessionGuidance` → `RunAgentForSession` → LLM responds → `sendAgentResponseToUser` → `SendAgentResponse` on each channel
- **Ghostwriting**: `handleSessionMessage` → `DeliverMessageToUser` → `sendAgentResponseToUser` → `SendAgentResponse` on each channel

The only difference is ghostwriting skips the LLM and delivers the supervisor's message directly.

The problem: When `RunAgentForSession` runs, the events go through the supervision event channel (so the supervisor sees them), but there's no mechanism to route those same events to the supervised user's actual channels.

Normal flow:
```
User sends message → Channel receives → RunAgent → Events → Same channel renders
```

Supervision flow:
```
Supervisor sends guidance → RunAgentForSession → Events → Supervision SSE (supervisor sees)
                                                      ↓
                                          User's channels see... nothing
```

**The hack:**

At the end of `RunAgent`, if the request source is "supervision", we call `sendAgentResponseToUser()` which iterates through all channels and calls `SendAgentResponse()` on each one that serves the target user.

Each channel's `SendAgentResponse` implementation:
- **HTTP:** Sends `start` event (typing indicator), then `done` event (response)
- **Telegram:** Sends `tele.Typing` action, then the message
- **TUI:** Just sends the message (no typing concept)

**Why this is poor architecture:**

1. **Duplicated logic:** Each channel reimplements typing + response delivery instead of reusing the event flow
2. **Bypasses the event system:** Events are the canonical way responses flow, but this sidesteps them entirely
3. **Tight coupling:** The gateway now knows about channel-specific delivery instead of just emitting events
4. **Inconsistent behavior:** The supervised user doesn't see streaming, tool calls, thinking - just the final response
5. **Maintenance burden:** Any change to how responses are delivered needs updating in multiple places

**Proper solution would be:**

Route the events from `RunAgent` to BOTH the supervision channel AND the supervised user's channels. This would require:
1. A pub/sub system for session events
2. Channels subscribing to events for sessions they care about
3. Events flowing through one path, rendered by multiple consumers

This was deemed too complex for the initial implementation, so we went with the direct injection hack.

**Date added:** 2026-02-06
**Related files:**
- `internal/gateway/gateway.go` - `sendAgentResponseToUser()`
- `internal/gateway/channel.go` - `Channel` interface
- `internal/http/supervision.go` - Supervision handlers
- `internal/session/supervision.go` - `SupervisionState` with event channel

---

*Add more hacks here as they accumulate.*
