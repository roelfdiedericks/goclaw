---
description: 
alwaysApply: true
---

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

## CHANGELOG.md updates
When make changelog updates, follow the similar simple, terse style as the existing CHANGELOG.md entries
Don't overword things, its a brief changelog not a dissertation. Do not update prior release changelog entries unless requested, add new entries under the "Unreleased" section

## Documentation updates
Follow the docs/AGENTS.md guide for changing files in the docs/ subdirectory. Ensure documentation updates are user-facing and not overly technical.


