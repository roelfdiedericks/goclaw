# Agent Tools

GoClaw provides tools that the agent can use to interact with the system.

## Overview

Tools are executed when the LLM requests them via tool_use. Each tool:
1. Receives JSON input
2. Performs an action
3. Returns a result (text or error)

---

## File Tools

### `read`

Read file contents.

**Input:**
```json
{
  "path": "/path/to/file.txt",
  "offset": 0,
  "limit": 100
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `path` | string | Yes | File path (absolute or relative to workspace) |
| `offset` | int | No | Start line (0-indexed) |
| `limit` | int | No | Max lines to read |

**Output:** File contents with line numbers

**Security:**
- Path traversal protection (no `../` escape)
- Symlink protection
- Unicode normalization

---

### `write`

Write content to a file.

**Input:**
```json
{
  "path": "/path/to/file.txt",
  "content": "File contents here"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `path` | string | Yes | File path |
| `content` | string | Yes | Content to write |

**Output:** Success message

**Security:**
- Same protections as `read`
- Atomic writes (temp file + rename)
- Creates parent directories if needed

---

### `edit`

Edit a file using string replacement.

**Input:**
```json
{
  "path": "/path/to/file.txt",
  "old_string": "text to replace",
  "new_string": "replacement text"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `path` | string | Yes | File path |
| `old_string` | string | Yes | Text to find (must be unique) |
| `new_string` | string | Yes | Replacement text |

**Output:** Success message with change preview

**Validation:**
- `old_string` must exist in file
- `old_string` must be unique (exactly one match)
- `old_string` cannot be empty

---

## Execution Tools

### `exec`

Execute a shell command.

**Input:**
```json
{
  "command": "ls -la",
  "timeout": 30,
  "working_dir": "/path/to/dir"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `command` | string | Yes | Command to execute |
| `timeout` | int | No | Timeout in seconds (default: 30) |
| `working_dir` | string | No | Working directory |

**Output:** Command output (stdout + stderr)

**Security:**
- Commands run in workspace context
- Timeout prevents runaway processes
- Exit code included in output

---

## Communication Tools

### `message`

Send messages, react, edit, or delete in channels.

**Input (send):**
```json
{
  "action": "send",
  "text": "Hello!",
  "channel": "telegram",
  "chat_id": "123456"
}
```

**Input (react):**
```json
{
  "action": "react",
  "message_id": "789",
  "emoji": "👍"
}
```

**Input (edit):**
```json
{
  "action": "edit",
  "message_id": "789",
  "text": "Updated message"
}
```

**Input (delete):**
```json
{
  "action": "delete",
  "message_id": "789"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `action` | string | Yes | `send`, `react`, `edit`, `delete` |
| `text` | string | For send/edit | Message content |
| `channel` | string | No | Channel name (auto-detected from context) |
| `chat_id` | string | No | Chat ID (auto-detected from context) |
| `message_id` | string | For react/edit/delete | Target message ID |
| `emoji` | string | For react | Reaction emoji |
| `media_path` | string | No | Path to media file to send |

**Output:** Message ID or success confirmation

---

## Memory Tools

### `memory_search`

Search memory files semantically.

**Input:**
```json
{
  "query": "What did we discuss about authentication?"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `query` | string | Yes | Search query |

**Output:** Matching memory chunks with scores

**Requirements:** Memory search must be enabled in config.

See [Memory Search](./memory-search.md) for details.

---

### `memory_list`

List available memory files.

**Input:**
```json
{}
```

**Output:** List of memory files with metadata

---

## Tool Permissions

Tools can be restricted per-user in `users.json`:

```json
{
  "users": {
    "telegram:123456": {
      "name": "User",
      "roles": ["user"],
      "allowedTools": ["read", "memory_search"]
    }
  }
}
```

If `allowedTools` is not specified, all tools are allowed for that user.

---

## Tool Errors

Tools return errors in a structured format:

```json
{
  "error": "file not found: /path/to/missing.txt"
}
```

Common errors:

| Error | Cause |
|-------|-------|
| `file not found` | Path doesn't exist |
| `permission denied` | File not readable/writable |
| `path traversal detected` | Attempted `../` escape |
| `old_string is not unique` | Multiple matches in edit |
| `command timed out` | Exec exceeded timeout |

---

## Slash Commands

Slash commands are available in all channels (Telegram, TUI) for session management.

### `/status`

Show session status and compaction health.

**Output:**
```
Session Status
  Messages: 45
  Tokens: 125,000 / 200,000 (62.5%)
  Compactions: 2

Compaction Health
  Ollama: healthy (0/3 failures)
  Mode: normal
  Last attempt: 5 min ago
  Pending retries: 0
```

**When degraded:**
```
Compaction Health
  Ollama: degraded (3/3 failures)
  Mode: fallback to main model
  Reset in: 25 min
```

### `/compact`

Force context compaction immediately.

**Output:**
```
Compaction completed!
  Tokens before: 175,000
  Summary source: LLM
```

Summary source can be:
- `LLM` - Generated by Ollama/Anthropic
- `checkpoint` - Used existing checkpoint
- `fallback model` - Used main model (Ollama failed)
- `emergency truncation` - Both LLMs failed

### `/clear`

Clear conversation history and reset session.

### `/help`

Show available commands.

---

## Adding Custom Tools

Tools are registered in `internal/tools/registry.go`:

```go
type Tool interface {
    Name() string
    Description() string
    Schema() map[string]interface{}
    Execute(ctx context.Context, input json.RawMessage) (string, error)
}
```

To add a new tool:

1. Create `internal/tools/mytool.go`
2. Implement the `Tool` interface
3. Register in `NewRegistry()`

---

## See Also

- [Configuration](./configuration.md) - Tool-related config
- [Architecture](./architecture.md) - How tools integrate
- [Memory Search](./memory-search.md) - Memory tool details
