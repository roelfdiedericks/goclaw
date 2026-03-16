---
title: "Memory Search"
description: "Semantic search over memory files using embeddings"
section: "Agent Memory"
weight: 20
---

# Memory Search

GoClaw includes semantic search over memory files using embeddings.

## Overview

Memory search allows the agent to find relevant information from:
- `memory/` directory (daily notes)
- `MEMORY.md` (long-term memory)
- Custom paths you configure

Search combines:
- **Vector search** (semantic similarity via embeddings)
- **Keyword search** (BM25 full-text search)

---

## Setup

### 1. Use the Built-In Embeddings Provider

Memory search requires an embedding model. By default, GoClaw uses the built-in local Hugot provider for semantic search:

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

The first semantic search may take longer because the model downloads on first use and is then cached under `~/.goclaw/hugot/models/`.

### 2. Enable Memory Search

```json
{
  "memory": {
    "enabled": true
  }
}
```

### 3. Configure Search Behavior (Optional)

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
    "paths": [
      "notes/",
      "journals/"
    ]
  }
}
```

### Alternative: Ollama for Embeddings

If you prefer Ollama for embeddings, configure it explicitly:

```bash
# Install Ollama
curl -fsSL https://ollama.com/install.sh | sh

# Pull an embedding model
ollama pull nomic-embed-text
```

---

## Configuration Options

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Enable memory search |
| `query.maxResults` | int | `6` | Maximum results per search |
| `query.minScore` | float | `0.35` | Minimum similarity score (0-1) |
| `query.vectorWeight` | float | `0.7` | Weight for semantic search |
| `query.keywordWeight` | float | `0.3` | Weight for keyword search |
| `paths` | string[] | `[]` | Additional paths to index |

---

## How It Works

### Indexing

When enabled, GoClaw indexes:
1. All `.md` files in `memory/` directory
2. `MEMORY.md` in workspace root
3. Files in custom `paths`

Files are:
1. Read and chunked into segments
2. Embedded via the configured embeddings provider
3. Stored in an in-memory index

### Searching

When the agent uses `memory_search`:

```
1. Query → embedding
2. Vector search (cosine similarity)
3. Keyword search (BM25)
4. Combine scores: vector * 0.7 + keyword * 0.3
5. Return top N results above minScore
```

---

## Agent Tools

### `memory_search`

Search memory files:

```json
{
  "tool": "memory_search",
  "input": {
    "query": "What did we discuss about authentication?"
  }
}
```

Returns:
```json
{
  "results": [
    {
      "file": "memory/2024-01-15.md",
      "content": "Discussed JWT vs session auth...",
      "score": 0.85
    }
  ]
}
```

### `memory_get`

Read full content from a memory file. Use after `memory_search` to get complete context around a match.

```json
{
  "tool": "memory_get",
  "input": {
    "path": "memory/2024-01-15.md"
  }
}
```

Optional parameters:

| Field | Type | Description |
|-------|------|-------------|
| `path` | string | Path to memory file (required) |
| `from` | int | Start reading from this line (1-indexed) |
| `lines` | int | Number of lines to read (omit for entire file) |

Example reading a specific section:

```json
{
  "tool": "memory_get",
  "input": {
    "path": "memory/2024-01-15.md",
    "from": 10,
    "lines": 20
  }
}
```

---

## Memory File Format

### Daily Notes (`memory/YYYY-MM-DD.md`)

```markdown
# 2024-01-15

## Worked on
- Authentication system
- Database migrations

## Decisions
- Using JWT for API auth
- PostgreSQL for main database

## Notes
User mentioned preference for...
```

### Long-term Memory (`MEMORY.md`)

```markdown
# Long-term Memory

## User Preferences
- Prefers concise responses
- Uses VSCode as editor

## Project Context
- Main project: GoClaw
- Tech stack: Go, SQLite, Anthropic

## Important Decisions
- 2024-01-10: Chose SQLite over PostgreSQL for simplicity
```

---

## Embedding Models

Recommended embeddings choices:

| Model | Dimensions | Speed | Quality |
|-------|------------|-------|---------|
| `KnightsAnalytics/all-MiniLM-L6-v2` | 384 | Fast | Good |
| `nomic-embed-text` | 768 | Fast | Good |
| `mxbai-embed-large` | 1024 | Medium | Better |
| `all-minilm` | 384 | Fastest | Basic |

---

## Performance

### Index Size

Memory usage scales with:
- Number of files indexed
- Chunk size
- Embedding dimensions

Rough estimate: ~1KB per chunk (768-dim float32)

### Search Speed

Typical search: 10-50ms for small indexes (<1000 chunks)

For large indexes, consider:
- Reducing `paths` scope
- Increasing `minScore` threshold
- Decreasing `maxResults`

---

## Troubleshooting

### "Model download is slow the first time"

The built-in `hugot-local` provider downloads its model on first use. Later searches reuse the cached model.

### "Ollama not available"

```bash
# Check Ollama is running
curl http://localhost:11434/api/tags

# Start Ollama if needed
ollama serve
```

### "No results found"

1. Check files exist in `memory/` or configured `paths`
2. Lower `minScore` threshold
3. Check query is meaningful (not too short/generic)

### Slow Indexing

Embedding generation can be slow on CPU. Consider:
- Reducing number of files indexed
- Using a smaller model
- Switching to a different embeddings provider if you prefer

---

## See Also

- [Embeddings](embeddings.md) — Embedding configuration
- [Agent Memory](agent-memory.md) — Memory system overview
- [Transcript Search](transcript-search.md) — Search conversation history
- [Configuration](configuration.md) — Full config reference
