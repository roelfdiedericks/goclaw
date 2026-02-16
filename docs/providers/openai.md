---
title: "OpenAI"
description: "Configure GPT models and OpenAI-compatible APIs (LM Studio, LocalAI, OpenRouter)"
section: "LLM Providers"
weight: 20
---

# OpenAI Provider

The OpenAI provider connects GoClaw to OpenAI models and any OpenAI-compatible API (LM Studio, LocalAI, OpenRouter, Kimi, etc.).

## Configuration

### OpenAI

```json
{
  "llm": {
    "registry": {
      "providers": {
        "openai": {
          "type": "openai",
          "apiKey": "YOUR_API_KEY"
        }
      },
      "agent": {
        "models": ["openai/gpt-4o"]
      }
    }
  }
}
```

### Local Server (LM Studio)

```json
{
  "llm": {
    "registry": {
      "providers": {
        "lmstudio": {
          "type": "openai",
          "baseURL": "http://localhost:1234"
        }
      },
      "agent": {
        "models": ["lmstudio/your-model-name"]
      }
    }
  }
}
```

API key is optional for local servers.

### OpenRouter

```json
{
  "llm": {
    "registry": {
      "providers": {
        "openrouter": {
          "type": "openai",
          "apiKey": "YOUR_OPENROUTER_KEY",
          "baseURL": "https://openrouter.ai/api"
        }
      },
      "agent": {
        "models": ["openrouter/anthropic/claude-3-opus"]
      }
    }
  }
}
```

OpenRouter requests include GoClaw attribution headers automatically.

### Options

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `apiKey` | string | - | API key (optional for local servers) |
| `baseURL` | string | OpenAI | API endpoint (auto-appends `/v1` if needed) |
| `maxTokens` | int | - | Output token limit |
| `contextTokens` | int | auto | Context window override |
| `timeoutSeconds` | int | 300 | Request timeout |
| `embeddingOnly` | bool | false | Use only for embeddings |

## Compatible APIs

The OpenAI provider works with any API that follows the OpenAI chat completions format:

| Service | Base URL | Notes |
|---------|----------|-------|
| OpenAI | (default) | Official API |
| LM Studio | `http://localhost:1234` | Local inference |
| LocalAI | `http://localhost:8080` | Local inference |
| OpenRouter | `https://openrouter.ai/api` | Multi-provider gateway |
| Kimi | `https://api.moonshot.cn` | Moonshot AI |
| Together.ai | `https://api.together.xyz` | Cloud inference |

## Features

### Tool Calling

Supports native function calling for models that implement it (GPT-4, GPT-4o, etc.).

### Vision

Supports image inputs for vision-capable models.

### Embeddings

Can be used for embeddings with models like `text-embedding-3-small`:

```json
{
  "llm": {
    "registry": {
      "providers": {
        "openai-embed": {
          "type": "openai",
          "apiKey": "YOUR_API_KEY",
          "embeddingOnly": true
        }
      },
      "embeddings": {
        "models": ["openai-embed/text-embedding-3-small"]
      }
    }
  }
}
```

### Reasoning (OpenRouter)

When using OpenRouter with reasoning-capable models, thinking levels are mapped to OpenRouter's `reasoning.effort` parameter.

## Troubleshooting

### Connection Refused (Local Server)

1. Verify the server is running
2. Check the port matches your config
3. Ensure the server exposes an OpenAI-compatible endpoint

### Model Not Found

The model name must match exactly what the API expects. For local servers, check the model name with:

```bash
curl http://localhost:1234/v1/models
```

### Rate Limiting

The provider enters cooldown automatically on rate limits. Check status with `/llm` command.

---

## See Also

- [LLM Providers](../llm-providers.md) — Provider overview
- [Ollama Provider](ollama.md) — Alternative for local inference
- [Configuration](../configuration.md) — Full config reference
