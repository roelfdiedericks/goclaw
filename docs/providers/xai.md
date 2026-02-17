---
title: "xAI"
description: "Configure Grok models via xAI API with reasoning, server-side tools, and vision"
section: "LLM Providers"
weight: 40
---

# xAI Provider

The xAI provider connects GoClaw to Grok models via xAI's gRPC API. It's built on [xai-go](https://github.com/roelfdiedericks/xai-go), a custom Go gRPC client for maximum performance.

## Highlights

- **2M context window** — Handle massive conversations, entire codebases, long documents
- **Server-side tools** — Web search, X search, code execution run on xAI infrastructure
- **Stateful conversations** — Context chaining reduces token usage and improves coherence
- **Vision** — Process images from Telegram, browser screenshots, etc.
- **Reasoning** — Configurable thinking levels for complex tasks
- **Image generation** — Create images via the `xai_imagine` tool
- **gRPC streaming** — Low-latency response streaming
- **Competitive pricing** — $0.20/$0.50 per million tokens (fast models)

## Configuration

```json
{
  "llm": {
    "providers": {
      "xai": {
        "type": "xai",
        "apiKey": "YOUR_XAI_API_KEY"
      }
    },
    "agent": {
      "models": ["xai/grok-4-1-fast-reasoning"]
    }
  }
}
```

### Options

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `apiKey` | string | required | xAI API key |
| `maxTokens` | int | 4096 | Output token limit |
| `contextTokens` | int | from API | Context window (fetched from xAI API on startup) |
| `timeoutSeconds` | int | 300 | Request timeout |
| `serverToolsAllowed` | string[] | all | Server-side tools to enable |
| `maxTurns` | int | 25 | Max agentic turns for server tools |
| `incrementalContext` | bool | true | Chain context across requests |

**Note:** Context window sizes and pricing are fetched from the xAI API on startup. If the API is unreachable, hardcoded fallback values are used.

## Models

Only Grok-4 family models are supported — they handle both vision and server-side tools simultaneously:

| Model | Context | Pricing (in/out) | Use Case |
|-------|---------|------------------|----------|
| `grok-4-1-fast-reasoning` | 2M | $0.20/$0.50 | **Recommended** — Fast with reasoning |
| `grok-4-1-fast-non-reasoning` | 2M | $0.20/$0.50 | Fast, no reasoning overhead |
| `grok-4-fast-reasoning` | 2M | $0.20/$0.50 | Fast with reasoning |
| `grok-4-fast-non-reasoning` | 2M | $0.20/$0.50 | Fast, no reasoning |
| `grok-4-0709` | 256K | $3.00/$15.00 | Dated version |
| `grok-4` | 256K | $3.00/$15.00 | Standard |
| `grok-4-1` | 2M | - | Standard v1 |

Pricing is per million tokens.

**Note:** Older models (grok-2, grok-3, grok-vision-beta) are blocked because they can't handle both vision and server-side tools in the same request.

## Server-Side Tools

xAI provides tools that execute on their infrastructure, not your machine:

| Tool | What It Does |
|------|--------------|
| `web_search` | Search the web with real-time results |
| `x_search` | Search X (Twitter) posts and profiles |
| `code_execution` | Execute code in a secure sandbox |
| `collections_search` | Search xAI collections |
| `attachment_search` | Search uploaded attachments |
| `mcp` | Model Context Protocol |

All tools are enabled by default. To restrict:

```json
{
  "providers": {
    "xai": {
      "type": "xai",
      "apiKey": "YOUR_XAI_API_KEY",
      "serverToolsAllowed": ["web_search", "code_execution"]
    }
  }
}
```

### How Server Tools Work

When the agent needs web search:
1. GoClaw sends the request to xAI
2. xAI's infrastructure performs the search
3. Results stream back through the response
4. Agent sees the results and continues

This is faster than client-side tools and doesn't require API keys for search services.

### Tool Name Conflicts

When xAI server tools conflict with GoClaw client tools (both have `web_search`), the client tool is prefixed with `local_`:

- `web_search` — xAI server-side (fast, no API key needed)
- `local_web_search` — GoClaw client-side (uses your Brave API key)

The LLM sees both and chooses based on the task.

## Stateful Conversations

xAI supports context preservation using `previous_response_id`. GoClaw manages this automatically:

**Without incremental context:**
```
Request 1: [system] + [msg1]
Request 2: [system] + [msg1] + [msg2]
Request 3: [system] + [msg1] + [msg2] + [msg3]  ← Tokens grow each turn
```

**With incremental context (default):**
```
Request 1: [system] + [msg1]              → responseID: abc123
Request 2: previousID: abc123 + [msg2]    → responseID: def456
Request 3: previousID: def456 + [msg3]    ← Only new messages sent
```

This dramatically reduces token usage for long conversations.

```json
{
  "providers": {
    "xai": {
      "type": "xai",
      "apiKey": "YOUR_XAI_API_KEY",
      "incrementalContext": true
    }
  }
}
```

## Reasoning (Thinking)

Grok models support extended reasoning. Configure per-user:

```json
{
  "users": [
    {
      "name": "TheRoDent",
      "role": "owner",
      "thinking": true,
      "thinkingLevel": "medium"
    }
  ]
}
```

| Level | xAI Effort | Use Case |
|-------|------------|----------|
| `off` | None | Simple tasks, fastest |
| `minimal`, `low` | Low | Basic reasoning |
| `medium` | Medium | **Recommended** — Balanced |
| `high`, `xhigh` | High | Complex analysis, coding |

Higher levels take longer but produce better results for complex tasks.

## Vision

Grok-4 models can process images. When you send an image via Telegram or the agent takes a browser screenshot, xAI sees and analyzes it.

No special configuration needed — just use a supported model.

## Image Generation

GoClaw includes the `xai_imagine` tool for generating images:

```json
{
  "tools": {
    "xai_imagine": {
      "enabled": true,
      "apiKey": "YOUR_XAI_API_KEY"
    }
  }
}
```

The agent can then generate images on request. See [xAI Imagine Tool](../tools/xai-imagine.md) for details.

## Agentic Turns

Limit how many turns the model can take when using server-side tools:

```json
{
  "providers": {
    "xai": {
      "type": "xai",
      "apiKey": "YOUR_XAI_API_KEY",
      "maxTurns": 5
    }
  }
}
```

This prevents runaway tool loops. Default is 25.

## Troubleshooting

### "Model not allowed"

Only Grok-4 family models work with GoClaw. Update your config:

```json
"models": ["xai/grok-4-1-fast-reasoning"]
```

### Server Tool Errors

Server-side tool failures are reported back to the LLM, which can retry or work around them.

### Rate Limiting

The provider enters cooldown automatically. Check status:

```
/llm
```

### Context Chain Broken

If the agent seems to "forget" context, the chain may have broken (e.g., after a compaction). It will rebuild automatically on the next request.

---

## See Also

- [xAI Imagine Tool](../tools/xai-imagine.md) — Image generation
- [LLM Providers](../llm-providers.md) — Provider overview
- [Configuration](../configuration.md) — Full config reference
