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

### Memory Graph

Structured knowledge graph for persistent agent memory:

| Tool | Description | Documentation |
|------|-------------|---------------|
| `memory_graph_recall` | Retrieve relevant memories | [Memory Graph](memory-graph.md) |
| `memory_graph_query` | Search/filter with metadata | [Memory Graph](memory-graph.md) |
| `memory_graph_store` | Add memory to graph | [Memory Graph](memory-graph.md) |
| `memory_graph_update` | Modify existing memory | [Memory Graph](memory-graph.md) |
| `memory_graph_forget` | Remove memory from graph | [Memory Graph](memory-graph.md) |
| `memory_graph_skip` | Log skip decision with reason | [Memory Graph](memory-graph.md) |

### Memory Files

Semantic search over markdown memory files:

| Tool | Description | Documentation |
|------|-------------|---------------|
| `memory_search` | Search memory files | [Memory Search](memory-search.md) |
| `memory_get` | Read memory file content | [Memory Search](memory-search.md) |

### Search

| Tool | Description | Documentation |
|------|-------------|---------------|
| `transcript` | Search/query conversation history | [Transcript Search](transcript-search.md) |
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
| `xai_video` | xAI video generation | [xAI Video](tools/xai-video.md) |
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
      "useBrowser": "auto",
      "profile": "default",
      "headless": true
    }
  }
}
```

## Tool Permissions

Tool access is controlled at two levels:

### Role-Based (goclaw.json)

Roles define default tool access:

```json
{
  "roles": {
    "user": {
      "tools": ["read", "memory_search", "transcript", "web_search"]
    },
    "guest": {
      "tools": ["read"]
    }
  }
}
```

Use `"tools": "*"` to allow all tools (default for `owner` role).

### Per-User Override (users.json)

Override role defaults for specific users:

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

When `permissions` is set, it overrides the role's tool list for that user.

See [Roles & Access Control](roles.md) for full RBAC documentation.

---

## See Also

- [Internal Tools](tools/internal.md) — read, write, edit, exec
- [Memory Graph](memory-graph.md) — Semantic knowledge graph
- [Browser Tool](tools/browser.md) — Browser automation
- [Home Assistant](tools/hass.md) — Smart home control
- [Skills](skills.md) — Skills system and installation
- [Configuration](configuration.md) — Full config reference
- [Sandbox](sandbox.md) — Tool security
