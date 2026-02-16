# Configuration Reference

GoClaw is configured via `goclaw.json` in the working directory.

## Quick Example

```json
{
  "telegram": {
    "enabled": true,
    "botToken": "YOUR_BOT_TOKEN"
  },
  "llm": {
    "provider": "anthropic",
    "model": "claude-sonnet-4-20250514",
    "apiKey": "YOUR_API_KEY"
  },
  "session": {
    "store": "sqlite",
    "compaction": {
      "reserveTokens": 30000,
      "ollama": {
        "url": "http://localhost:11434",
        "model": "qwen2.5:7b"
      }
    }
  }
}
```

---

## Telegram

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

### Telegram Commands

| Command | Description |
|---------|-------------|
| `/clear` | Clear session history |
| `/compact` | Force compaction |
| `/status` | Show session info |

---

## LLM

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
| `provider` | string | `"anthropic"` | LLM provider |
| `model` | string | - | Model name |
| `apiKey` | string | - | API key (or use `ANTHROPIC_API_KEY` env) |
| `maxTokens` | int | `200000` | Context window size |
| `promptCaching` | bool | `true` | Enable Anthropic prompt caching |

---

## Session Storage

```json
{
  "session": {
    "store": "sqlite",
    "storePath": "~/.goclaw/sessions.db"
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `storePath` | string | `~/.goclaw/sessions.db` | SQLite database path |
| `path` | string | - | OpenClaw sessions directory (for session inheritance) |

Note: GoClaw uses SQLite exclusively for session storage. The `path` option points to an existing OpenClaw sessions directory for inheriting conversation history.

---

## Checkpoints

Checkpoints are rolling conversation snapshots. See [Session Management](./session-management.md).

```json
{
  "session": {
    "checkpoint": {
      "enabled": true,
      "tokenThresholdPercents": [25, 50, 75],
      "turnThreshold": 10,
      "minTokensForGen": 5000
    }
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable checkpoint generation |
| `tokenThresholdPercents` | int[] | `[25, 50, 75]` | Generate at these % of context |
| `turnThreshold` | int | `10` | Generate every N user messages |
| `minTokensForGen` | int | `5000` | Minimum tokens before checkpointing |

---

## Compaction

Compaction truncates old messages when context is nearly full. See [Session Management](./session-management.md).

```json
{
  "session": {
    "compaction": {
      "reserveTokens": 30000,
      "preferCheckpoint": true,
      "retryIntervalSeconds": 60,
      "ollamaFailureThreshold": 3,
      "ollamaResetMinutes": 30,
      "ollama": {
        "url": "http://localhost:11434",
        "model": "qwen2.5:7b",
        "timeoutSeconds": 600,
        "contextTokens": 131072
      }
    }
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `reserveTokens` | int | `30000` | Trigger compaction when `tokens >= max - reserve` |
| `preferCheckpoint` | bool | `true` | Use checkpoint summary if available (skip LLM) |
| `retryIntervalSeconds` | int | `60` | Background retry interval for failed summaries |
| `ollamaFailureThreshold` | int | `3` | Fall back to Anthropic after N Ollama failures |
| `ollamaResetMinutes` | int | `30` | Try Ollama again after this many minutes |

### Ollama Configuration (for compaction/checkpoints)

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `url` | string | - | Ollama API URL |
| `model` | string | - | Model name (e.g., `qwen2.5:7b`) |
| `timeoutSeconds` | int | `300` | Request timeout |
| `contextTokens` | int | auto | Override context window (0 = auto-detect) |

---

## Prompt Cache

Caches workspace files to avoid repeated disk I/O.

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

---

## Memory Search

Semantic search over memory files using embeddings.

```json
{
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
    "paths": ["custom/path"]
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable memory search tools |
| `ollama.url` | string | - | Ollama URL for embeddings |
| `ollama.model` | string | - | Embedding model |
| `query.maxResults` | int | `6` | Max search results |
| `query.minScore` | float | `0.35` | Minimum similarity score |
| `query.vectorWeight` | float | `0.7` | Weight for semantic search |
| `query.keywordWeight` | float | `0.3` | Weight for keyword search |
| `paths` | string[] | `[]` | Additional paths to index |

---

## Media Storage

Temporary media file storage (screenshots, etc.).

```json
{
  "media": {
    "dir": "~/.openclaw/media",
    "ttl": 600,
    "maxSize": 5242880
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `dir` | string | `~/.openclaw/media` | Media directory |
| `ttl` | int | `600` | File TTL in seconds |
| `maxSize` | int | `5242880` | Max file size (5MB) |

---

## TUI (Terminal UI)

```json
{
  "tui": {
    "showLogs": true
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `showLogs` | bool | `true` | Show logs panel by default |

### TUI Keybindings

| Key | Action |
|-----|--------|
| `Tab` | Switch focus between panels |
| `Ctrl+L` | Cycle layout (Normal → Logs Hidden → Logs Full) |
| `Ctrl+C` | Quit |

---

## Gateway

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

## Environment Variables

Some settings can be provided via environment variables:

| Variable | Config Equivalent |
|----------|-------------------|
| `ANTHROPIC_API_KEY` | `llm.apiKey` |
| `TELEGRAM_BOT_TOKEN` | `telegram.botToken` |

---

## See Also

- [Session Management](./session-management.md) - Compaction and checkpoints explained
- [Architecture](./architecture.md) - System overview
