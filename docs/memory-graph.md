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

### Memory Trigger Configuration

Controls the poller that wakes the agent when a routine memory comes due. See [Memory Triggers](#memory-triggers) below for the architectural overview.

```json
{
  "memoryGraph": {
    "trigger": {
      "enabled": true,
      "pollIntervalSeconds": 60,
      "missedGraceMinutes": 30
    }
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Master switch for the memory-trigger poller. |
| `pollIntervalSeconds` | int | `60` | How often to scan for due routines. |
| `missedGraceMinutes` | int | `30` | If a routine's scheduled time is older than `now - missedGraceMinutes`, the fire is silently dropped and `next_trigger_at` is rolled forward. Prevents post-downtime floods. |

Bulletin injection for memtrigger turns is controlled separately by `memoryGraph.bulletin.injectForMemTrigger` (default `true`), and the LLM chain used for the turn is `llm.memtrigger` (falls back to `llm.agent` when unset).

## Memory Triggers

Routine memories with a structured [recurrence](#recurring-routines) can **spontaneously wake the agent** when their scheduled time arrives. Alongside user messages, heartbeat, and cron, memory triggers are a first-class turn source in GoClaw.

### Lifecycle

1. **Schedule.** When a routine is created or updated, its `next_trigger_at` is derived from the recurrence cadence (`days` + `time_start`, honouring `starts_on`/`ends_on`/`skip_dates`).
2. **Poll.** A background poller (`pollIntervalSeconds`, default `60s`) scans for routines whose `next_trigger_at <= now`.
3. **Grace check.** If the scheduled time is older than `missedGraceMinutes` (default `30m`), the fire is recorded as `missed_grace` and dropped — the poller rolls `next_trigger_at` forward to the next valid occurrence instead of flooding after downtime.
4. **Wake.** The poller opens a turn on the user's **primary session** (`primary` for the owner, or `user:<id>`) with `Purpose = "memtrigger"`. A short `[memtrigger]` preamble tells the agent a routine is due and to either respond or reply `SILENT_OK`.
5. **Bulletin injection.** Memory and context bulletins are injected into the system prompt (controlled by `bulletin.injectForMemTrigger`, default on), so the agent has the same situational awareness it would have during a user-initiated turn.
6. **LLM chain.** The turn routes through the `memtrigger` LLM purpose chain, which falls back to `agent` when unconfigured. Configure a distinct chain under `llm.memtrigger` if you want memtrigger turns to use a different model from normal conversation.
7. **Response fan-out.** Any agent output (not `SILENT_OK`) is fanned out to every active channel for the owning user — Telegram, WhatsApp, HTTP, TUI — so a nudge reaches wherever the user currently is.
8. **Outcome.** Each fire is recorded in an audit log as `fired` (agent produced output), `silent` (agent replied `SILENT_OK`), `missed_grace` (poller skipped as stale), or `error` (invocation failed).

### Safeguards

- **Grace window.** `missedGraceMinutes` prevents a backlog of old routines from firing at once after GoClaw restarts.
- **Loop-avoidance guard.** `memory_graph_store` calls that originate from a `memtrigger` turn are refused when the new routine's next occurrence is less than 5 minutes away, so a misbehaving agent can't create a routine that immediately re-wakes itself.
- **Autonomy hint.** Each routine carries an `autonomy` field (`observe` default, `suggest`, `confirm`, `auto`, `silent`) that the agent uses as context for whether to speak up.

### Related Surfaces

- **Today's Schedule bulletin** — due routines for today appear in a dedicated bulletin section, with `[silent]` / `[skipped]` / `[err]` annotations when recent fires weren't successful nudges.
- **`memory_graph_query` `mode: "triggers"`** — reads the fire audit log so the agent can answer "did I remind them?" or debug missed nudges. See [Inspecting fired triggers](#inspecting-fired-triggers).
- **`memory_graph_store` with `recurrence`** — the agent-facing entry point for creating a triggered routine. See [Recurring Routines](#recurring-routines).

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
    "mode": "typed",
    "memory_type": "todo",
    "happens_within": "7d",
    "sort_by": "scheduled",
    "max_results": 10
  }
}
```

Supports filtering by memory type, `occurred_at` date ranges, `happens_at` windows for scheduled items, and other metadata.

### memory_graph_store

Add a new memory to the graph.

```json
{
  "tool": "memory_graph_store",
  "input": {
    "content": "Project deadline for v1 launch",
    "memory_type": "todo",
    "happens_at": "2026-04-01",
    "reasoning": "User set a real deadline that should be queryable and appear in upcoming bulletins"
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `content` | string | The memory content |
| `memory_type` | string | Entity type (preference, fact, event, etc.) |
| `reasoning` | string | Why the memory is worth storing |
| `occurred_at` | string | When the memory was observed/recorded or when a past event happened |
| `happens_at` | string | When a scheduled event, plan, appointment, or deadline is set to happen |
| `confidence` | float | Confidence score (0-1) for pattern-style memories |

Use `occurred_at` for observation or past-event timing, and `happens_at` for structured scheduling of future events and deadlines. `next_trigger_at` remains internal to routine and prediction behavior.

#### Recurring Routines

For `memory_type: "routine"` you can pass a structured `recurrence` object instead of hoping the agent encodes the cadence in prose. The fields are normalized, the cron expression is derived from `days` + `time_start`, and the routine surfaces in the bulletin's new **Today's Schedule** section at the scheduled time.

```json
{
  "tool": "memory_graph_store",
  "input": {
    "content": "Evening lifting with Bob",
    "memory_type": "routine",
    "reasoning": "Recurring gym session with Bob that we want surfaced on the right days",
    "recurrence": {
      "days": ["tuesday", "thursday"],
      "time_start": "17:45",
      "time_end": "18:45",
      "location": "gym",
      "person": "Bob",
      "starts_on": "2025-10-01",
      "ends_on": "2026-06-30",
      "skip_dates": ["2025-12-24", "2025-12-25"]
    }
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `days` | array | Lowercase full day names (`"monday"`..`"sunday"`). Short forms (`"mon"`) and ISO numbers (`1`..`7`) also accepted. |
| `time_start` | string | Start time `"HH:MM"` (server-local). Required alongside `days` for the cron to be derived. |
| `time_end` | string | Optional end time `"HH:MM"`. Used for the `recurs_at_time` window filter and the rendered cadence line. |
| `duration_minutes` | int | Optional duration in minutes; alternative to `time_end`. |
| `location` | string | Optional location (e.g. `"office"`, `"Carrefour"`). |
| `person` | string | Optional person the routine involves. Queryable via `involves_person`. |
| `starts_on` | string | Inclusive `"YYYY-MM-DD"` — routine doesn't fire before this date. |
| `ends_on` | string | Inclusive `"YYYY-MM-DD"` — routine stops firing after this date; recall/query results render `(ended YYYY-MM-DD)` in place of `next:`. |
| `skip_dates` | array | List of `"YYYY-MM-DD"` dates to skip (holidays, travel). |
| `autonomy` | string | One of `observe` (default), `suggest`, `confirm`, `auto`, `silent` — how autonomously the agent should act when the routine fires. |

Once stored, the routine shows up in the bulletin's **Today's Schedule** section on matching days and wakes the agent at each occurrence via the memory-trigger poller. See [Memory Triggers](#memory-triggers) above for the wake-up lifecycle, grace window, LLM chain, and loop-avoidance guard.

The matching filters on `memory_graph_query` (`recurs_on_day`, `recurs_on_days`, `recurs_today`, `recurs_at_time: "17:00-19:00"`, `next_occurrence_within: "1h"`, `involves_person`) let the agent find routines by cadence without free-text search. `recurs_on_day` takes a single day; `recurs_on_days` takes an array for multi-day matches.

Query and recall results for routine memories also surface a compact `recurrence:` line alongside the content (e.g. `recurrence: Tue,Thu @ 17:45-18:45 @ gym | person: Bob | next: Tue 2026-04-21 17:45`). When a routine has passed its `ends_on`, the line shows `(ended YYYY-MM-DD)` instead of `next:`. At `detail_level: "full"` on `memory_graph_query` the line also includes `bounds:` and `skip:` entries.

#### Inspecting fired triggers

`memory_graph_query` with `mode: "triggers"` reads the trigger-fire audit log so the agent can answer "did I remind them?" or debug missed nudges. Filters: `memory_uuid`, `outcome` (one of `fired | silent | missed_grace | error`), `since`, `before`, `max_results`. In triggers mode the `since`/`before` filters apply to `fired_at` (not `occurred_at`); hybrid/recurrence filters are ignored.

```json
{
  "tool": "memory_graph_query",
  "input": {
    "mode": "triggers",
    "outcome": "silent",
    "since": "24h"
  }
}
```

Scope to a single routine by adding `"memory_uuid": "<uuid>"`. Each result shows `scheduled`, `fired` (with non-negative lag — the poller only acts when `next_trigger_at <= now`), `outcome`, and the originating `run_id`.

When the bulletin's **Today's Schedule** includes any line whose fire had a non-success outcome, the section header is annotated with a short legend (`[silent]` = agent stayed quiet via `SILENT_OK`, `[skipped]` = poller skipped the fire as stale past the `missedGraceMinutes` window, `[err]` = invocation error). Successful fires carry no annotation.

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
- Upcoming scheduled events and deadlines driven by `happens_at`

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

For dated `event`, `todo`, and `goal` memories, store machine-readable schedule timing in `happens_at` instead of relying only on prose in `content`.

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
3. Try a direct `memory_graph_recall` query to test retrieval

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
