---
title: "Anthropic"
description: "Configure Claude models via the Anthropic API with prompt caching and extended thinking"
section: "LLM Providers"
weight: 10
---

# Anthropic Provider

The Anthropic provider connects GoClaw to Claude models via the Anthropic API.

## Configuration

```json
{
  "llm": {
    "providers": {
      "anthropic": {
        "type": "anthropic",
        "apiKey": "YOUR_API_KEY",
        "promptCaching": true
      }
    },
    "agent": {
      "models": ["anthropic/claude-sonnet-4-20250514"]
    }
  }
}
```

**Note:** The setup wizard (`goclaw setup`) can detect `ANTHROPIC_API_KEY` from your environment and offer to write it to config.

### Provider Options

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `type` | string | - | Must be `"anthropic"` |
| `apiKey` | string | - | Anthropic API key |
| `maxTokens` | int | 200000 | Context window size |
| `promptCaching` | bool | true | Enable prompt caching (reduces cost) |
| `timeoutSeconds` | int | 300 | Request timeout |

Models are specified in the `agent.models` array using `provider/model` format (e.g., `anthropic/claude-sonnet-4-20250514`).

## Models

| Model | Context | Best For |
|-------|---------|----------|
| `claude-sonnet-4-20250514` | 200k | General agent use, balanced |
| `claude-opus-4-20250514` | 200k | Complex reasoning |
| `claude-3-haiku-20240307` | 200k | Fast, cheap summarization |
| `claude-3-5-sonnet-20241022` | 200k | Previous generation |

## Features

### Prompt Caching

When `promptCaching: true`, the system prompt is cached server-side by Anthropic. This reduces costs by up to 90% for repeated requests with the same system prompt.

Cache expires after 5 minutes of inactivity.

### Extended Thinking

Anthropic supports extended thinking (reasoning) on Claude 3.5+ models. Configure per-user in `users.json`:

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

| Level | Token Budget | Use Case |
|-------|--------------|----------|
| `off` | 0 | Disabled |
| `minimal` | 1,024 | Quick responses |
| `low` | 4,096 | Light reasoning |
| `medium` | 10,000 | Balanced (default) |
| `high` | 25,000 | Deep reasoning |
| `xhigh` | 50,000 | Maximum effort |

## Multi-Provider Setup

For setups with multiple providers:

```json
{
  "llm": {
    "providers": {
      "claude": {
        "type": "anthropic",
        "apiKey": "YOUR_API_KEY",
        "promptCaching": true
      },
      "ollama": {
        "type": "ollama",
        "url": "http://localhost:11434"
      }
    },
    "agent": {
      "models": ["claude/claude-sonnet-4-20250514"]
    },
    "summarization": {
      "models": ["ollama/qwen2.5:7b"]
    }
  }
}
```

## Troubleshooting

### "invalid_api_key"

Verify your API key:
1. Check it starts with `sk-ant-`
2. Verify it's not expired in the Anthropic console
3. Check the `apiKey` field in `goclaw.json`

### Rate Limiting

If you hit rate limits, the provider enters cooldown with automatic retry. Check status with `/llm` command.

### Model Not Available

Some models require specific API access levels. Check your Anthropic account permissions.

---

## See Also

- [LLM Providers](../llm-providers.md) — Provider overview
- [Configuration](../configuration.md) — Full config reference
