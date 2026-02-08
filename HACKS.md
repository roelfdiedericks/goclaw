# GoClaw Architectural Hacks

This file documents architectural shortcuts and hacks that exist in the codebase. These are known technical debt items that work but violate clean architecture principles.

---

## ~~SendAgentResponse - Direct Channel Injection~~ (RESOLVED)

**Status:** Resolved on 2026-02-06

**Original problem:**

The supervision feature (guidance + ghostwriting) needed to deliver responses to supervised users' channels, but `RunAgent` events only go to whoever called it. This led to a hack where `SendAgentResponse` bypassed the normal event flow and manually sent fake `start`/`done` events to channels.

**Solution implemented:**

Unified message injection architecture via `Channel.InjectMessage`:

```go
// Channel interface
InjectMessage(ctx context.Context, u *user.User, sessionKey, message string, invokeLLM bool) error
```

- **Guidance (invokeLLM=true):** Each channel runs `RunAgent` through its own event handling, streaming to the user
- **Ghostwrite (invokeLLM=false):** Each channel sends typing indicator, waits, then delivers message

The key insight: instead of hacking delivery after the fact, injection happens *through* the channel infrastructure, so events flow normally.

**What was removed:**
- `SendAgentResponse` method from Channel interface and all implementations
- `sendAgentResponseToUser()` in gateway
- `DeliverMessageToUser()` in gateway  
- `if req.Source == "supervision"` block in RunAgent
- `agent_response` event handler in chat.html

**Configuration added:**
```json
{
  "supervision": {
    "guidance": {
      "prefix": "[Supervisor]: "
    },
    "ghostwriting": {
      "typingDelayMs": 500
    }
  }
}
```

**Related spec:** `specs/GUIDANCE_ARCHITECTURE.md`

---

*Add more hacks here as they accumulate.*
