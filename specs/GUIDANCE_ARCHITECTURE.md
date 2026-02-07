# Unified Message Injection Architecture

## Problem Statement

When a supervisor sends guidance or ghostwrites to a user's session, the response should be **indistinguishable** from a normal agent response - the user should see:
1. Typing indicator while the agent is thinking
2. Streaming text as it generates
3. Tool calls if any
4. Final response

Currently, this doesn't work because of a fundamental architectural flaw.

### Current Broken Architecture

```
Normal User Message Flow:
┌─────────┐    ┌─────────────┐    ┌──────────┐    ┌─────────────┐
│ Channel │───>│ RunAgent()  │───>│ Events   │───>│ Same Channel│
│ (HTTP)  │    │             │    │ Channel  │    │ renders SSE │
└─────────┘    └─────────────┘    └──────────┘    └─────────────┘
     │                                                   │
     └───────────────── Same channel ────────────────────┘

Supervision Flow (BROKEN):
┌────────────┐    ┌───────────────────┐    ┌──────────────┐
│ Supervisor │───>│ RunAgentForSession│───>│ Events drain │
│ HTTP POST  │    │                   │    │ to nowhere   │
└────────────┘    └───────────────────┘    └──────────────┘
                           │
                           ▼
                  ┌─────────────────────┐
                  │ SendAgentResponse() │ ← HACK: sends fake start/done
                  │ after agent done    │   events, timing is wrong
                  └─────────────────────┘
```

The problem: Events from `RunAgent` go to whoever called it. For normal messages, that's the channel that will render them. For supervision, it's a goroutine that drains them to nowhere.

`SendAgentResponse` tries to fix this by sending fake `start` and `done` events after the agent completes, but:
- Events arrive milliseconds apart (no typing indicator time)
- No streaming - just instant start→done
- Completely bypasses the normal event flow
- Used by both guidance AND ghostwriting (same hack, duplicated problems)

## Solution: Unified Message Injection

Both guidance and ghostwriting are fundamentally the same operation: **injecting a message into a session and delivering it through normal channel infrastructure**.

| Operation | Message Role | LLM Involved | Delivery |
|-----------|--------------|--------------|----------|
| Guidance | User (with prefix) | Yes | Agent response streams to user's channels |
| Ghostwrite | Assistant | No | Message delivered directly to user's channels |

### Gateway API

```go
// InjectMessage injects a message into a user's session and delivers appropriately.
//
// If invokeLLM is true (guidance):
//   - Message is added as user message with configured prefix
//   - Agent run is triggered through user's channels
//   - Response streams to all user's active channels
//
// If invokeLLM is false (ghostwrite):
//   - Message is added as assistant message
//   - Delivered directly to all user's active channels
func (g *Gateway) InjectMessage(ctx context.Context, sessionKey, message string, invokeLLM bool) error
```

### Channel Interface

```go
type Channel interface {
    // ... existing methods ...
    
    // InjectMessage handles message injection for a user on this channel.
    //
    // If invokeLLM is true: triggers agent run, streams events through normal path
    // If invokeLLM is false: delivers message directly (typing indicator + message)
    InjectMessage(ctx context.Context, u *user.User, sessionKey, message string, invokeLLM bool) error
}
```

### HTTP Endpoints

Separate endpoints for clarity, both calling the same underlying method:

```go
// POST /api/session/{key}/guidance
// Body: {"message": "..."}
func (s *Server) handleSessionGuidance(w http.ResponseWriter, r *http.Request, sessionKey string) {
    var req struct { Message string `json:"message"` }
    // ...
    s.gw.InjectMessage(ctx, sessionKey, req.Message, true)  // invokeLLM=true
}

// POST /api/session/{key}/ghostwrite
// Body: {"message": "..."}
func (s *Server) handleSessionGhostwrite(w http.ResponseWriter, r *http.Request, sessionKey string) {
    var req struct { Message string `json:"message"` }
    // ...
    s.gw.InjectMessage(ctx, sessionKey, req.Message, false)  // invokeLLM=false
}
```

### Configuration

#### Go Types (internal/config/config.go)

```go
// SupervisionConfig configures session supervision features
type SupervisionConfig struct {
    Guidance     GuidanceConfig     `json:"guidance"`
    Ghostwriting GhostwritingConfig `json:"ghostwriting"`
}

// GuidanceConfig configures supervisor guidance injection
type GuidanceConfig struct {
    // Prefix prepended to guidance messages (default: "[Supervisor]: ")
    // The LLM sees this prefix and knows the message is from the supervisor
    Prefix string `json:"prefix"`
    
    // SystemNote is an optional system message injected with guidance (future use)
    // Could contain instructions like "Respond to this guidance naturally"
    SystemNote string `json:"systemNote,omitempty"`
}

// GhostwritingConfig configures supervisor ghostwriting
type GhostwritingConfig struct {
    // TypingDelayMs is the delay before delivering the message (default: 500)
    // Simulates natural typing so message doesn't appear instantly
    TypingDelayMs int `json:"typingDelayMs"`
}
```

Add to main `Config` struct:
```go
type Config struct {
    // ... existing fields ...
    Supervision  SupervisionConfig `json:"supervision"`
}
```

#### JSON Example (goclaw.json)

```json
{
  "supervision": {
    "guidance": {
      "prefix": "[Supervisor]: ",
      "systemNote": ""
    },
    "ghostwriting": {
      "typingDelayMs": 500
    }
  }
}
```

#### Defaults (in Load())

```go
Supervision: SupervisionConfig{
    Guidance: GuidanceConfig{
        Prefix:     "[Supervisor]: ",
        SystemNote: "",
    },
    Ghostwriting: GhostwritingConfig{
        TypingDelayMs: 500,
    },
},
```

#### Field Descriptions

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `supervision.guidance.prefix` | string | `"[Supervisor]: "` | Prefix added to guidance messages. The LLM sees this and knows it's from the supervisor. |
| `supervision.guidance.systemNote` | string | `""` | Optional system message injected with guidance (reserved for future use). |
| `supervision.ghostwriting.typingDelayMs` | int | `500` | Milliseconds to wait before delivering ghostwritten message. Simulates typing. |

### Flow Diagrams

```
gateway.InjectMessage(sessionKey, message, invokeLLM)
    │
    ├─ invokeLLM=true ──> session.AddUserMessage(prefix + message)
    │
    └─ invokeLLM=false ─> session.AddAssistantMessage(message)
    │
    ▼
For each channel serving this user:
    channel.InjectMessage(user, sessionKey, message, invokeLLM)
        │
        ├─ invokeLLM=true:
        │   └─> RunAgent with channel's event handler
        │       └─> Events stream to user (typing, deltas, tools, done)
        │
        └─ invokeLLM=false:
            └─> Send typing indicator
            └─> Wait typing_delay_ms
            └─> Deliver message
```

### Channel Implementations

**HTTPChannel.InjectMessage:**
```go
func (c *HTTPChannel) InjectMessage(ctx context.Context, u *user.User, sessionKey, message string, invokeLLM bool) error {
    // Find user's active SSE sessions
    sessions := c.getSessionsForUser(u)
    if len(sessions) == 0 {
        return nil  // No active sessions, nothing to deliver
    }
    
    if invokeLLM {
        // Create event channel, run agent, stream to all sessions
        events := make(chan gateway.AgentEvent, 100)
        go func() {
            for ev := range events {
                for _, sess := range sessions {
                    sess.SendEvent(convertToSSE(ev))
                }
            }
        }()
        return c.gw.RunAgent(ctx, gateway.AgentRequest{
            SessionKey: sessionKey,
            Source:     "http",
        }, events)
    } else {
        // Ghostwrite: deliver directly
        for _, sess := range sessions {
            sess.SendEvent(SSEEvent{Event: "start", Data: ...})
            time.Sleep(c.config.Supervision.Ghostwriting.TypingDelayMs)
            sess.SendEvent(SSEEvent{Event: "done", Data: ...})
        }
        return nil
    }
}
```

**TelegramChannel.InjectMessage:**
```go
func (b *Bot) InjectMessage(ctx context.Context, u *user.User, sessionKey, message string, invokeLLM bool) error {
    chatID := u.GetTelegramChatID()
    if chatID == 0 {
        return nil  // User not on Telegram
    }
    
    // Send typing indicator
    b.bot.Notify(&tele.Chat{ID: chatID}, tele.Typing)
    
    if invokeLLM {
        // Run agent, send response
        events := make(chan gateway.AgentEvent, 100)
        var finalText string
        go func() {
            for ev := range events {
                if done, ok := ev.(gateway.EventAgentEnd); ok {
                    finalText = done.FinalText
                }
            }
        }()
        if err := b.gw.RunAgent(ctx, ...); err != nil {
            return err
        }
        return b.SendText(chatID, finalText)
    } else {
        // Ghostwrite: deliver directly
        time.Sleep(b.config.Supervision.Ghostwriting.TypingDelayMs)
        return b.SendText(chatID, message)
    }
}
```

### What Gets Removed

- `SendAgentResponse` method on Channel interface
- `sendAgentResponseToUser` in gateway
- `DeliverMessageToUser` in gateway
- The hack in `RunAgent` that checks for "supervision" source
- Separate code paths for guidance vs ghostwriting delivery

### Benefits

1. **One concept**: Both operations are "injection" - unified mental model
2. **Real events**: Guidance uses channel's actual agent run path, not fake events
3. **Consistent UX**: User sees identical experience whether message came from them or supervisor
4. **Extensible config**: Easy to add more supervision settings later
5. **Clean separation**: Gateway handles session logic, channels handle delivery

### Migration Steps

1. Add `supervision` section to config schema
2. Add `InjectMessage` to Channel interface
3. Implement in HTTPChannel, TelegramChannel, TUIChannel
4. Add `InjectMessage` to Gateway
5. Update supervision HTTP handlers to call `gw.InjectMessage`
6. Remove old hack methods (`SendAgentResponse`, `sendAgentResponseToUser`, etc.)
7. Update HACKS.md (mark as resolved or remove)

---

## Cleanup: Hacks to Remove

### 1. Channel Interface - Remove `SendAgentResponse`

**File:** `internal/gateway/gateway.go` (line 40)
```go
// REMOVE from Channel interface:
SendAgentResponse(ctx context.Context, u *user.User, response string) error
```

### 2. HTTPChannel.SendAgentResponse

**File:** `internal/http/channel.go` (lines 146-195)

**Remove entirely.** This method sends fake `start`/`done` events with wrong timing:
```go
// REMOVE THIS ENTIRE METHOD:
func (c *HTTPChannel) SendAgentResponse(ctx context.Context, u *user.User, response string) error {
    // ... fake events with instant timing ...
}
```

### 3. TelegramBot.SendAgentResponse

**File:** `internal/telegram/bot.go` (lines 979-1001)

**Remove entirely.** This method sends typing + message directly:
```go
// REMOVE THIS ENTIRE METHOD:
func (b *Bot) SendAgentResponse(ctx context.Context, u *user.User, response string) error {
    // ... typing + send ...
}
```

### 4. TUIChannel.SendAgentResponse

**File:** `internal/tui/tui.go` (lines 705-714)

**Remove entirely:**
```go
// REMOVE THIS ENTIRE METHOD:
func (c *TUIChannel) SendAgentResponse(ctx context.Context, u *user.User, response string) error {
    // ... sends via SendMirror ...
}
```

### 5. Gateway.sendAgentResponseToUser

**File:** `internal/gateway/gateway.go` (lines 1330-1349)

**Remove entirely.** This is the dispatcher that calls `SendAgentResponse` on all channels:
```go
// REMOVE THIS ENTIRE FUNCTION:
func (g *Gateway) sendAgentResponseToUser(ctx context.Context, u *user.User, response string) {
    // ... iterates channels, calls SendAgentResponse ...
}
```

### 6. Gateway.DeliverMessageToUser

**File:** `internal/gateway/gateway.go` (lines 1351-1376)

**Remove entirely.** Used by ghostwriting, will be replaced by `InjectMessage`:
```go
// REMOVE THIS ENTIRE FUNCTION:
func (g *Gateway) DeliverMessageToUser(ctx context.Context, sessionKey string, message string) error {
    // ... resolves user, calls sendAgentResponseToUser ...
}
```

### 7. Gateway.RunAgent - Supervision Source Check

**File:** `internal/gateway/gateway.go` (lines 1144-1150)

**Remove the special case:**
```go
// REMOVE THIS BLOCK:
if req.Source == "supervision" {
    // Supervision-triggered run: send response directly to user's channels
    g.sendAgentResponseToUser(ctx, req.User, finalText)
} else {
    // Normal run: mirror to other channels
    g.mirrorToOthers(ctx, req, finalText)
}

// REPLACE WITH JUST:
g.mirrorToOthers(ctx, req, finalText)
```

### 8. SupervisionGateway Interface - Remove DeliverMessageToUser

**File:** `internal/http/supervision.go` (lines 39-41)

**Remove from interface:**
```go
// REMOVE:
DeliverMessageToUser(ctx context.Context, sessionKey string, message string) error
```

### 9. chat.html - Remove Supervision Hacks

**File:** `internal/http/html/chat.html`

#### 9a. Remove `agent_response` event handler (lines 437-444)
This was added to handle the fake direct delivery:
```javascript
// REMOVE THIS ENTIRE HANDLER:
eventSource.addEventListener('agent_response', function(e) {
    var data = JSON.parse(e.data);
    if (data.content) {
        appendMessage('assistant', data.content);
        saveHistory();
    }
});
```

#### 9b. Review `user_message` handler (lines 446-456)
This may still be needed for supervisor view - **keep but verify**:
```javascript
// KEEP - supervisor needs to see user messages in real-time
eventSource.addEventListener('user_message', function(e) {
    if (!isSupervising) return;
    // ...
});
```

#### 9c. Review debug-content rendering
The `debug-content` class logic for supervision is OK - it's just CSS toggling, not a hack.

---

## Summary of Removal

| Location | What | Lines |
|----------|------|-------|
| `gateway/gateway.go` | `SendAgentResponse` in Channel interface | ~40 |
| `gateway/gateway.go` | `sendAgentResponseToUser()` function | 1330-1349 |
| `gateway/gateway.go` | `DeliverMessageToUser()` function | 1351-1376 |
| `gateway/gateway.go` | `if req.Source == "supervision"` block | 1144-1150 |
| `http/channel.go` | `SendAgentResponse()` method | 146-195 |
| `telegram/bot.go` | `SendAgentResponse()` method | 979-1001 |
| `tui/tui.go` | `SendAgentResponse()` method | 705-714 |
| `http/supervision.go` | `DeliverMessageToUser` in interface | 39-41 |
| `http/html/chat.html` | `agent_response` event handler | 437-444 |

### Open Questions

1. What if user has no active channels? Skip silently, or queue for later?
2. Should supervisor see delivery confirmation (which channels received it)?
3. For Telegram, should we accumulate streaming and send one message, or send multiple updates?
