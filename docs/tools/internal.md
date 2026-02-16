---
title: "Internal Tools"
description: "Core file and system tools: read, write, edit, exec"
section: "Tools"
weight: 10
---

# Internal Tools

Core file and system tools available to the agent.

## read

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
- Blocked files: `users.json`, `goclaw.json`, `openclaw.json`

---

## write

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

**Output:** Success message with bytes written

**Behavior:**
- Atomic writes (temp file + rename)
- Creates parent directories if needed
- Overwrites existing files

---

## edit

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

**Error:** `old_string is not unique` — Include more context to make the match unique.

---

## exec

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
| `timeout` | int | No | Timeout in seconds (default: 30, max: 1800) |
| `working_dir` | string | No | Working directory |

**Output:** Command output (stdout + stderr) with exit code

**Configuration:**

```json
{
  "tools": {
    "exec": {
      "timeout": 1800,
      "bubblewrap": {
        "enabled": false,
        "extraRoBind": [],
        "extraBind": [],
        "extraEnv": {},
        "allowNetwork": true
      }
    }
  }
}
```

| Option | Default | Description |
|--------|---------|-------------|
| `timeout` | 1800 | Default timeout (30 minutes) |
| `bubblewrap.enabled` | false | Enable bubblewrap sandboxing (Linux) |
| `bubblewrap.extraRoBind` | [] | Additional read-only paths |
| `bubblewrap.extraBind` | [] | Additional writable paths |
| `bubblewrap.extraEnv` | {} | Additional environment variables |
| `bubblewrap.allowNetwork` | true | Allow network access |

See [Sandbox](../sandbox.md) for sandboxing details.

---

## Security

All internal tools share these protections:

| Protection | Description |
|------------|-------------|
| Path traversal | Blocks `../` and symlinks escaping workspace |
| Unicode normalization | Normalizes space characters to prevent confusion |
| Blocked files | Protects `users.json`, `goclaw.json` from agent access |
| Workspace containment | All paths resolved within workspace |

Users with `sandbox: false` in their config can bypass path restrictions.

---

## See Also

- [Tools](../tools.md) — Tool overview
- [Sandbox](../sandbox.md) — Security and sandboxing
- [Configuration](../configuration.md) — Full config reference
