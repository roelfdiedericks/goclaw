---
title: "Configuration"
description: "Configure GoClaw with goclaw.json: LLM providers, channels, tools, and features"
section: "Getting Started"
weight: 2
---

# Configuration Reference

GoClaw is configured via `goclaw.json` in the working directory.

## Full Configuration Example

```json
{
  "llm": {
    "providers": {
      "anthropic": {
        "driver": "anthropic",
        "apiKey": "sk-ant-...",
        "promptCaching": true
      }
    },
    "agent": {
      "models": ["anthropic/claude-sonnet-4-20250514"]
    },
    "subagent": {
      "models": []
    }
  },

  "channels": {
    "telegram": {
      "enabled": true,
      "botToken": "123456:ABC..."
    },
    "whatsapp": {
      "enabled": false
    },
    "http": {
      "enabled": true,
      "listen": ":1337"
    }
  },

  "stt": {
    "provider": "whispercpp",
    "whispercpp": {
      "model": "ggml-tiny.en.bin",
      "modelsDir": "~/.goclaw/stt/whisper"
    }
  },

  "voicellm": {
    "enabled": false,
    "default": "xai",
    "providers": {
      "xai": {
        "driver": "xai",
        "apiKey": "xai-...",
        "voice": "Eve"
      }
    }
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

  "memory": {
    "enabled": true,
    "dbPath": "",
    "paths": [],
    "query": {
      "maxResults": 6,
      "minScore": 0.35,
      "vectorWeight": 0.7,
      "keywordWeight": 0.3
    }
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
      "useBrowser": "auto",
      "profile": "default",
      "headless": true
    },
    "subagent": {
      "enabled": true
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
    "workingDir": "~/.goclaw/workspace",
    "logFile": "~/.goclaw/goclaw.log",
    "pidFile": "~/.goclaw/goclaw.pid",
    "delegatedRuns": {
      "enabled": true,
      "maxSpawnDepth": 4,
      "maxActiveChildrenPerParent": 4,
      "maxConcurrentRuns": 16,
      "defaultTimeoutSeconds": 300,
      "maxTimeoutSeconds": 1800
    }
  }
}
```

---

## Configuration Sections

### Core

| Section | Description | Documentation |
|---------|-------------|---------------|
| `agent` | Agent name, emoji, personality | Below |
| `llm` | LLM provider settings and purpose chains | [LLM Providers](llm-providers.md) |
| `session` | Session storage, compaction, checkpoints | [Session Management](session-management.md) |
| `memory` | Semantic memory search | [Memory Search](memory-search.md) |
| `memoryGraph` | Knowledge graph extraction and bulletins | [Memory Graph](memory-graph.md) |
| `transcript` | Conversation transcript indexing | [Transcript Search](transcript-search.md) |

### Channels

| Section | Description | Documentation |
|---------|-------------|---------------|
| `channels.telegram` | Telegram bot configuration | [Telegram](telegram.md) |
| `channels.whatsapp` | WhatsApp via linked device | [WhatsApp](whatsapp.md) |
| `channels.http` | Web UI, chat, and voice | [Web UI](web-ui.md) |
| `channels.tui` | Terminal UI settings | [TUI](tui.md) |

### Voice & Audio

| Section | Description | Documentation |
|---------|-------------|---------------|
| `stt` | Speech-to-text transcription | Below |
| `voicellm` | Real-time voice conversations | [Voice](voice.md) |

### Tools

| Section | Description | Documentation |
|---------|-------------|---------------|
| `tools.exec` | Shell command execution | [Tools](tools.md) |
| `tools.browser` | Browser automation | [Browser Tool](tools/browser.md) |
| `tools.web` | Web search and fetch | [Tools](tools.md) |
| `tools.subagent` | Owner-only subagent and fanout tools | [Delegated Runs](delegated-runs.md) |
| `tools.xaiImagine` | xAI image generation | [Tools](tools.md) |
| `tools.xaiVideo` | xAI video generation | [Tools](tools.md) |
| `skills` | Skills system | [Skills](skills.md) |

**Dependency note:** subagent tools only appear when both `tools.subagent.enabled=true` and `gateway.delegatedRuns.enabled=true`.

### System

| Section | Description | Documentation |
|---------|-------------|---------------|
| `media` | Temporary media storage | Below |
| `promptCache` | Workspace file caching | Below |
| `gateway` | Server settings | Below |
| `auth` | Role elevation via external script | [User Auth Tool](tools/user-auth.md) |
| `roles` | Role-based access control definitions | [Roles](roles.md) |
| `cron` | Scheduled jobs and heartbeat | [Cron](tools/cron.md) |
| `safety` | Panic stop phrases | [Security](security.md) |
| `sandbox` | Execution isolation (bubblewrap/seatbelt) | [Sandbox](sandbox.md) |
| `supervision` | Ghostwriting and guidance | [Supervision](supervision.md) |
| `homeassistant` | Home Assistant integration | [Home Assistant](tools/hass.md) |

---

## Quick Reference

### LLM Settings

```json
{
  "llm": {
    "providers": {
      "anthropic": {
        "driver": "anthropic",
        "apiKey": "sk-ant-...",
        "promptCaching": true
      },
      "hugot-local": {
        "driver": "hugot",
        "embeddingOnly": true
      }
    },
    "agent": {
      "models": ["anthropic/claude-sonnet-4-20250514"]
    },
    "subagent": {
      "models": []
    },
    "embeddings": {
      "models": ["hugot-local/KnightsAnalytics/all-MiniLM-L6-v2"]
    }
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `providers` | object | Named provider instances (alias → config) |
| `agent` | object | Model chain for main conversation |
| `subagent` | object | Model chain used internally for subagent and fanout runs (falls back to `agent`) |
| `summarization` | object | Model chain for compaction/checkpoints |
| `embeddings` | object | Model chain for semantic search |

GoClaw keeps a built-in local embeddings provider named `hugot-local` in `llm.providers`. If `llm.embeddings.models` is empty, GoClaw seeds the default local embeddings model automatically. If you already configured an embeddings chain, GoClaw leaves it unchanged.

See [LLM Providers](llm-providers.md) for full configuration details.

### Telegram Settings

```json
{
  "channels": {
    "telegram": {
      "enabled": true,
      "botToken": "123456:ABC..."
    }
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable Telegram bot |
| `botToken` | string | - | Bot token from @BotFather |

The setup wizard (`goclaw setup`) can detect `TELEGRAM_BOT_TOKEN` from your environment and offer to use it.

### WhatsApp Settings

```json
{
  "channels": {
    "whatsapp": {
      "enabled": true
    }
  }
}
```

WhatsApp uses the linked device protocol (no business API required). On first run, scan the QR code with your phone to link. Session persists in `~/.goclaw/whatsapp/`.

### HTTP Settings

```json
{
  "channels": {
    "http": {
      "enabled": true,
      "listen": ":1337"
    }
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` when unset | Enable HTTP/Web UI |
| `listen` | string | `:1337` | Listen address (`:port` or `host:port`) |

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
| `inherit` | bool | `true` | Enable OpenClaw session inheritance |
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
    "workingDir": "~/.goclaw/workspace",
    "logFile": "~/.goclaw/goclaw.log",
    "pidFile": "~/.goclaw/goclaw.pid",
    "delegatedRuns": {
      "enabled": true,
      "maxSpawnDepth": 4,
      "maxActiveChildrenPerParent": 4,
      "maxConcurrentRuns": 16
    }
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `workingDir` | string | `~/.goclaw/workspace` | Workspace directory |
| `logFile` | string | - | Log file path |
| `pidFile` | string | - | PID file path |
| `delegatedRuns.enabled` | bool | `true` | Enable subagents, fanout, and other delegated background runs |
| `delegatedRuns.maxSpawnDepth` | int | `4` | Max parent-child subagent depth (0 = unlimited) |
| `delegatedRuns.maxActiveChildrenPerParent` | int | `4` | Max active child runs per parent (0 = unlimited) |
| `delegatedRuns.maxConcurrentRuns` | int | `16` | Max delegated/background runs at once (0 = unlimited) |
| `delegatedRuns.defaultTimeoutSeconds` | int | `300` | Default time limit for delegated runs when a tool/job does not set one |
| `delegatedRuns.maxTimeoutSeconds` | int | `1800` | Maximum delegated run timeout for safety (0 = unlimited) |

### Speech-to-Text (STT)

```json
{
  "stt": {
    "provider": "whispercpp",
    "whispercpp": {
      "model": "ggml-tiny.en.bin",
      "modelsDir": "~/.goclaw/stt/whisper"
    }
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `provider` | string | `"whispercpp"` | Provider: `whispercpp`, `openai`, `groq`, `google` |
| `whispercpp.model` | string | `ggml-tiny.en.bin` | Model filename |
| `whispercpp.modelsDir` | string | `~/.goclaw/stt/whisper` | Models directory |
| `openai.apiKey` | string | - | OpenAI API key (for Whisper API) |
| `groq.apiKey` | string | - | Groq API key |
| `google.apiKey` | string | - | Google Cloud API key |

**Local whisper.cpp** is the default and runs offline. Models are downloaded via the setup wizard or `goclaw setup edit`. The `.deb` package includes `ggml-tiny.en.bin` at `/usr/share/goclaw/stt/`.

### Voice LLM (Real-time Voice)

```json
{
  "voicellm": {
    "enabled": true,
    "default": "xai",
    "serverVAD": true,
    "idleTimeout": 300,
    "providers": {
      "xai": {
        "driver": "xai",
        "apiKey": "xai-...",
        "voice": "Eve",
        "sampleRate": 48000
      }
    }
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable real-time voice |
| `default` | string | - | Default provider name |
| `serverVAD` | bool | `true` | Server-side voice activity detection |
| `idleTimeout` | int | `300` | Idle timeout in seconds |
| `providers` | object | - | Named provider configs |

**Provider config:**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `driver` | string | - | `xai` |
| `apiKey` | string | - | Provider API key |
| `voice` | string | `Eve` | Voice name (Eve, Ara, Rex, Sal, Leo) |
| `sampleRate` | int | `48000` | Audio sample rate |

See [Voice](voice.md) for full documentation including prompt customization and browser requirements.

---

## No Environment Variables for Runtime Config

Secrets and settings are read only from `goclaw.json` (and `users.json`). Environment variables are not used at runtime, to avoid unexpected overrides.

**During setup:** If you run `goclaw setup` and have `ANTHROPIC_API_KEY`, `TELEGRAM_BOT_TOKEN`, or `BRAVE_API_KEY` set in your environment (e.g. from OpenClaw), the wizard will detect them and ask whether to use each one. If you accept, they are written into `goclaw.json`. After that, runtime uses only the config file.

---

### Auth (Role Elevation)

```json
{
  "auth": {
    "enabled": true,
    "script": "/home/user/.goclaw/scripts/auth.sh",
    "credentialHints": [
      {"key": "customer_id", "label": "Customer ID", "required": true},
      {"key": "phone", "label": "phone number"},
      {"key": "email", "label": "email address"}
    ],
    "allowedRoles": ["customer", "user"],
    "rateLimit": 3,
    "timeout": 10
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | boolean | `false` | Enable the `user_auth` tool |
| `script` | string | - | Path to authentication script |
| `credentialHints` | object[] | `[]` | Credentials the script accepts |
| `credentialHints[].key` | string | - | JSON field name |
| `credentialHints[].label` | string | key | Friendly name for agent to use |
| `credentialHints[].required` | boolean | `false` | Mark as required |
| `allowedRoles` | string[] | `[]` | Roles the script can return |
| `rateLimit` | int | `3` | Max auth attempts per minute |
| `timeout` | int | `10` | Script timeout in seconds |

The `user_auth` tool allows guest users to authenticate mid-session and be elevated to a higher role. The `credentialHints` tell the agent what information to ask for (with friendly labels) and which credentials are required.

See [User Auth Tool](tools/user-auth.md) for full documentation.

---

## Security: config file and credentials

### Sandbox and location

**Config is sandboxed from the agent.** The `read`, `write`, and `edit` tools cannot access `goclaw.json`, `users.json`, or `openclaw.json`. These filenames are on a [denied list](sandbox.md#denied-files) in the file-tools sandbox and are blocked even if they appear inside the workspace. The agent cannot read or modify API keys or user credentials through file tools.

**Config is stored outside the workspace directory** in the normal layout. The default config path is `~/.goclaw/goclaw.json`; the default workspace (where the agent reads/writes) is `~/.goclaw/workspace` or a path you set (e.g. a project directory). So the config file is not inside the agent’s workspace. If you use a local `goclaw.json` in the current directory, it can be alongside the workspace but remains inaccessible to the agent because of the denied list. For stricter setups, keep `goclaw.json` in `~/.goclaw/` with mode `0600` and avoid committing it.

### Environment variable references

GoClaw does **not** automatically scan environment variables for API keys. However, you can explicitly reference env vars using `${VAR_NAME}` syntax:

```json
{
  "llm": {
    "providers": {
      "anthropic": {
        "apiKey": "${ANTHROPIC_API_KEY}"
      }
    }
  }
}
```

- **At runtime** — `${VAR}` references are expanded when starting the gateway or CLI commands
- **In setup wizard** — The literal `${VAR_NAME}` text is preserved for editing
- **Missing vars** — GoClaw fails with a clear error if a referenced variable is not set

This is useful for Kubernetes, Docker, CI/CD pipelines, and other deployment systems that inject secrets via environment.

For the full rationale (why explicit references rather than auto-scanning, security considerations, best practice), see [Environment variables and secrets](security-envvars.md).

---

## Users Configuration

User access is configured in `users.json`:

```json
{
  "users": [
    {
      "name": "Alice",
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
