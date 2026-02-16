# Configuration Reference

GoClaw is configured via `goclaw.json` in the working directory.

## Full Configuration Example

```json
{
  "llm": {
    "provider": "anthropic",
    "model": "claude-sonnet-4-20250514",
    "apiKey": "sk-ant-...",
    "maxTokens": 200000,
    "promptCaching": true
  },

  "telegram": {
    "enabled": true,
    "botToken": "123456:ABC..."
  },

  "http": {
    "enabled": true,
    "port": 8080
  },

  "session": {
    "store": "sqlite",
    "storePath": "~/.goclaw/sessions.db",
    "inherit": false,
    "inheritPath": "",
    "inheritFrom": "",
    
    "summarization": {
      "ollama": {
        "url": "http://localhost:11434",
        "model": "qwen2.5:7b",
        "timeoutSeconds": 600,
        "contextTokens": 131072
      },
      "fallbackModel": "claude-3-haiku-20240307",
      "failureThreshold": 3,
      "resetMinutes": 30,
      "retryIntervalSeconds": 60,
      
      "checkpoint": {
        "enabled": true,
        "thresholds": [25, 50, 75],
        "turnThreshold": 10,
        "minTokensForGen": 5000
      },
      
      "compaction": {
        "reserveTokens": 4000,
        "maxMessages": 500,
        "preferCheckpoint": true,
        "keepPercent": 50,
        "minMessages": 20
      }
    },
    
    "memoryFlush": {
      "enabled": true,
      "showInSystemPrompt": true,
      "thresholds": [
        {"percent": 50, "prompt": "Consider noting key decisions.", "injectAs": "system", "oncePerCycle": true},
        {"percent": 75, "prompt": "Write important context now.", "injectAs": "user", "oncePerCycle": true}
      ]
    }
  },

  "memorySearch": {
    "enabled": true,
    "ollama": {
      "url": "http://localhost:11434",
      "model": "nomic-embed-text"
    },
    "query": {
      "maxResults": 6,
      "minScore": 0.35,
      "vectorWeight": 0.7,
      "keywordWeight": 0.3
    },
    "paths": []
  },

  "skills": {
    "enabled": true,
    "watch": true,
    "watchDebounceMs": 500,
    "entries": {}
  },

  "tools": {
    "exec": {
      "timeout": 1800,
      "bubblewrap": {
        "enabled": false
      }
    },
    "browser": {
      "enabled": false
    },
    "web": {
      "braveApiKey": "",
      "useJina": false
    }
  },

  "media": {
    "dir": "~/.goclaw/media",
    "ttl": 600,
    "maxSize": 5242880
  },

  "promptCache": {
    "pollInterval": 60
  },

  "gateway": {
    "port": 8080,
    "workingDir": "/path/to/workspace"
  }
}
```

---

## Configuration Sections

### Core

| Section | Description | Documentation |
|---------|-------------|---------------|
| `llm` | Primary LLM provider settings | [LLM Providers](llm-providers.md) |
| `session` | Session storage, compaction, checkpoints | [Session Management](session-management.md) |
| `memorySearch` | Semantic memory search | [Memory Search](memory-search.md) |

### Channels

| Section | Description | Documentation |
|---------|-------------|---------------|
| `telegram` | Telegram bot configuration | [Telegram](telegram.md) |
| `http` | Web UI and HTTP API | [Web UI](web-ui.md) |
| `tui` | Terminal UI settings | [TUI](tui.md) |

### Tools

| Section | Description | Documentation |
|---------|-------------|---------------|
| `tools.exec` | Shell command execution | [Tools](tools.md) |
| `tools.browser` | Browser automation | [Browser Tool](tools/browser.md) |
| `tools.web` | Web search and fetch | [Tools](tools.md) |
| `skills` | Skills system | [Skills](skills.md) |

### System

| Section | Description | Documentation |
|---------|-------------|---------------|
| `media` | Temporary media storage | Below |
| `promptCache` | Workspace file caching | Below |
| `gateway` | Server settings | Below |

---

## Quick Reference

### LLM Settings

```json
{
  "llm": {
    "provider": "anthropic",
    "model": "claude-sonnet-4-20250514",
    "apiKey": "sk-ant-...",
    "maxTokens": 200000,
    "promptCaching": true
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `provider` | string | `"anthropic"` | LLM provider (anthropic, openai, ollama, xai) |
| `model` | string | - | Model name |
| `apiKey` | string | - | API key (or use `ANTHROPIC_API_KEY` env) |
| `maxTokens` | int | `200000` | Context window size |
| `promptCaching` | bool | `true` | Enable Anthropic prompt caching |

See [LLM Providers](llm-providers.md) for multi-provider setup and purpose chains.

### Telegram Settings

```json
{
  "telegram": {
    "enabled": true,
    "botToken": "123456:ABC..."
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable Telegram bot |
| `botToken` | string | - | Bot token from @BotFather |

Token can also be set via `TELEGRAM_BOT_TOKEN` environment variable.

### Session Storage

```json
{
  "session": {
    "store": "sqlite",
    "storePath": "~/.goclaw/sessions.db",
    "inherit": true,
    "inheritPath": "~/.openclaw/agents/main/sessions",
    "inheritFrom": "main"
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `store` | string | `"sqlite"` | Storage backend (always sqlite) |
| `storePath` | string | `~/.goclaw/sessions.db` | SQLite database path |
| `inherit` | bool | `false` | Enable OpenClaw session inheritance |
| `inheritPath` | string | - | Path to OpenClaw sessions directory |
| `inheritFrom` | string | - | Session key to inherit from |

See [Session Management](session-management.md) for compaction, checkpoints, and memory flush.

### Media Storage

```json
{
  "media": {
    "dir": "~/.goclaw/media",
    "ttl": 600,
    "maxSize": 5242880
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `dir` | string | `~/.goclaw/media` | Media directory |
| `ttl` | int | `600` | File TTL in seconds |
| `maxSize` | int | `5242880` | Max file size (5MB) |

### Prompt Cache

```json
{
  "promptCache": {
    "pollInterval": 60
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `pollInterval` | int | `60` | Hash check interval in seconds (0 = disabled) |

The prompt cache watches workspace identity files (SOUL.md, AGENTS.md, etc.) for changes.

### Gateway

```json
{
  "gateway": {
    "port": 8080,
    "workingDir": "/path/to/workspace",
    "logFile": "goclaw.log",
    "pidFile": "goclaw.pid"
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `port` | int | `8080` | HTTP server port |
| `workingDir` | string | cwd | Workspace directory |
| `logFile` | string | - | Log file path |
| `pidFile` | string | - | PID file path |

---

## No Environment Variables for Runtime Config

Secrets and settings are read only from `goclaw.json` (and `users.json`). Environment variables are not used at runtime, to avoid unexpected overrides.

**During setup:** If you run `goclaw setup` and have `ANTHROPIC_API_KEY`, `TELEGRAM_BOT_TOKEN`, or `BRAVE_API_KEY` set in your environment (e.g. from OpenClaw), the wizard will detect them and ask whether to use each one. If you accept, they are written into `goclaw.json`. After that, runtime uses only the config file.

---

## Security: config file and credentials

**Config is sandboxed from the agent.** The `read`, `write`, and `edit` tools cannot access `goclaw.json`, `users.json`, or `openclaw.json` — these filenames are on a denied list and are blocked even if they appear inside the workspace. So the agent cannot read or modify your API keys or user credentials through file tools. See [Sandbox](sandbox.md#denied-files).

**Config is stored outside the workspace directory** in the normal layout. The default config path is `~/.goclaw/goclaw.json`; the default workspace (where the agent reads/writes) is `~/.goclaw/workspace` or a path you set (e.g. a project directory). So the config file is not inside the agent’s workspace. If you use a local `goclaw.json` in the current directory, it can be alongside the workspace, but it remains inaccessible to the agent because of the denied list.

**Environment variables vs file: a pragmatic view.** Storing secrets in a file has downsides (backup leaks, world-readable if misconfigured), but so do env vars: they live in process memory and in shell history, and can be inherited by child processes. Many stacks (OpenClaw included) store API keys in files under the app dir (e.g. OpenClaw’s `auth-profiles.json` under `~/.openclaw/`). GoClaw’s choice: **no env overrides at runtime**, so behaviour is predictable and the only place to look for secrets is the config file. The setup wizard can copy env vars into `goclaw.json` once, so OpenClaw users who relied on env can migrate without changing how they set those vars before running the wizard. For stricter setups, keep `goclaw.json` in `~/.goclaw/` with mode `0600` and avoid committing it.

---

## Users Configuration

User access is configured in `users.json`:

```json
{
  "users": [
    {
      "name": "TheRoDent",
      "role": "owner",
      "identities": [
        {"provider": "telegram", "id": "123456789"}
      ]
    },
    {
      "name": "Ratpup",
      "role": "user",
      "identities": [
        {"provider": "telegram", "id": "987654321"}
      ],
      "permissions": ["read", "write", "exec"]
    }
  ]
}
```

| Field | Description |
|-------|-------------|
| `name` | Display name |
| `role` | `"owner"` (full access) or `"user"` (limited) |
| `identities` | Array of identity providers and IDs |
| `permissions` | Tool whitelist for non-owner users |
| `sandbox` | `false` to bypass file sandboxing |
| `thinking` | `true` to show tool calls by default |
| `thinkingLevel` | Thinking intensity (off/minimal/low/medium/high) |

See [Roles](roles.md) for detailed access control documentation.

---

## See Also

- [Session Management](session-management.md) — Compaction, checkpoints, memory flush
- [LLM Providers](llm-providers.md) — Multi-provider setup
- [Tools](tools.md) — Tool configuration
- [Skills](skills.md) — Skills system
- [Architecture](architecture.md) — System overview
