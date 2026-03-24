---
title: "Tools"
description: "Agent tools for file operations, web access, and external services"
section: "Tools"
weight: 1
landing: true
---

# Tools

Tools are how GoClaw does work for you: reading files, searching the web, querying memory, automating the browser, and more.

This page helps you understand:
- what tools are available
- where to configure them
- how access is controlled
- how parallel tool execution works

## Tool Categories

### Core Tools

Daily file and shell operations:

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

Structured long-term memory operations:

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

Most tool-specific settings live under `tools` in `goclaw.json`.
Common examples include shell timeout, browser settings, and web search keys.

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
      "braveApiKey": "YOUR_BRAVE_API_KEY",
      "search": {
        "enabled": true,
        "provider": "auto",
        "fallbackProviders": [],
        "maxFallbackAttempts": 3,
        "retry": {
          "enabled": true,
          "maxAttemptsPerProvider": 2,
          "baseBackoffMs": 500,
          "maxBackoffMs": 5000
        },
        "providers": {
          "brave": { "apiKey": "YOUR_BRAVE_API_KEY" },
          "grok": { "apiKey": "YOUR_XAI_API_KEY" },
          "perplexity": { "apiKey": "YOUR_PERPLEXITY_API_KEY" },
          "gemini": { "apiKey": "YOUR_GEMINI_API_KEY" }
        }
      },
      "useBrowser": "auto",
      "profile": "default",
      "headless": true
    }
  }
}
```

## Binary Content Protection

GoClaw protects tool execution from raw binary payloads entering model context.
This helps prevent context overflows and malformed output from files like PDFs, archives, media, and other non-text content.

### What to expect

- If a tool detects binary content, it returns a safe summary instead of raw bytes.
- The summary includes path, MIME type, and size when available.
- Large tool outputs may be truncated for context safety.
- Existing session history is also sanitized before reuse.

### Affected tool flows

- `read` rejects raw binary file content and returns a safe summary.
- `web_fetch` sanitizes non-HTML/binary responses before returning content.
- `exec`/`jq` outputs are guarded so binary stdout/stderr does not poison context.

### If your file is rejected

- Extract text first (for example from a PDF) and send the extracted text.
- Keep raw binaries in uploads/media, but pass text excerpts to the model.
- For very large text outputs, narrow scope before sending (smaller files, fewer lines, or filtered output).

## Parallel Tool Execution

GoClaw can run some tool batches concurrently to reduce latency.
This is enabled by default, but only for allowlisted safe tools.

### How it works

- Parallel execution is controlled by gateway settings.
- A batch runs in parallel only if:
  - `gateway.toolExecution.parallelEnabled` is `true`
  - there are at least 2 tool calls in the same turn
  - every tool in that batch is in the parallel allowlist
- If any tool in that batch is not allowlisted, GoClaw runs the full batch sequentially.
- Tool completion events can appear out of order while running in parallel.
- Saved session/transcript order remains stable.

### Configuration

```json
{
  "gateway": {
    "toolExecution": {
      "parallelEnabled": true,
      "maxConcurrent": 3,
      "parallelAllowlist": []
    }
  }
}
```

| Option | Default | Description |
|--------|---------|-------------|
| `parallelEnabled` | `true` | Enable allowlisted parallel batch execution |
| `maxConcurrent` | `3` | Max worker count for a parallel tool batch |
| `parallelAllowlist` | `[]` | Empty uses built-in defaults; otherwise use your explicit list |

Built-in allowlist (used when `parallelAllowlist` is empty): `read`, `web_search`, `web_fetch`, `memory_get`, `memory_search`, `transcript`.

## web_search providers

`web_search` supports `grok`, `brave`, `perplexity`, and `gemini`.

- API keys are read from `goclaw.json` (`tools.web.search.providers.<provider>.apiKey`).
- If your `goclaw.json` uses `${VAR}` placeholders, GoClaw expands them at config load.
- Legacy `tools.web.braveApiKey` still works as a fallback for Brave.
- If `provider` is `auto`, selection order is: `grok`, `brave`, `perplexity`, `gemini`.
- On retryable failures (such as `429`, `408`, timeouts, `5xx`), `web_search` retries and can fall back to the next provider.

### Change it in the web editor

Go to **Gateway Settings → Tool Execution** and adjust:

- **Enable Parallel Tool Execution**
- **Max Concurrent Tools**
- **Parallel Allowlist**

## Tool Permissions

Tool access is controlled in two layers:

### Role-Based (goclaw.json)

Roles set default tool access:

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

You can override role defaults for specific users:

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

When `permissions` is set, it replaces the role's tool list for that user.

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

---

## Delegated Subagent Tools

These tools are owner-only and are enabled behind `tools.subagent.enabled`.

Canonical design and behavior documentation lives in [Delegated Runs](delegated-runs.md).

- `subagent_spawn`
  - Starts one background worker and returns `runId` immediately.
  - Best when you want work to continue separately and hear back later.
  - `notifyOnComplete=true` by default, so the worker reports back when it finishes.
- `subagent_status`
  - List workers, inspect one worker by `runId`, read logs, or message a running worker.
  - Use `action=info` when you want the stored result for one worker.
  - Use `steer` to guide a worker and `send` to place a message into its session.
- `subagent_cancel`
  - Stop a run by `runId`.
  - `cascade=true` (default) also stops child runs.
  - `kill` is the hard-stop version and only works on the run itself.
- `subagent_fanout`
  - Starts several workers in parallel from one request.
  - Best when you want several results back in the current turn so you can compare or interpret them yourself.
  - Returns worker results inline when they fit. If not, it returns as many full results as fit plus `runId` handles for the rest.
  - `includeSummary=true` adds one optional extra summary, but the main result is still the worker outputs.
  - If a worker fails, times out, is canceled, or fails to start, fanout returns `ok=false` with a status/message explaining that the overall fanout was only partially successful.
  - `notifyOnComplete=false` by default; enable it only if you also want a later callback after the immediate result.

Operational UI/API for delegated runs:

- `/runners` owner dashboard
- `/api/runners`
- `/api/runners/:runId`
- `/api/runners/events`
- `POST /api/runners/:runId/cancel`
