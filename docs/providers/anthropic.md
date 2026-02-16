# Anthropic Provider

The Anthropic provider connects GoClaw to Claude models via the Anthropic API.

## Configuration

```json
{
  "llm": {
    "provider": "anthropic",
    "model": "claude-sonnet-4-20250514",
    "apiKey": "YOUR_API_KEY",
    "maxTokens": 200000,
    "promptCaching": true
  }
}
```

Or via environment variable:
```bash
export ANTHROPIC_API_KEY="YOUR_API_KEY"
```

### Options

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `apiKey` | string | - | Anthropic API key (or use `ANTHROPIC_API_KEY` env) |
| `model` | string | - | Model name (e.g., `claude-sonnet-4-20250514`) |
| `maxTokens` | int | 200000 | Context window size |
| `promptCaching` | bool | true | Enable prompt caching (reduces cost) |
| `timeoutSeconds` | int | 300 | Request timeout |

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

## Registry Configuration

For multi-provider setups:

```json
{
  "llm": {
    "registry": {
      "providers": {
        "claude": {
          "type": "anthropic",
          "apiKey": "YOUR_API_KEY",
          "promptCaching": true
        }
      },
      "agent": {
        "models": ["claude/claude-sonnet-4-20250514"]
      }
    }
  }
}
```

## Troubleshooting

### "invalid_api_key"

Verify your API key:
1. Check it starts with `sk-ant-`
2. Verify it's not expired in the Anthropic console
3. Check environment variable if using `ANTHROPIC_API_KEY`

### Rate Limiting

If you hit rate limits, the provider enters cooldown with automatic retry. Check status with `/llm` command.

### Model Not Available

Some models require specific API access levels. Check your Anthropic account permissions.

---

## See Also

- [LLM Providers](../llm-providers.md) — Provider overview
- [Configuration](../configuration.md) — Full config reference
