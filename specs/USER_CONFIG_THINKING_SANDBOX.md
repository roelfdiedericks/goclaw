# User Config: Thinking & Sandbox Flags

## Overview

Add per-user configuration flags:
1. **`thinking`** — Default state for `/thinking` toggle (show tool calls and reasoning)
2. **`sandbox`** — Restrict user to sandboxed file operations

## Configuration

**`users.json`:**

```json
{
  "rodent": {
    "name": "TheRoDent",
    "role": "owner",
    "telegram_id": "123456789",
    "http_password_hash": "...",
    "thinking": true,
    "sandbox": false
  },
  "guest": {
    "name": "Guest User",
    "role": "user",
    "http_password_hash": "...",
    "thinking": false,
    "sandbox": true
  }
}
```

## Fields

### `thinking`

| Property | Value |
|----------|-------|
| Type | `bool` (optional) |
| Default | `true` for owner, `false` for others |
| Effect | Initial state of `/thinking` toggle for new sessions |

Sets the default for `ShowThinking` when a user starts a new session. Users can still toggle with `/thinking on|off` during the session. This just controls the starting state.

When enabled, users see:
- Tool calls (start/end)
- Reasoning/thinking content from models that provide it
- Working output during agent runs

### `sandbox`

| Property | Value |
|----------|-------|
| Type | `bool` (optional) |
| Default | `false` for owner, `true` for others |
| Effect | Restrict file tools to workspace root |

**Current restrictions (when `sandbox: true`):**
- File tools (read/write/edit) restricted to workspace root
- Cannot access paths outside workspace via symlinks or `..`

**Planned future restrictions:**
- Exec tool seccomp filtering (see `EXEC_SANDBOX.md`)
- Chroot isolation
- Network restrictions

## Implementation

### 1. Update UserEntry struct

```go
// internal/config/users.go
type UserEntry struct {
    Name             string `json:"name"`
    Role             string `json:"role"`
    TelegramID       string `json:"telegram_id,omitempty"`
    HTTPPasswordHash string `json:"http_password_hash,omitempty"`
    Thinking         *bool  `json:"thinking,omitempty"`  // nil = use role default
    Sandbox          *bool  `json:"sandbox,omitempty"`   // nil = use role default
}
```

### 2. Update User struct

```go
// internal/user/user.go
type User struct {
    ID               string
    Name             string
    Role             Role
    TelegramID       string
    HTTPPasswordHash string
    Permissions      map[string]bool
    Thinking         bool  // resolved from config or role default
    Sandbox          bool  // resolved from config or role default
}
```

### 3. Apply defaults when loading

```go
// internal/config/users.go in LoadUsers()
func applyDefaults(entry *UserEntry) {
    isOwner := entry.Role == "owner"
    
    if entry.Thinking == nil {
        val := isOwner  // true for owner, false for others
        entry.Thinking = &val
    }
    if entry.Sandbox == nil {
        val := !isOwner  // false for owner, true for others
        entry.Sandbox = &val
    }
}
```

### 4. Copy fields in user registry

```go
// internal/user/registry.go in NewRegistryFromUsers()
user := &User{
    ID:               username,
    Name:             entry.Name,
    Role:             Role(entry.Role),
    TelegramID:       entry.TelegramID,
    HTTPPasswordHash: entry.HTTPPasswordHash,
    Thinking:         *entry.Thinking,  // already defaulted
    Sandbox:          *entry.Sandbox,   // already defaulted
}
```

### 5. Initialize HTTP session from user preference

```go
// internal/http/channel.go in GetSession()
func (c *HTTPChannel) GetSession(sessionID string) *HTTPSession {
    // ... existing lookup ...
    
    // Create new session
    sess := &HTTPSession{
        ID:           sessionID,
        ShowThinking: false, // default
    }
    
    // Apply user preference if available
    if user := c.getSessionUser(sessionID); user != nil {
        sess.ShowThinking = user.Thinking
    }
    
    c.sessions[sessionID] = sess
    return sess
}
```

### 6. Initialize Telegram prefs from user preference

```go
// internal/telegram/bot.go in getChatPrefs()
func (b *Bot) getChatPrefs(chatID int64) *ChatPreferences {
    if prefs, ok := b.chatPrefs.Load(chatID); ok {
        return prefs.(*ChatPreferences)
    }
    
    // Default
    prefs := &ChatPreferences{ShowThinking: false}
    
    // Apply user preference if available
    if user := b.getUserForChat(chatID); user != nil {
        prefs.ShowThinking = user.Thinking
    }
    
    b.chatPrefs.Store(chatID, prefs)
    return prefs
}
```

### 7. Check sandbox in file tools

```go
// internal/tools/read.go (similar for write.go, edit.go)
func (t *ReadTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
    // Get user from context
    sessCtx := tools.GetSessionContext(ctx)
    sandboxed := true // default to safe
    if sessCtx != nil && sessCtx.User != nil {
        sandboxed = sessCtx.User.Sandbox
    }
    
    if sandboxed {
        // Use existing sandbox validation
        content, err := sandbox.ReadFile(params.Path, t.workingDir, t.workspaceRoot)
    } else {
        // Resolve path but allow any location
        resolved := resolvePath(params.Path, t.workingDir)
        content, err := os.ReadFile(resolved)
    }
}
```

## Migration

Existing `users.json` files without these fields will use role-based defaults:
- Owner: `thinking: true`, `sandbox: false`
- User/Guest: `thinking: false`, `sandbox: true`

No manual migration required.

## Security Notes

- Sandbox is defense-in-depth, not a security boundary
- Owner with `sandbox: false` has full filesystem access
- For untrusted users, combine with OS-level isolation (containers, VMs)
