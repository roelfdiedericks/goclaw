# GoClaw Development Guidelines

## Logging Standards

All code in GoClaw MUST include appropriate logging to aid debugging and observability. Use the logging package with dot import for convenience:

```go
import . "github.com/roelfdiedericks/goclaw/internal/logging"
```

### Log Levels

**L_trace** - Very detailed, low-level information
- Individual iterations, data transformations
- File contents being read/written
- Only visible with `-t` flag
- Example: `L_trace("parsing json field", "field", name, "value", val)`

**L_debug** - Information useful for debugging
- Function entry/exit with key parameters
- Configuration values being used
- External API calls (request/response summaries)
- State changes and decisions
- Visible with `-d` flag
- Example: `L_debug("sending request to Anthropic", "model", model, "messages", len(msgs))`

**L_info** - Normal operational information
- Service startup/shutdown
- Significant events (user authenticated, agent run started/completed)
- Always visible
- Example: `L_info("telegram bot started", "username", bot.Username)`

**L_warn** - Potential issues that don't stop execution
- Unknown users attempting access
- Retryable errors
- Deprecated feature usage
- Example: `L_warn("unknown telegram user ignored", "userID", id)`

**L_error** - Errors that affect functionality
- Failed API calls
- Configuration errors
- Tool execution failures
- Example: `L_error("failed to send message", "error", err)`

### Required Logging Points

Every package MUST log:

1. **Initialization**: Log when the component is created with key config values
2. **External calls**: Log before/after any external API or system call
3. **State changes**: Log significant state transitions
4. **Errors**: Always log errors with context
5. **User actions**: Log user-initiated actions with user identity

### Format Guidelines

- Use structured logging with key-value pairs: `L_info("message", "key1", val1, "key2", val2)`
- Keep messages lowercase and concise
- Include relevant IDs (runID, userID, sessionID) for correlation
- For sensitive data, log length not content: `"tokenLength", len(token)`
- Prefix with package/component: `"config: loading file"`, `"telegram: user authenticated"`

### Example Implementation

```go
func (b *Bot) handleMessage(c tele.Context) error {
    userID := fmt.Sprintf("%d", c.Sender().ID)
    
    L_debug("telegram: message received", "userID", userID, "chatID", c.Chat().ID)
    
    user := b.users.FromIdentity("telegram", userID)
    if user == nil {
        L_warn("telegram: unknown user ignored", "userID", userID)
        return nil
    }
    
    L_info("telegram: processing message", "user", user.Name, "role", user.Role)
    
    result, err := b.process(c.Text())
    if err != nil {
        L_error("telegram: processing failed", "error", err, "userID", userID)
        return err
    }
    
    L_debug("telegram: message processed", "responseLength", len(result))
    return nil
}
```

## Code Style

- Use meaningful variable names
- Keep functions focused and small
- Document exported functions and types
- Handle errors explicitly, never ignore them silently
- Use context.Context for cancellation and timeouts

---

## Session & Memory Management

### The Compaction Problem

Long conversations fill up the model's context window. When it overflows, compaction summarizes and truncates older messages. **If important context wasn't written to memory files before compaction, it's lost forever.**

OpenClaw's approach fires a single memory flush prompt close to the limit (~96% context). By then it may be too late — the agent has amnesia before it can save.

### GoClaw's Solution: Proactive Memory Prompts

GoClaw must track context usage and prompt for memory writes at **multiple thresholds**:

| Threshold | Behavior |
|-----------|----------|
| 50% | Soft reminder: "Consider noting key decisions" |
| 75% | Stronger: "Write important context to memory now" |
| 90% | Urgent: "Compaction imminent. Save critical context." |

This gives the agent time to save context before it's lost.

### Implementation Requirements

See [specs/SESSION_PERSISTENCE.md](specs/SESSION_PERSISTENCE.md) for full details.

**Must implement:**

1. **Token tracking** — Count tokens in session, know the model's context window
2. **Threshold checking** — After each turn, check if thresholds crossed
3. **System prompt injection** — Show context usage: `[Context: 156k/200k (78%)]`
4. **Flush prompts** — Inject memory write prompts at thresholds
5. **Threshold state** — Track which thresholds fired (reset after compaction)

**Key interfaces:**

```go
type ContextStats struct {
    UsedTokens    int
    MaxTokens     int
    UsagePercent  float64
}

func (g *Gateway) GetContextStats(sessionKey string) ContextStats
func (g *Gateway) checkMemoryFlush(sess *Session)  // Call after each turn
```

### Why This Matters

An agent losing context mid-session and asking "what were we talking about?" is a bad experience. The agent should:

1. See context pressure building (via system prompt or tool)
2. Get proactive prompts to save important context
3. Never be surprised by compaction

This is a **core feature**, not an afterthought. Build it into the session manager from the start.
