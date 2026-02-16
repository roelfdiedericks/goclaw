---
title: "Ollama"
description: "Configure locally-running Ollama for inference, embeddings, and summarization"
section: "LLM Providers"
weight: 30
---

# Ollama Provider

The Ollama provider connects GoClaw to locally-running Ollama for inference, embeddings, and summarization.

## Configuration

```json
{
  "llm": {
    "registry": {
      "providers": {
        "ollama": {
          "type": "ollama",
          "url": "http://localhost:11434"
        }
      },
      "summarization": {
        "models": ["ollama/qwen2.5:7b"]
      },
      "embeddings": {
        "models": ["ollama/nomic-embed-text"]
      }
    }
  }
}
```

### Options

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `url` | string | - | Ollama server URL |
| `maxTokens` | int | - | Output token limit |
| `contextTokens` | int | auto | Context window override (queried from Ollama) |
| `timeoutSeconds` | int | 300 | Request timeout |
| `embeddingOnly` | bool | false | Use only for embeddings |

## Use Cases

### Summarization

Ollama is commonly used for compaction summaries to avoid cloud API costs:

```json
{
  "llm": {
    "registry": {
      "providers": {
        "ollama-summarize": {
          "type": "ollama",
          "url": "http://localhost:11434"
        },
        "claude": {
          "type": "anthropic",
          "apiKey": "YOUR_API_KEY"
        }
      },
      "agent": {
        "models": ["claude/claude-sonnet-4-20250514"]
      },
      "summarization": {
        "models": ["ollama-summarize/qwen2.5:7b", "claude/claude-3-haiku-20240307"]
      }
    }
  }
}
```

This uses Ollama for summarization (free, local) with Anthropic as fallback.

### Embeddings

For semantic search (memory_search, transcript_search):

```json
{
  "memorySearch": {
    "enabled": true,
    "ollama": {
      "url": "http://localhost:11434",
      "model": "nomic-embed-text"
    }
  }
}
```

Or via the registry:

```json
{
  "llm": {
    "registry": {
      "providers": {
        "ollama-embed": {
          "type": "ollama",
          "url": "http://localhost:11434",
          "embeddingOnly": true
        }
      },
      "embeddings": {
        "models": ["ollama-embed/nomic-embed-text"]
      }
    }
  }
}
```

### Agent (Local-Only)

For fully local operation:

```json
{
  "llm": {
    "registry": {
      "providers": {
        "ollama": {
          "type": "ollama",
          "url": "http://localhost:11434",
          "contextTokens": 131072
        }
      },
      "agent": {
        "models": ["ollama/qwen2.5:32b"]
      },
      "summarization": {
        "models": ["ollama/qwen2.5:7b"]
      },
      "embeddings": {
        "models": ["ollama/nomic-embed-text"]
      }
    }
  }
}
```

## Recommended Models

| Use Case | Model | Notes |
|----------|-------|-------|
| Summarization | `qwen2.5:7b` | Good balance of speed and quality |
| Summarization | `llama3.2:3b` | Faster, lower quality |
| Embeddings | `nomic-embed-text` | Best for semantic search |
| Embeddings | `all-minilm` | Faster, smaller vectors |
| Agent | `qwen2.5:32b` | Large context, good tool use |

## Context Window

Ollama queries the model's context size automatically. Override with `contextTokens` if needed:

```json
{
  "providers": {
    "ollama": {
      "type": "ollama",
      "url": "http://localhost:11434",
      "contextTokens": 131072
    }
  }
}
```

## Troubleshooting

### "Ollama not available"

1. Check Ollama is running:
   ```bash
   curl http://localhost:11434/api/tags
   ```
2. Start Ollama:
   ```bash
   ollama serve
   ```
3. Verify URL in config matches server address

### "context deadline exceeded"

Increase timeout or use a smaller model:

```json
{
  "providers": {
    "ollama": {
      "type": "ollama",
      "url": "http://localhost:11434",
      "timeoutSeconds": 600
    }
  }
}
```

### Model Not Found

Pull the model first:

```bash
ollama pull qwen2.5:7b
ollama pull nomic-embed-text
```

### Slow Performance

- Use GPU acceleration if available
- Try smaller models (`7b` instead of `14b`)
- Reduce `contextTokens` if not needed

---

## See Also

- [LLM Providers](../llm-providers.md) — Provider overview
- [Embeddings](../embeddings.md) — Embedding configuration
- [Session Management](../session-management.md) — Summarization config
