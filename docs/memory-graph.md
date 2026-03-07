---
title: "Memory Graph"
description: "Semantic knowledge graph for persistent agent memory with entity extraction and relationships"
section: "Agent Memory"
weight: 15
---

# Memory Graph

The Memory Graph is a semantic knowledge graph that provides persistent, structured memory for the agent. It extracts entities, relationships, and facts from conversations and stores them in a queryable database.

## Memory vs Memory Graph

GoClaw has two complementary memory systems:

| Feature | File Memory | Memory Graph |
|---------|-------------|--------------|
| Storage | Markdown files | SQLite database |
| Structure | Free-form text | Entities & relationships |
| Search | Semantic search | Hybrid search + filters |
| Updates | Manual edits | Tool-based CRUD |
| Context | Loaded at session start | Dynamically injected |
| Ingestion | Automatic (file read) | Requires `goclaw graph ingest` |

**Use both systems together:**

- **File memory** — For notes, logs, and human-readable records. Files in your workspace (MEMORY.md, daily notes) are read directly by the agent.
- **Memory graph** — For structured facts and automatic recall. Requires explicit ingestion to process markdown files into the graph.

## Overview

The memory graph:

- **Extracts entities** — People, places, projects, preferences, facts
- **Tracks relationships** — How entities relate to each other
- **Maintains context** — Automatically injects relevant memories into conversations
- **Supports queries** — Agent can search and retrieve specific memories

## How It Works

1. **Live extraction** — As conversations happen, the system extracts notable information
2. **Batch ingestion** — Markdown files and transcripts are processed via CLI
3. **Entity resolution** — New information is merged with existing entities
4. **Embedding generation** — Memories are embedded for semantic search
5. **Context injection** — Relevant memories are automatically included in the system prompt

## CLI Commands

GoClaw provides CLI commands for managing the memory graph. These are essential for ingesting content and inspecting the graph state.

### goclaw graph ingest

Ingest content into the memory graph. This is **the only way** to process markdown files and transcripts into memories.

```bash
goclaw graph ingest [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--source` | `all` | Source to ingest: `markdown`, `transcript`, or `all` |
| `--user` | owner | Username to ingest for |
| `--max-age` | `0` | Maximum age in days for transcripts (0 = no limit) |

**Examples:**

```bash
# Ingest everything (markdown + transcripts)
goclaw graph ingest

# Ingest only markdown files
goclaw graph ingest --source markdown

# Ingest transcripts from the last 30 days
goclaw graph ingest --source transcript --max-age 30

# Ingest for a specific user
goclaw graph ingest --user alex
```

**What gets ingested:**

- **Markdown** — Files in your workspace matching include patterns
- **Transcripts** — Conversation history stored in the sessions database

The ingestion process:

1. Scans sources for new or changed content (content hashing)
2. Skips items already ingested with matching hash
3. Extracts memories using the summarization LLM
4. Stores memories with entity relationships

**Ingestion is incremental** — Running `ingest` multiple times only processes new or changed content.

### goclaw graph stats

Show memory graph statistics:

```bash
goclaw graph stats
```

Output:

```
# Memory Graph Statistics

- Total Memories: 211
- Total Associations: 3
- With Embeddings: 0

## By Type
- decision: 20
- fact: 13
- observation: 19
- preference: 22
- event: 81
- goal: 10
- identity: 9
...

## Ingestion
- markdown_sources: 45
- markdown_memories: 28
- transcript_sources: 6971
- transcript_memories: 142
- live_sources: 497
- live_memories: 50
```

The "Ingestion" section shows how many source items have been processed and how many memories were extracted from each source type.

### goclaw graph search

Search the memory graph from the command line:

```bash
goclaw graph search <query> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--user` | owner | Username to search for |
| `--limit` | `10` | Maximum results |

**Examples:**

```bash
# Search for coffee preferences
goclaw graph search "coffee preferences"

# Search with more results
goclaw graph search "project deadlines" --limit 20
```

### goclaw graph bulletin

Generate memory bulletins — the context summaries injected into agent system prompts.

```bash
goclaw graph bulletin <type> [flags]
```

| Argument | Description |
|----------|-------------|
| `memory` | Identity, goals, preferences, recent events and decisions |
| `context` | Pending todos and time-sensitive items |

| Flag | Default | Description |
|------|---------|-------------|
| `--user` | owner | Username to generate for |

**Memory bulletin example:**

```bash
goclaw graph bulletin memory
```

```
## Identity
- User's name is Alex
- User's handle is alexdev

## Active Goals
- Research AI agent memory designs
- Improve tool performance for long-running workflows

## Preferences
- Prefers Go programming language over Python
- Active on Twitter with interests in AI agents and tech policy

## Recent Events
- Memory extraction feature went live on 2026-03-06
- Arrived home at 16:44 based on HomeAssistant event

## Recent Decisions
- Include timestamps in message metadata rather than system prompt
- Output facts directly rather than LLM-synthesized summaries
```

**Context bulletin example:**

```bash
goclaw graph bulletin context
```

```
# Context Bulletin for alexdev
Generated: 2026-03-07T21:44:39+02:00

## Pending Todos
- Buy supplies from store tomorrow (March 7th)
- Add uninstall functionality to skills tool
```

## Configuration

### Basic Configuration

```json
{
  "memoryGraph": {
    "enabled": true,
    "dbPath": "~/.goclaw/memory-graph.db"
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Enable memory graph |
| `dbPath` | string | `~/.goclaw/memory-graph.db` | SQLite database path |

### Ingestion Configuration

Control what files are ingested:

```json
{
  "memoryGraph": {
    "ingestion": {
      "includePatterns": ["*.md", "memory/*.md", "albums/*.md"],
      "excludePatterns": ["skills/**", "ref/**", "goclaw/**", ".*/**"],
      "transcriptBatchSize": 25
    }
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `includePatterns` | array | `["*.md", "memory/*.md", "albums/*.md"]` | Files to include (relative to workspace) |
| `excludePatterns` | array | `["skills/**", "ref/**", "goclaw/**", ".*/**"]` | Files to exclude (takes priority) |
| `transcriptBatchSize` | int | `25` | Chunks per LLM call for transcript ingestion |

Patterns use glob syntax relative to the workspace directory.

### Live Extraction Configuration

Control automatic extraction from conversations:

```json
{
  "memoryGraph": {
    "liveExtraction": {
      "enabled": true,
      "agentExtraction": true,
      "intervalSeconds": 120,
      "minMessages": 5,
      "maxTurns": 10,
      "batchSize": 50,
      "excludeSources": ["heartbeat", "cron", "delivered"]
    }
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Enable live extraction |
| `agentExtraction` | bool | `true` | Allow agent to store memories via tool |
| `intervalSeconds` | int | `120` | Background check interval |
| `minMessages` | int | `5` | Minimum messages before extraction |
| `maxTurns` | int | `10` | Max extraction loop turns |
| `batchSize` | int | `50` | Max messages per batch |
| `excludeSources` | array | `["heartbeat", "cron", "delivered"]` | Sources to exclude |

### Bulletin Configuration

Control context bulletin injection:

```json
{
  "memoryGraph": {
    "bulletin": {
      "enabled": true,
      "maxIdentity": 5,
      "maxGoals": 5,
      "maxPreferences": 5,
      "maxEvents": 5,
      "maxDecisions": 5
    }
  }
}
```

## Agent Tools

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

When bulletin injection is enabled, the memory graph automatically generates a context bulletin that's injected into the system prompt. This includes:

- Recently accessed memories
- Memories relevant to the current conversation
- Important facts about the user

The bulletin is regenerated periodically and when context changes significantly.

## Entity Types

Common entity types used in the memory graph:

| Type | Description | Examples |
|------|-------------|----------|
| `identity` | User identity information | name, handle, role |
| `preference` | User preferences | "likes dark mode", "prefers tea" |
| `fact` | Factual information | "works at Acme Corp" |
| `event` | Past or future events | "vacation in June" |
| `decision` | Decisions made | "chose React over Vue" |
| `goal` | Active goals | "learn Rust this year" |
| `routine` | Regular patterns | "morning coffee at 8am" |
| `observation` | Observed behaviors | "tends to work late" |
| `todo` | Pending tasks | "buy groceries" |
| `anomaly` | Unusual occurrences | "power outage on March 1" |

## Automatic Extraction

When live extraction is enabled, the system monitors conversations and extracts:

- Stated preferences ("I prefer...", "I like...")
- Personal facts ("I work at...", "My birthday is...")
- Relationships ("My wife Sarah...", "My colleague John...")
- Events and plans ("Next week I'm...", "Last month we...")

Extraction runs in the background and doesn't interrupt conversations.

## Troubleshooting

### Memories not being recalled

1. Check that bulletin injection is enabled
2. Verify the memory was stored with correct entities
3. Try a direct `recall` query to test retrieval

### Extraction not working

1. Verify live extraction is enabled
2. Check logs for extraction errors
3. Ensure embedding provider is configured

### Markdown files not in graph

Markdown files require explicit ingestion:

```bash
goclaw graph ingest --source markdown
```

Check that your files match the include patterns and aren't excluded.

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
