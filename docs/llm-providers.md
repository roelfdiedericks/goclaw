---
title: "LLM Providers"
description: "Configure AI model providers: Anthropic, OpenAI, Ollama, xAI, and built-in Hugot embeddings"
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
| Hugot | Local | Built-in embeddings-only provider for semantic search |

## Quick Setup

### Minimal Config (Single Provider)

For basic usage with Anthropic:

```json
{
  "llm": {
    "providers": {
      "anthropic": {
        "type": "anthropic",
        "apiKey": "sk-ant-...",
        "promptCaching": true
      }
    },
    "agent": {
      "models": ["anthropic/claude-sonnet-4-20250514"]
    }
  }
}
```

If you leave the embeddings chain empty, GoClaw automatically restores the built-in `hugot-local` provider and seeds the default local embeddings model.

### Multi-Provider Setup

For advanced setups with multiple providers and purpose-specific chains:

```json
{
  "llm": {
    "providers": {
      "claude": {
        "driver": "anthropic",
        "apiKey": "sk-ant-...",
        "promptCaching": true
      },
      "ollama-qwen": {
        "driver": "ollama",
        "url": "http://localhost:11434"
      },
      "hugot-local": {
        "driver": "hugot",
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
      "models": ["hugot-local/KnightsAnalytics/all-MiniLM-L6-v2"]
    }
  }
}
```

---

## Purpose Chains

GoClaw routes LLM requests based on **purpose**:

| Purpose | Config Key | Used For |
|---------|------------|----------|
| `agent` | `agent` | Main conversation, tool use |
| `summarization` | `summarization` | Compaction summaries, checkpoints |
| `embeddings` | `embeddings` | Semantic search (memory, transcripts, Memory Graph) |
| `heartbeat` | `heartbeat` | Periodic heartbeat tasks |
| `cron` | `cron` | Scheduled cron jobs |
| `hass` | `hass` | Home Assistant queries |
| `memory_extraction` | `memoryExtraction` | Memory Graph entity extraction |

If a purpose has no models configured, it falls back to the `agent` chain.

Each purpose has a **model chain** — the first model is primary, others are fallbacks:

```json
{
  "llm": {
    "summarization": {
      "models": [
        "ollama-qwen/qwen2.5:7b",
        "claude/claude-3-haiku-20240307"
      ]
    }
  }
}
```

The first model (`ollama-qwen/qwen2.5:7b`) is tried first. If it fails, the next model in the chain is used as fallback.

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
      "name": "Alice",
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
  "driver": "anthropic",       // Required: provider driver
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
  "driver": "anthropic",
  "promptCaching": true        // Enable prompt caching (reduces cost)
}
```

**OpenAI:**
```json
{
  "driver": "openai",
  "baseURL": "https://api.openai.com/v1"  // Or compatible endpoint
}
```

**Ollama:**
```json
{
  "driver": "ollama",
  "url": "http://localhost:11434",
  "embeddingOnly": true        // Skip chat availability check
}
```

**Hugot (embeddings only):**
```json
{
  "driver": "hugot",
  "embeddingOnly": true
}
```

Hugot is the built-in local embeddings provider. It is intended for the `embeddings` purpose, not for `agent` or `summarization`.

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
hugot-local/KnightsAnalytics/all-MiniLM-L6-v2
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
- [Embeddings](embeddings.md) — Embeddings purpose configuration
- [Memory Graph](memory-graph.md) — Memory extraction purpose
- [Configuration](configuration.md) — Full config reference
- [Session Management](session-management.md) — Summarization config
