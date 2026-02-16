---
title: "LLM Providers"
description: "Configure AI model providers: Anthropic, OpenAI, Ollama, and xAI"
section: "LLM Providers"
weight: 1
landing: true
---

# LLM Providers

GoClaw supports multiple LLM providers through a unified registry system. This enables flexible model selection, automatic failover, and purpose-specific provider chains.

## Supported Providers

| Provider | Type | Use Cases |
|----------|------|-----------|
| [Anthropic](providers/anthropic.md) | Cloud | Agent responses (Claude), extended thinking, prompt caching |
| [OpenAI](providers/openai.md) | Cloud/Local | GPT models, OpenAI-compatible APIs (LM Studio, LocalAI) |
| [Ollama](providers/ollama.md) | Local | Local inference, embeddings, summarization |
| [xAI](providers/xai.md) | Cloud | Grok models, stateful conversations, server-side tools |

## Quick Setup

### Single Provider (Simple)

For basic usage with one provider:

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

### Multi-Provider (Registry)

For advanced setups with multiple providers and purpose chains:

```json
{
  "llm": {
    "registry": {
      "providers": {
        "claude": {
          "type": "anthropic",
          "apiKey": "sk-ant-...",
          "promptCaching": true
        },
        "ollama-qwen": {
          "type": "ollama",
          "url": "http://localhost:11434"
        },
        "ollama-embed": {
          "type": "ollama",
          "url": "http://localhost:11434",
          "embeddingOnly": true
        }
      },
      "agent": {
        "models": ["claude/claude-sonnet-4-20250514"]
      },
      "summarization": {
        "models": ["ollama-qwen/qwen2.5:7b", "claude/claude-3-haiku-20240307"]
      },
      "embeddings": {
        "models": ["ollama-embed/nomic-embed-text"]
      }
    }
  }
}
```

---

## Purpose Chains

The registry routes requests based on **purpose**:

| Purpose | Used For |
|---------|----------|
| `agent` | Main conversation, tool use |
| `summarization` | Compaction summaries, checkpoints |
| `embeddings` | Semantic search vectors |

Each purpose has a **model chain** — the first model is primary, others are fallbacks:

```json
{
  "summarization": {
    "models": [
      "ollama-qwen/qwen2.5:7b",     // Primary: local, free
      "claude/claude-3-haiku-20240307"  // Fallback: cloud
    ]
  }
}
```

### Automatic Failover

When a provider fails:

1. Error is classified (rate limit, auth, timeout, server error)
2. Provider enters **cooldown** with exponential backoff
3. Next model in chain is tried
4. After cooldown expires, original provider is tried again

Check provider status with `/llm` command:
```
LLM Provider Status

claude: healthy
ollama-qwen: cooldown (rate_limit), retry in 2m30s
ollama-embed: healthy
```

---

## Thinking Levels

Extended thinking/reasoning can be enabled for supported models. This tells the LLM to "think through" complex problems before responding.

### Available Levels

| Level | Description | Anthropic Tokens |
|-------|-------------|------------------|
| `off` | No extended thinking | 0 |
| `minimal` | Quick responses | 1,024 |
| `low` | Light reasoning | 4,096 |
| `medium` | Balanced (default) | 10,000 |
| `high` | Deep reasoning | 25,000 |
| `xhigh` | Maximum effort | 50,000 |

### Configuration

Per-user in `users.json`:
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

Or dynamically via Telegram/TUI settings.

### Provider Support

| Provider | Thinking Support |
|----------|------------------|
| Anthropic | Yes (Claude 3.5+), token budget |
| OpenAI | Via OpenRouter reasoning |
| Ollama | Model-dependent |
| xAI | Yes (grok-3-mini), effort levels |

---

## Provider Configuration

### Common Options

All providers support:

```json
{
  "type": "anthropic",         // Required: provider type
  "apiKey": "...",             // API key (or env var)
  "maxTokens": 8192,           // Output limit override
  "contextTokens": 200000,     // Context window override
  "timeoutSeconds": 300,       // Request timeout
  "trace": true,               // Enable request tracing
  "dumpOnSuccess": false       // Keep request dumps on success
}
```

### Provider-Specific Options

**Anthropic:**
```json
{
  "type": "anthropic",
  "promptCaching": true        // Enable prompt caching (reduces cost)
}
```

**OpenAI:**
```json
{
  "type": "openai",
  "baseURL": "https://api.openai.com/v1"  // Or compatible endpoint
}
```

**Ollama:**
```json
{
  "type": "ollama",
  "url": "http://localhost:11434",
  "embeddingOnly": true        // Skip chat availability check
}
```

**xAI:**
```json
{
  "type": "xai",
  "serverToolsAllowed": ["web_search"],  // Server-side tools
  "maxTurns": 5                // Max agentic turns
}
```

---

## Model Reference Format

Models are referenced as `provider/model`:

```
claude/claude-sonnet-4-20250514
ollama-qwen/qwen2.5:7b
openai/gpt-4o
xai/grok-3
```

The provider name is the key from your `providers` config, not the provider type.

---

## Cooldown Management

### View Status

```
/llm
```

Shows all providers, their status, and any cooldowns.

### Clear Cooldown

```
/llm clear <provider>
```

Manually clears a provider's cooldown to retry immediately.

### Cooldown Behavior

| Error Type | Initial Cooldown | Max Cooldown |
|------------|------------------|--------------|
| Rate limit | 30s | 5 min |
| Auth error | 1 hour | 1 hour |
| Server error | 1 min | 10 min |
| Timeout | 30s | 5 min |

Cooldowns use exponential backoff within these ranges.

---

## See Also

- [Anthropic Provider](providers/anthropic.md) — Claude models, prompt caching
- [OpenAI Provider](providers/openai.md) — GPT and compatible APIs
- [Ollama Provider](providers/ollama.md) — Local inference
- [xAI Provider](providers/xai.md) — Grok models
- [Configuration](configuration.md) — Full config reference
- [Session Management](session-management.md) — Summarization config
