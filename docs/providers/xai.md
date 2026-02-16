---
title: "xAI"
description: "Configure Grok models via xAI API with reasoning and server-side tools"
section: "LLM Providers"
weight: 40
---

# xAI Provider

The xAI provider connects GoClaw to Grok models via the xAI API. It supports stateful conversations, server-side tools, and reasoning.

## Configuration

```json
{
  "llm": {
    "registry": {
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
}
```

### Options

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `apiKey` | string | - | xAI API key |
| `maxTokens` | int | - | Output token limit |
| `contextTokens` | int | 131072 | Context window size |
| `timeoutSeconds` | int | 300 | Request timeout |
| `serverToolsAllowed` | string[] | all | Server-side tools to enable |
| `maxTurns` | int | - | Max agentic turns |
| `incrementalContext` | bool | true | Chain context, send only new messages |

## Models

Only Grok-4 family models are supported (they handle both vision and server-side tools):

| Model | Description |
|-------|-------------|
| `grok-4-1-fast-reasoning` | Default, fast with reasoning |
| `grok-4-1-fast-non-reasoning` | Fast without reasoning |
| `grok-4-fast-reasoning` | Fast with reasoning |
| `grok-4-fast-non-reasoning` | Fast without reasoning |
| `grok-4` | Standard |
| `grok-4-0414` | Dated version |
| `grok-4-0709` | Dated version |
| `grok-4-1` | Standard v1 |

Other models (grok-2, grok-3, grok-vision-beta) are blocked because they don't support both vision and server-side tools simultaneously.

## Features

### Server-Side Tools

xAI provides server-side tools that run on their infrastructure:

| Tool | Description |
|------|-------------|
| `web_search` | Search the web |
| `x_search` | Search X (Twitter) |
| `code_execution` | Run code in sandbox |
| `collections_search` | Search collections |
| `attachment_search` | Search attachments |
| `mcp` | Model Context Protocol |

By default, all known tools are enabled. To limit:

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

### Stateful Conversations

xAI supports context preservation across requests using `previous_response_id`. GoClaw manages this automatically, allowing more efficient conversations without resending full history.

Enable incremental context (default):
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

### Reasoning

Grok models support reasoning via thinking levels:

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

| Level | xAI Effort |
|-------|------------|
| `off` | No reasoning |
| `minimal`, `low` | Low |
| `medium` | Medium |
| `high`, `xhigh` | High |

### Agentic Turns

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

## Tool Name Conflicts

When xAI server tools conflict with GoClaw client tools (e.g., both have `web_search`), the client tool is prefixed with `local_` internally. The LLM sees both options:
- `web_search` — xAI server-side
- `local_web_search` — GoClaw client-side

## Troubleshooting

### "Model not allowed"

Only Grok-4 family models are supported. Update your config to use a supported model.

### Server Tool Errors

If a server-side tool fails, the error is reported back to the LLM for handling.

### Rate Limiting

The provider enters cooldown automatically. Check status with `/llm` command.

---

## See Also

- [LLM Providers](../llm-providers.md) — Provider overview
- [Configuration](../configuration.md) — Full config reference
