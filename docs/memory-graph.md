---
title: "Memory Graph"
description: "Semantic knowledge graph for persistent agent memory with entity extraction and relationships"
section: "Agent Memory"
weight: 15
---

# Memory Graph

The Memory Graph is a semantic knowledge graph that provides persistent, structured memory for the agent. Unlike file-based memory (daily notes, MEMORY.md), the memory graph extracts entities, relationships, and facts from conversations and stores them in a queryable database.

## Overview

The memory graph:

- **Extracts entities** — People, places, projects, preferences, facts
- **Tracks relationships** — How entities relate to each other
- **Maintains context** — Automatically injects relevant memories into conversations
- **Supports queries** — Agent can search and retrieve specific memories

## How It Works

1. **Live extraction** — As conversations happen, the system extracts notable information
2. **Entity resolution** — New information is merged with existing entities
3. **Embedding generation** — Memories are embedded for semantic search
4. **Context injection** — Relevant memories are automatically included in the system prompt

## Configuration

```json
{
  "memoryGraph": {
    "enabled": true,
    "dbPath": "~/.goclaw/memory-graph.db",
    "autoExtract": true,
    "bulletinEnabled": true,
    "bulletinMaxItems": 10
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Enable memory graph |
| `dbPath` | string | `~/.goclaw/memory-graph.db` | SQLite database path |
| `autoExtract` | bool | `true` | Auto-extract from conversations |
| `bulletinEnabled` | bool | `true` | Inject context into system prompt |
| `bulletinMaxItems` | int | `10` | Max items in context bulletin |

## Memory Graph Tools

The agent has access to six tools for managing the memory graph:

### memory_graph_recall

Retrieve relevant memories for the current context. This is the primary tool for accessing memories.

```json
{
  "tool": "memory_graph_recall",
  "input": {
    "query": "user's coffee preferences",
    "limit": 5
  }
}
```

Returns memories ranked by relevance using hybrid search (semantic + keyword).

### memory_graph_query

Search and filter memories with more control than recall.

```json
{
  "tool": "memory_graph_query", 
  "input": {
    "filter": "type:preference",
    "query": "food",
    "limit": 10
  }
}
```

Supports filtering by entity type, date ranges, and other metadata.

### memory_graph_store

Add a new memory to the graph.

```json
{
  "tool": "memory_graph_store",
  "input": {
    "content": "User prefers dark roast coffee",
    "memory_type": "preference",
    "entities": ["user", "coffee"],
    "confidence": 0.9
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `content` | string | The memory content |
| `memory_type` | string | Entity type (preference, fact, event, etc.) |
| `entities` | array | Related entity names |
| `confidence` | float | Confidence score (0-1) |

### memory_graph_update

Modify an existing memory.

```json
{
  "tool": "memory_graph_update",
  "input": {
    "id": "mem_abc123",
    "content": "User now prefers medium roast coffee",
    "confidence": 0.95
  }
}
```

### memory_graph_forget

Remove a memory from the graph.

```json
{
  "tool": "memory_graph_forget",
  "input": {
    "id": "mem_abc123",
    "reason": "User corrected this information"
  }
}
```

The `reason` field is optional but helps with audit trails.

### memory_graph_skip

Explicitly document why information is not being stored. Used during memory extraction to log skip decisions.

```json
{
  "tool": "memory_graph_skip",
  "input": {
    "content": "User mentioned the weather",
    "reason": "transient, not about user"
  }
}
```

This tool is primarily used by the automatic extraction process to provide visibility into what was considered but not stored. Useful for debugging extraction behavior.

## Context Bulletin

When `bulletinEnabled` is true, the memory graph automatically generates a context bulletin that's injected into the system prompt. This includes:

- Recently accessed memories
- Memories relevant to the current conversation
- Important facts about the user

The bulletin is regenerated periodically and when context changes significantly.

## Entity Types

Common entity types used in the memory graph:

| Type | Description | Examples |
|------|-------------|----------|
| `person` | People mentioned | "John", "my sister" |
| `preference` | User preferences | "likes dark mode", "prefers tea" |
| `fact` | Factual information | "works at Acme Corp" |
| `event` | Past or future events | "vacation in June" |
| `project` | Projects or work items | "website redesign" |
| `location` | Places | "home office", "San Francisco" |

## Automatic Extraction

When `autoExtract` is enabled, the system monitors conversations and extracts:

- Stated preferences ("I prefer...", "I like...")
- Personal facts ("I work at...", "My birthday is...")
- Relationships ("My wife Sarah...", "My colleague John...")
- Events and plans ("Next week I'm...", "Last month we...")

Extraction runs in the background and doesn't interrupt conversations.

## Memory vs Memory Graph

GoClaw has two memory systems:

| Feature | File Memory | Memory Graph |
|---------|-------------|--------------|
| Storage | Markdown files | SQLite database |
| Structure | Free-form text | Entities & relationships |
| Search | Semantic search | Hybrid search + filters |
| Updates | Manual edits | Tool-based CRUD |
| Context | Loaded at session start | Dynamically injected |

Both systems complement each other:
- **File memory** — For notes, logs, and human-readable records
- **Memory graph** — For structured facts and automatic recall

## Troubleshooting

### Memories not being recalled

1. Check that `bulletinEnabled` is true
2. Verify the memory was stored with correct entities
3. Try a direct `recall` query to test retrieval

### Extraction not working

1. Verify `autoExtract` is enabled
2. Check logs for extraction errors
3. Ensure embedding provider is configured

### Database issues

The memory graph uses SQLite. If corruption occurs:

```bash
# Backup current database
cp ~/.goclaw/memory-graph.db ~/.goclaw/memory-graph.db.bak

# Delete and let GoClaw recreate
rm ~/.goclaw/memory-graph.db
```

## See Also

- [Memory Search](memory-search.md) — File-based semantic search
- [Session Management](session-management.md) — Conversation persistence
- [Configuration](configuration.md) — Full config reference
