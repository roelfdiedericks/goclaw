---
title: "Core Concepts"
description: "Key concepts that define how GoClaw operates: agent loop, sessions, tools, and channels"
section: "About"
weight: 2
---

# Core Concepts

GoClaw is built around several key concepts that define how the agent operates. Understanding these will help you configure and extend the system effectively.

## Agent Loop

At its heart, GoClaw runs an **agent loop** that orchestrates LLM interactions:

```
User Message → LLM → Tool Use? → Execute Tool → LLM → ... → Final Response
```

The loop continues until the LLM provides a final response without requesting tool use. This enables complex, multi-step reasoning where the agent can read files, execute commands, search memory, and more.

## Sessions

A **session** represents a conversation with persistent state:

- **Messages** — The conversation history (user, assistant, tool calls)
- **Context window** — How much history fits in the LLM's memory
- **Compaction** — Automatic truncation when context is nearly full
- **Checkpoints** — Rolling snapshots for recovery

Sessions are identified by a **session key** (for example, `primary` for owner sessions or `user:<id>` for non-owner sessions). Channels can also pass explicit session IDs in some flows.

See [Session Management](session-management.md) for details on compaction and checkpoints.

## Channels

**Channels** are communication interfaces between users and the agent:

| Channel | Description |
|---------|-------------|
| [Telegram](telegram.md) | Bot interface via Telegram |
| [WhatsApp](whatsapp.md) | Personal WhatsApp via linked device |
| [HTTP](web-ui.md) | Web interface and API |
| HTTP Voice | Real-time voice conversations |
| [TUI](tui.md) | Interactive terminal UI |
| [Cron](cron.md) | Scheduled task execution |

Text channels use the main **Gateway** and LLM Registry. The voice channel uses a separate **VoiceLLM Registry** with per-session WebSocket connections to real-time voice APIs.

See [Channels](channels.md) for the full overview.

## Tools

**Tools** extend the agent's capabilities beyond text generation:

| Category | Examples |
|----------|----------|
| File operations | read, write, edit |
| System | exec (shell commands), jq |
| Search | memory_search, memory_get, transcript, web_search |
| Memory Graph | memory_graph_recall/query/store/update/forget |
| Orchestration | cron, subagent_spawn/status/cancel, subagent_fanout |
| Integration | hass (Home Assistant), browser |
| Communication | message (send to channels) |
| Utility | media_display, skills, goclaw_update, user_auth |
| Media generation | xai_imagine, xai_video |

Tools are registered with the gateway and exposed to the LLM via function calling. Many tools are conditionally enabled by configuration and channel/runtime availability (for example browser/HASS/subagent features).

Recent architecture additions include delegated-run tools and fanout orchestration:

- `subagent_spawn` for asynchronous delegated runs
- `subagent_status` / `subagent_cancel` for operator control
- `subagent_fanout` for bounded parallel child execution with deterministic aggregation (+ optional synthesis)

See [Tools](tools.md) for the complete tool reference.

## Delegated Runs & Subagents

GoClaw uses a shared **delegated run** model for isolated background and nested agent execution:

- cron jobs can execute through delegated runs
- owner-only subagent tools can spawn delegated children
- fanout mode can launch multiple children with bounded parallelism and deterministic aggregation
- result routing and completion dispatch are policy-driven (`store_only`, `deliver`, `handoff_main`, `return_to_requester`)

This is the core model behind `/runners` visibility, delegated lifecycle events, and subagent orchestration behavior.

See [Delegated Runs](delegated-runs.md) for the full architecture and operational details.

## Skills

**Skills** are markdown files that provide domain-specific knowledge and instructions. They extend the agent's capabilities without code changes:

```
skills/
├── weather/
│   └── SKILL.md
├── discord/
│   └── SKILL.md
└── ...
```

Skills can declare requirements (binaries, environment variables) and are automatically filtered based on availability.

See [Skills](skills.md) for the skills system.

## LLM Providers

GoClaw supports multiple **LLM providers** through a unified registry:

| Provider | Use Cases |
|----------|-----------|
| Anthropic | Agent responses (Claude), extended thinking |
| OpenAI | GPT models, compatible APIs |
| Ollama | Local inference, embeddings, summarization |
| xAI | Grok models, stateful conversations |

The registry supports **purpose chains** — different providers for different tasks (agent, summarization, embeddings) with automatic fallback.

### VoiceLLM Registry

A separate **VoiceLLM Registry** handles real-time voice conversations:

| Provider | Description |
|----------|-------------|
| xAI Voice | Grok-based real-time voice |

Voice providers maintain per-session WebSocket connections and handle audio streaming directly.

See [LLM Providers](llm-providers.md) for configuration.

## Memory

GoClaw has three memory systems:

### Workspace Memory
Traditional markdown files that the agent can read and write:
- `MEMORY.md` — Long-term curated memories
- `memory/*.md` — Daily notes and logs

### Semantic Memory
Embeddings-based search over memory files and conversation transcripts:
- **memory_search** — Search memory files by meaning
- **transcript** — Search/query past conversations

### Memory Graph
A semantic knowledge graph for structured facts and relationships:
- **memory_graph_recall** — Retrieve relevant context automatically
- **memory_graph_store/update/forget** — Manage entities and facts
- **memory_graph_query** — Natural language questions over the graph

Memory Graph provides structured, queryable memory that persists across sessions. It's designed to eventually supersede file-based memory.

See [Agent Memory](agent-memory.md) for the memory architecture and [Memory Graph](memory-graph.md) for details.

## Roles & Access Control

Users have **roles** that determine their access level:

| Role | Description |
|------|-------------|
| `owner` | Full access to all tools and settings |
| `user` | Limited access based on permissions |

Users authenticate via **identities** (Telegram ID, API key, etc.) and can have tool-specific permissions.

See [Roles](roles.md) for access control configuration.

## Workspace

The **workspace** is the agent's home directory — where it operates and stores files:

- Identity files: `SOUL.md`, `AGENTS.md`, `USER.md`
- Memory files: `MEMORY.md`, `memory/`
- Skills: `skills/`
- Configuration: `goclaw.json`, `users.json`

File operations are sandboxed to the workspace by default.

---

## See Also

- [Architecture](architecture.md) — Technical system overview
- [Delegated Runs](delegated-runs.md) — Delegated execution architecture
- [Configuration](configuration.md) — All configuration options
- [Session Management](session-management.md) — Context and compaction
- [LLM Providers](llm-providers.md) — Provider setup
- [Memory Graph](memory-graph.md) — Semantic knowledge graph
