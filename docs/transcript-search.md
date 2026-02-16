# Transcript Search

GoClaw indexes all conversations into a searchable database, giving your agent persistent memory that survives context compaction.

## Overview

Transcript search solves a fundamental problem with LLM agents: **context windows are finite, but conversations are forever**.

When your context window fills up, GoClaw compacts old messages into summaries. Without transcript search, the details of those conversations are lost to the agent. With transcript search, your agent can query the full history and recover context on demand.

### Key Features

| Feature | Description |
|---------|-------------|
| **Hybrid Search** | Combines semantic embeddings with BM25 keyword matching |
| **Automatic Indexing** | New messages indexed every 30 seconds |
| **Embedding Backfill** | Historical chunks get embeddings added automatically |
| **OpenClaw Import** | Merges OpenClaw conversation history into the index |
| **Real-time Sync** | New OpenClaw messages indexed while running side-by-side |
| **Configurable Chunking** | Control how messages are grouped into searchable units |

### How It Compares

| | GoClaw Transcripts | Claude Insights | ChatGPT Memory |
|---|---|---|---|
| **Storage** | Local (SQLite) | Cloud | Cloud |
| **Privacy** | Your machine | Anthropic servers | OpenAI servers |
| **Persistence** | Permanent | Unknown | Limited |
| **Cross-platform** | Merges OpenClaw + GoClaw | Single platform | Single platform |
| **Search Type** | Semantic + Keyword | Unknown | Keyword? |
| **Offline** | Yes | No | No |

---

## Setup

### 1. Configure Embedding Provider

Transcript search requires embeddings. Any OpenAI-compatible API works:

**Option A: LM Studio (Recommended for local)**
```json
{
  "llm": {
    "providers": {
      "lmstudio": {
        "type": "openai",
        "url": "http://localhost:1234"
      }
    },
    "embeddings": {
      "models": ["lmstudio/text-embedding-nomic-embed-text-v1.5"]
    }
  }
}
```

**Option B: Ollama**
```json
{
  "llm": {
    "providers": {
      "ollama": {
        "type": "ollama",
        "url": "http://localhost:11434"
      }
    },
    "embeddings": {
      "models": ["ollama/nomic-embed-text"]
    }
  }
}
```

### 2. Enable Transcript Indexing

```json
{
  "transcript": {
    "enabled": true,
    "indexIntervalSeconds": 30,
    "batchSize": 100,
    "backfillBatchSize": 20
  }
}
```

### 3. Verify It's Working

After starting GoClaw, you should see:
```
openai: embedding ready name=lmstudio dimensions=768
memory: provider upgraded from=none to=lmstudio
transcript: starting indexer
```

And periodically:
```
transcript: sync completed messagesProcessed=5 chunksCreated=2 progress="500/500 (100%)"
transcript: backfill progress processed=20 remaining=150 elapsed=1.2s
```

---

## Configuration Reference

```json
{
  "transcript": {
    "enabled": true,
    "indexIntervalSeconds": 30,
    "batchSize": 100,
    "backfillBatchSize": 20,
    "maxGroupGapSeconds": 300,
    "maxMessagesPerChunk": 8,
    "maxEmbeddingContentLen": 16000,
    "query": {
      "maxResults": 10,
      "minScore": 0.3,
      "vectorWeight": 0.7,
      "keywordWeight": 0.3
    }
  }
}
```

### Indexing Options

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Enable transcript indexing |
| `indexIntervalSeconds` | int | `30` | How often to check for new messages |
| `batchSize` | int | `100` | Max messages to process per sync cycle |
| `backfillBatchSize` | int | `10` | Max chunks to add embeddings to per cycle |
| `maxGroupGapSeconds` | int | `300` | Max time gap (5 min) before starting new chunk |
| `maxMessagesPerChunk` | int | `8` | Max messages per conversation chunk |
| `maxEmbeddingContentLen` | int | `16000` | Max chars to send to embedding model |

### Search Options

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `query.maxResults` | int | `10` | Maximum results per search |
| `query.minScore` | float | `0.3` | Minimum similarity score (0-1) |
| `query.vectorWeight` | float | `0.7` | Weight for semantic search |
| `query.keywordWeight` | float | `0.3` | Weight for keyword search |

---

## How It Works

### Message → Chunk → Embedding

```
Messages arrive
    ↓
Group by session + time gap (≤5 min)
    ↓
Create conversation chunks (≤8 messages each)
    ↓
Generate embedding via LM Studio/Ollama
    ↓
Store in SQLite with vector index
```

### Chunking Strategy

Messages are grouped into "conversation chunks" based on:

1. **Same session** — Messages from the same conversation
2. **Time proximity** — Within `maxGroupGapSeconds` of each other
3. **Size limit** — At most `maxMessagesPerChunk` messages

This creates semantically coherent units that are:
- Small enough for accurate embeddings
- Large enough for context (not single messages)
- Temporally grouped (related discussion stays together)

### Hybrid Search

When searching, GoClaw combines two approaches:

1. **Vector Search** (70% weight by default)
   - Query embedded via same model
   - Cosine similarity against all chunks
   - Finds semantically similar content

2. **Keyword Search** (30% weight by default)
   - BM25 full-text search
   - Catches exact matches vector might miss
   - Handles names, IDs, specific terms

Final score: `vector * 0.7 + keyword * 0.3`

---

## Agent Tools

### `transcript_search`

Search conversation history:

```json
{
  "tool": "transcript_search",
  "input": {
    "query": "authentication system design decisions"
  }
}
```

Returns:
```json
{
  "results": [
    {
      "content": "User: What auth approach should we use?\n\nGoClaw: Based on your requirements...",
      "score": 0.82,
      "timestamp": "2024-01-15T14:30:00Z",
      "messageCount": 4
    }
  ],
  "totalResults": 3,
  "searchTime": "45ms"
}
```

### `transcript_stats`

Get indexing statistics:

```json
{
  "tool": "transcript_stats",
  "input": {}
}
```

Returns:
```json
{
  "totalChunks": 495,
  "chunksWithEmbeddings": 495,
  "chunksNeedingEmbeddings": 0,
  "pendingMessages": 0,
  "chunksIndexedSession": 12,
  "lastSync": "2024-01-15T15:00:00Z",
  "provider": "text-embedding-nomic-embed-text-v1.5"
}
```

**Field explanations:**
- `totalChunks` — Total conversation chunks in database
- `chunksWithEmbeddings` — Chunks that have been embedded
- `chunksNeedingEmbeddings` — Backlog waiting for embeddings
- `pendingMessages` — New messages not yet chunked
- `provider` — Current embedding model (`"none"` if unavailable)

---

## OpenClaw Integration

### Initial Import

On startup, GoClaw imports your OpenClaw conversation history:

```
session: imported OpenClaw messages to SQLite for transcript indexing imported=123
```

These messages are stored with `source='openclaw'` and will be indexed like any other messages.

### Real-time Sync

While running side-by-side with OpenClaw, new messages in your OpenClaw session are:
1. Detected via file watcher
2. Stored in SQLite
3. Indexed on next sync cycle

```
session: stored new OpenClaw messages for transcript indexing count=2
```

This means conversations in OpenClaw become searchable in GoClaw within ~30 seconds.

---

## Use Cases

### Recovering Context After Compaction

```
Agent: "I don't have the earlier context, but let me search..."
→ transcript_search("database migration approach")
→ "Found: We decided to use incremental migrations with checksums..."
```

### Finding Past Decisions

```
User: "What did we decide about the caching strategy?"
Agent: [searches transcripts]
→ "On January 10th, we decided to use Redis with a 5-minute TTL..."
```

### Recalling Specific Discussions

```
User: "Remember when we talked about that weird bug with timezones?"
Agent: [searches "timezone bug"]
→ "Yes, on December 5th we debugged an issue where..."
```

### Cross-Session Context

Unlike in-context memory, transcript search works across:
- Multiple sessions
- Before/after compaction
- OpenClaw and GoClaw conversations

---

## Troubleshooting

### "provider: none" in stats

The embedding provider isn't initialized. Check:

1. Embedding model configured in `llm.embeddings.models`
2. Provider (LM Studio/Ollama) is running
3. Model is loaded/available

Look for:
```
openai: embedding ready name=lmstudio dimensions=768
memory: provider upgraded from=none to=lmstudio
```

### Chunks not getting embeddings

Check `chunksNeedingEmbeddings` in stats. If high:
- Increase `backfillBatchSize` for faster catchup
- Verify embedding provider is working
- Check logs for embedding errors

### Search returns no results

1. Verify chunks exist: check `totalChunks` in stats
2. Lower `minScore` threshold (try `0.2`)
3. Check query isn't too vague
4. Ensure `chunksWithEmbeddings > 0`

### Slow indexing

Embedding generation can be slow. Consider:
- Using GPU-accelerated inference
- Increasing `indexIntervalSeconds` for less frequent batches
- Using a faster/smaller embedding model

---

## Performance

### Storage

- ~1KB per chunk (768-dim float32 embedding + content + metadata)
- 500 chunks ≈ 500KB
- SQLite handles millions of rows efficiently

### Search Speed

- Typical search: 20-100ms
- Scales with chunk count
- Vector index provides O(log n) lookup

### Memory

- Embeddings stored in SQLite, not memory
- Indexer runs in background goroutine
- Minimal runtime overhead

---

## See Also

- [Embeddings](embeddings.md) — Embedding configuration
- [Agent Memory](agent-memory.md) — Memory system overview
- [Memory Search](memory-search.md) — Search workspace memory files
- [Session Management](session-management.md) — Compaction and checkpoints
- [Configuration](configuration.md) — Full config reference
