---
title: "Embeddings"
description: "Vector embeddings powering semantic search for memory, transcripts, and Memory Graph"
section: "Agent Memory"
weight: 10
---

# Embeddings

Embeddings power semantic search in GoClaw, enabling `memory_search`, `transcript`, and Memory Graph queries to find content by meaning rather than exact keywords.

## Overview

Embeddings convert text into numerical vectors that capture semantic meaning. Similar content produces similar vectors, allowing search by concept rather than literal match.

GoClaw uses embeddings for:
- **Memory search** — Find relevant memory file chunks
- **Transcript search** — Find past conversation segments
- **Memory Graph** — Semantic similarity for `memory_graph_recall` and `memory_graph_query` tools

By default, GoClaw includes a built-in local embeddings provider named `hugot-local`. New configs and edited configs automatically keep this provider available, so embeddings can work even if your main chat model is something like Anthropic.

## Configuration

### Via LLM Purpose Chain (Recommended)

Configure embeddings using the `embeddings` purpose in your LLM config:

```json
{
  "llm": {
    "providers": {
      "hugot-local": {
        "driver": "hugot",
        "embeddingOnly": true
      }
    },
    "embeddings": {
      "models": ["hugot-local/KnightsAnalytics/all-MiniLM-L6-v2"]
    }
  }
}
```

The `models` array is a fallback chain — if the first model fails, the next is tried. See [LLM Providers](llm-providers.md#purpose-chains) for details on purpose chains.

### Built-In Default Behavior

If `llm.embeddings.models` is empty, GoClaw seeds the default local embeddings model automatically:

```json
{
  "llm": {
    "providers": {
      "hugot-local": {
        "driver": "hugot",
        "embeddingOnly": true
      }
    },
    "embeddings": {
      "models": ["hugot-local/KnightsAnalytics/all-MiniLM-L6-v2"]
    }
  }
}
```

The `hugot-local` provider is treated as built-in. If it is missing from config later, GoClaw restores it automatically. If you already configured an embeddings chain, GoClaw leaves that chain unchanged.

### Memory Query Config

Search behavior for memory search is configured under `memory.query`:

```json
{
  "memory": {
    "enabled": true,
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

## Recommended Models

| Model | Provider | Dimensions | Notes |
|-------|----------|------------|-------|
| `KnightsAnalytics/all-MiniLM-L6-v2` | Hugot | 384 | Built-in default, recommended |
| `nomic-embed-text` | Ollama | 768 | Alternative local option |
| `text-embedding-3-small` | OpenAI | 1536 | Cloud option |

## Storage

Embeddings are stored in SQLite alongside the content they index:

| Database | Tables | Content |
|----------|--------|---------|
| `~/.goclaw/sessions.db` | `memory`, `transcripts` | Memory file chunks, conversation chunks |
| `~/.goclaw/memorygraph.db` | `memories` | Memory Graph entities with embeddings |

Memory Graph maintains its own embeddings in `memorygraph.db`, separate from the file-based memory search.

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

Model: KnightsAnalytics/all-MiniLM-L6-v2
Provider: hugot-local
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

Configure search behavior in `memory.query`:

```json
{
  "memory": {
    "enabled": true,
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
3. If using the built-in local provider, run a search once and allow the model to download on first use

### Slow Search

1. Increase `minScore` to filter weak matches
2. Reduce indexed paths
3. If you are using a custom provider, try a smaller embeddings model

### Model Changed

After changing embedding models, run `/embeddings rebuild` to re-index with the new model.

### First Search Downloads a Model

The built-in `hugot-local` provider downloads its model the first time GoClaw needs to use it. This can make the first semantic search slower than later searches. The downloaded model is cached under `~/.goclaw/hugot/models/`.

---

## See Also

- [Agent Memory](agent-memory.md) — Memory system overview
- [Memory Graph](memory-graph.md) — Semantic knowledge graph
- [Memory Search](memory-search.md) — memory_search tool
- [Transcript Search](transcript-search.md) — transcript tool
- [LLM Providers](llm-providers.md) — Provider configuration
