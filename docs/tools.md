---
title: "Tools"
description: "Agent tools for file operations, web access, and external services"
section: "Tools"
weight: 1
landing: true
---

# Tools

GoClaw provides tools that the agent can use to interact with the system, files, web, and external services.

## Tool Categories

### Core Tools

Basic file and system operations:

| Tool | Description | Documentation |
|------|-------------|---------------|
| `read` | Read file contents | [Internal Tools](tools/internal.md) |
| `write` | Write file contents | [Internal Tools](tools/internal.md) |
| `edit` | Edit file (string replace) | [Internal Tools](tools/internal.md) |
| `exec` | Execute shell commands | [Internal Tools](tools/internal.md) |

### Communication

| Tool | Description | Documentation |
|------|-------------|---------------|
| `message` | Send, edit, react to channel messages | [Message Tool](tools/message.md) |

### Memory & Search

| Tool | Description | Documentation |
|------|-------------|---------------|
| `recall` | Retrieve relevant memories from graph | [Memory Graph](memory-graph.md) |
| `query` | Search/filter memory graph | [Memory Graph](memory-graph.md) |
| `store` | Add memory to graph | [Memory Graph](memory-graph.md) |
| `update` | Modify existing memory | [Memory Graph](memory-graph.md) |
| `forget` | Remove memory from graph | [Memory Graph](memory-graph.md) |
| `memory_search` | Semantic search over memory files | [Memory Search](memory-search.md) |
| `transcript_search` | Search conversation history | [Transcript Search](transcript-search.md) |
| `web_search` | Search the web | [Web Tools](tools/web.md) |
| `web_fetch` | Fetch web page content | [Web Tools](tools/web.md) |

### Integration

| Tool | Description | Documentation |
|------|-------------|---------------|
| `browser` | Browser automation | [Browser Tool](tools/browser.md) |
| `hass` | Home Assistant control | [Home Assistant](tools/hass.md) |
| `cron` | Schedule tasks | [Cron Tool](tools/cron.md) |

### Utility

| Tool | Description | Documentation |
|------|-------------|---------------|
| `jq` | JSON query and transformation | [JQ Tool](tools/jq.md) |
| `xai_imagine` | xAI image generation | [xAI Imagine](tools/xai-imagine.md) |
| `user_auth` | Request role elevation | [User Auth](tools/user-auth.md) |
| `skills` | List, search, install, and manage skills | [Skills](skills.md) |
| `media_display` | Display images/media to user | — |
| `goclaw_update` | Check for and install updates | [GoClaw Update](tools/goclaw-update.md) |

## Configuration

Tool configuration in `goclaw.json`:

```json
{
  "tools": {
    "exec": {
      "timeout": 1800,
      "bubblewrap": {
        "enabled": false
      }
    },
    "browser": {
      "enabled": true,
      "headless": true
    },
    "web": {
      "braveApiKey": "YOUR_API_KEY",
      "useJina": false
    }
  }
}
```

## Tool Permissions

Tools can be restricted per-user in `users.json`:

```json
{
  "users": [
    {
      "name": "Ratpup",
      "role": "user",
      "identities": [{"provider": "telegram", "id": "987654321"}],
      "permissions": ["read", "memory_search"]
    }
  ]
}
```

When `permissions` is set, only those tools are available. Owners have access to all tools.

## Tool Errors

Tools return structured errors:

| Error | Cause |
|-------|-------|
| `file not found` | Path doesn't exist |
| `permission denied` | File not readable/writable |
| `path traversal detected` | Attempted `../` escape |
| `old_string is not unique` | Multiple matches in edit |
| `command timed out` | Exec exceeded timeout |

---

## See Also

- [Internal Tools](tools/internal.md) — read, write, edit, exec
- [Memory Graph](memory-graph.md) — Semantic knowledge graph
- [Browser Tool](tools/browser.md) — Browser automation
- [Home Assistant](tools/hass.md) — Smart home control
- [Skills](skills.md) — Skills system and installation
- [Configuration](configuration.md) — Full config reference
- [Sandbox](sandbox.md) — Tool security
