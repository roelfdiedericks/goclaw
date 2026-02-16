# Embeddings

Embeddings power semantic search in GoClaw, enabling `memory_search` and `transcript_search` to find content by meaning rather than exact keywords.

## Overview

Embeddings convert text into numerical vectors that capture semantic meaning. Similar content produces similar vectors, allowing search by concept rather than literal match.

GoClaw uses embeddings for:
- **Memory search** — Find relevant memory file chunks
- **Transcript search** — Find past conversation segments

## Configuration

Embeddings use Ollama by default:

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

Or via the LLM registry for more control:

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

## Recommended Models

| Model | Provider | Dimensions | Notes |
|-------|----------|------------|-------|
| `nomic-embed-text` | Ollama | 768 | Best quality, recommended |
| `all-minilm` | Ollama | 384 | Faster, smaller vectors |
| `text-embedding-3-small` | OpenAI | 1536 | Cloud option |

## Storage

Embeddings are stored in SQLite alongside the content they index:

| Table | Content |
|-------|---------|
| `memory` | Memory file chunks and embeddings |
| `transcripts` | Conversation chunks and embeddings |

Location: `~/.goclaw/sessions.db`

## Commands

### Check Status

```
/embeddings
```

Shows embedding coverage:
```
Embeddings Status

Session transcripts: 1,234 chunks
Memory files: 56 chunks

Model: nomic-embed-text
Provider: ollama
```

### Rebuild Embeddings

```
/embeddings rebuild
```

Re-indexes all content with the current model. Use when:
- Changing embedding models
- Embeddings are corrupted
- Adding new content sources

Rebuild runs in the background and may take time for large databases.

## Search Configuration

Configure search behavior in `memorySearch`:

```json
{
  "memorySearch": {
    "enabled": true,
    "ollama": {
      "url": "http://localhost:11434",
      "model": "nomic-embed-text"
    },
    "query": {
      "maxResults": 6,
      "minScore": 0.35,
      "vectorWeight": 0.7,
      "keywordWeight": 0.3
    },
    "paths": []
  }
}
```

| Option | Default | Description |
|--------|---------|-------------|
| `maxResults` | 6 | Maximum results returned |
| `minScore` | 0.35 | Minimum similarity score (0-1) |
| `vectorWeight` | 0.7 | Weight for semantic similarity |
| `keywordWeight` | 0.3 | Weight for keyword matching |
| `paths` | [] | Additional paths to index |

### Hybrid Search

GoClaw uses hybrid search combining:
- **Vector similarity** — Semantic matching via embeddings
- **Keyword matching** — BM25-style term matching

Adjust weights to favor one approach:
- Higher `vectorWeight` → Better for conceptual queries
- Higher `keywordWeight` → Better for specific term lookup

## Troubleshooting

### "No results found"

1. Verify embeddings are indexed: `/embeddings`
2. Lower `minScore` to 0.2
3. Check embedding model is available: `ollama list`

### Slow Search

1. Use smaller model (`all-minilm`)
2. Increase `minScore` to filter weak matches
3. Reduce indexed paths

### Model Changed

After changing embedding models, run `/embeddings rebuild` to re-index with the new model.

---

## See Also

- [Agent Memory](agent-memory.md) — Memory system overview
- [Memory Search](memory-search.md) — memory_search tool
- [Transcript Search](transcript-search.md) — transcript_search tool
- [Ollama Provider](providers/ollama.md) — Ollama configuration
