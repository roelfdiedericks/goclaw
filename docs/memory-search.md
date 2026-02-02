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

### 1. Configure Ollama for Embeddings

Memory search requires an embedding model. Ollama works well:

```bash
# Install Ollama (if not already)
curl -fsSL https://ollama.com/install.sh | sh

# Pull an embedding model
ollama pull nomic-embed-text
```

### 2. Enable Memory Search

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

### 3. Configure Search Behavior (Optional)

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
    "paths": [
      "notes/",
      "journals/"
    ]
  }
}
```

---

## Configuration Options

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable memory search |
| `ollama.url` | string | - | Ollama API URL |
| `ollama.model` | string | - | Embedding model name |
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
2. Embedded via Ollama
3. Stored in an in-memory index

### Searching

When the agent uses `memory_search`:

```
1. Query → Ollama embedding
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

### `memory_list`

List available memory files:

```json
{
  "tool": "memory_list",
  "input": {}
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

Recommended models for `nomic-embed-text`:

| Model | Dimensions | Speed | Quality |
|-------|------------|-------|---------|
| `nomic-embed-text` | 768 | Fast | Good |
| `mxbai-embed-large` | 1024 | Medium | Better |
| `all-minilm` | 384 | Fastest | Basic |

Pull with:
```bash
ollama pull nomic-embed-text
```

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
- Using GPU-accelerated Ollama
- Reducing number of files indexed
- Using a smaller model (`all-minilm`)

---

## See Also

- [Configuration](./configuration.md) - Memory search config
- [Architecture](./architecture.md) - System overview
- [Tools](./tools.md) - All agent tools
