# Agent Memory

GoClaw implements a dual memory system that combines traditional file-based memory with semantic search capabilities.

## Memory Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Agent Memory System                       │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌─────────────────────┐    ┌─────────────────────┐        │
│  │  Workspace Memory   │    │   Semantic Memory    │        │
│  │  (File-Based)       │    │   (Embeddings)       │        │
│  │                     │    │                      │        │
│  │  MEMORY.md          │    │  memory_search       │        │
│  │  memory/*.md        │    │  transcript_search   │        │
│  │  USER.md            │    │                      │        │
│  │  SOUL.md            │    │  SQLite + Ollama     │        │
│  └─────────────────────┘    └─────────────────────┘        │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

## Workspace Memory

Traditional file-based memory that the agent can read and write directly.

### Memory Files

| File | Purpose |
|------|---------|
| `MEMORY.md` | Long-term curated memories, distilled insights |
| `memory/*.md` | Daily notes and raw logs (e.g., `memory/2026-02-16.md`) |
| `USER.md` | Information about the user |
| `SOUL.md` | Agent personality and identity |

### How It Works

1. Agent reads memory files at session start (via prompt cache)
2. Agent writes to memory files using `write` or `edit` tools
3. Memory persists across sessions
4. Agent decides what's worth remembering

### Best Practices

From `AGENTS.md`:
- Write important context to `memory/YYYY-MM-DD.md` as it happens
- Periodically review daily notes and update `MEMORY.md` with lasting insights
- Include source attribution: `(told me directly)`, `(from website.com)`, `(inferred)`

## Semantic Memory

Embeddings-based search over memory files and conversation transcripts.

### Components

| Component | Description |
|-----------|-------------|
| [memory_search](memory-search.md) | Search memory files by meaning |
| [transcript_search](transcript-search.md) | Search past conversations |
| [Embeddings](embeddings.md) | Shared embedding infrastructure |

### How It Works

1. Memory files and transcripts are chunked and embedded
2. Embeddings stored in SQLite alongside the content
3. Search queries are embedded and compared using cosine similarity
4. Hybrid scoring combines vector similarity + keyword matching

### Search Tools

**memory_search** — Search memory files:
```json
{
  "query": "what did we decide about the API design?",
  "maxResults": 5
}
```

**transcript_search** — Search conversation history:
```json
{
  "query": "when did we discuss deployment",
  "sessionKey": "main",
  "maxResults": 5
}
```

## Memory vs Transcripts

| Aspect | Memory Files | Transcripts |
|--------|--------------|-------------|
| Source | Agent-written markdown | Conversation history |
| Persistence | Until deleted | Compacted over time |
| Search | memory_search | transcript_search |
| Content | Curated, organized | Raw conversation |
| Use case | Long-term knowledge | Recent context recovery |

## Configuration

### Memory Search

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

### Transcript Search

Transcript search uses the same embedding infrastructure configured for memory search.

See [Embeddings](embeddings.md) for embedding model configuration.

## Compaction Recovery

When context is compacted during long sessions, recent conversation history is lost. The agent can recover using:

1. **transcript_search** — Find relevant past conversation chunks
2. **memory_search** — Check if context was saved to memory files
3. **Daily notes** — Read `memory/YYYY-MM-DD.md` for recent context

### Prevention

Configure memory flush prompts to remind the agent to save context before compaction:

```json
{
  "session": {
    "memoryFlush": {
      "enabled": true,
      "thresholds": [
        {"percent": 50, "prompt": "Consider noting key decisions."},
        {"percent": 75, "prompt": "Write important context now."}
      ]
    }
  }
}
```

## OpenClaw Compatibility

GoClaw's memory system is compatible with OpenClaw:

- Same file locations (`MEMORY.md`, `memory/`, etc.)
- Same semantic search tools
- Shared embedding database

This enables running GoClaw alongside OpenClaw with a unified memory system.

---

## See Also

- [Memory Search](memory-search.md) — memory_search tool
- [Transcript Search](transcript-search.md) — transcript_search tool
- [Embeddings](embeddings.md) — Embedding infrastructure
- [Session Management](session-management.md) — Compaction and memory flush
