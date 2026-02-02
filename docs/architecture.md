# Architecture Overview

GoClaw is a Go implementation of an AI agent gateway, designed to orchestrate LLM interactions with tool execution and multi-channel communication.

## High-Level Architecture

```
┌───────────────────────────────────────────────────┐
│                    Channels                        │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐        │
│  │ Telegram │  │   TUI    │  │   HTTP   │        │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘        │
│       │             │             │               │
└───────┼─────────────┼─────────────┼───────────────┘
        │             │             │
        └─────────────┼─────────────┘
                      │
                      ▼
              ┌───────────────────────────┐
              │         Gateway           │
              │  ┌─────────────────────┐  │
              │  │    Agent Loop       │  │
              │  │  ┌───────────────┐  │  │
              │  │  │ LLM Client    │  │  │
              │  │  └───────────────┘  │  │
              │  │  ┌───────────────┐  │  │
              │  │  │ Tool Registry │  │  │
              │  │  └───────────────┘  │  │
              │  └─────────────────────┘  │
              │                           │
              │  ┌─────────────────────┐  │
              │  │  Session Manager    │  │
              │  │  ┌───────────────┐  │  │
              │  │  │ Compaction    │  │  │
              │  │  │ Manager       │  │  │
              │  │  └───────────────┘  │  │
              │  │  ┌───────────────┐  │  │
              │  │  │ Checkpoint    │  │  │
              │  │  │ Generator     │  │  │
              │  │  └───────────────┘  │  │
              │  └─────────────────────┘  │
              └───────────────────────────┘
                          │
                          ▼
              ┌───────────────────────────┐
              │      Storage Layer        │
              │  ┌─────────┐ ┌─────────┐  │
              │  │ SQLite  │ │  JSONL  │  │
              │  │ (main)  │ │(legacy) │  │
              │  └─────────┘ └─────────┘  │
              └───────────────────────────┘
```

## Core Components

### Gateway (`internal/gateway`)

The central orchestrator that:
- Receives requests from channels
- Manages the agent loop (LLM ↔ Tools)
- Handles session lifecycle
- Coordinates compaction and checkpoints

```go
type Gateway struct {
    sessions            *session.Manager
    llm                 *llm.Client
    tools               *tools.Registry
    channels            map[string]Channel
    compactor           *session.CompactionManager
    checkpointGenerator *session.CheckpointGenerator
    promptCache         *gcontext.PromptCache
    // ...
}
```

### Session Manager (`internal/session`)

Manages conversation state:

| Component | Responsibility |
|-----------|---------------|
| `Manager` | Session lifecycle, storage coordination |
| `Session` | In-memory message buffer, token tracking |
| `CompactionManager` | Context overflow handling, fallback logic |
| `CheckpointGenerator` | Rolling snapshot generation |
| `SQLiteStore` | Persistent storage |

### LLM Clients (`internal/llm`)

| Client | Use Case |
|--------|----------|
| `Client` (Anthropic) | Main agent responses |
| `OllamaClient` | Checkpoints, compaction summaries, embeddings |

### Tool Registry (`internal/tools`)

Available agent tools:

| Tool | Description |
|------|-------------|
| `read` | Read file contents |
| `write` | Write file contents |
| `edit` | Edit file (string replace) |
| `exec` | Execute shell commands |
| `message` | Send messages to channels |
| `memory_search` | Semantic search over memory |

### Channels (`internal/telegram`, etc.)

Communication interfaces:

| Channel | Description |
|---------|-------------|
| Telegram | Bot interface via telebot.v4 |
| TUI | Terminal UI via bubbletea |
| HTTP | REST API (planned) |

### Command Handler (`internal/commands`)

Unified slash command handling across all channels:

| Command | Description |
|---------|-------------|
| `/status` | Session info + compaction health |
| `/compact` | Force context compaction |
| `/clear` | Reset session |
| `/help` | List commands |

The `CommandHandler` provides consistent output formatting for each channel (plain text for TUI, Markdown for Telegram).

---

## Request Flow

### User Message → Response

```
1. Channel receives message
   └─ Telegram: Update from bot API
   └─ TUI: User input

2. Channel calls Gateway.RunAgent(request)
   └─ AgentRequest{UserMsg, Source, ChatID, Images}

3. Gateway agent loop:
   ┌─────────────────────────────────────────┐
   │  a. Check compaction needed?            │
   │     └─ Yes: Run compaction              │
   │                                         │
   │  b. Build prompt (system + messages)    │
   │     └─ PromptCache provides workspace   │
   │                                         │
   │  c. Call LLM                            │
   │     └─ Stream response                  │
   │                                         │
   │  d. Tool use requested?                 │
   │     └─ Yes: Execute tool, loop back     │
   │     └─ No: Return final response        │
   │                                         │
   │  e. Check checkpoint trigger?           │
   │     └─ Yes: Generate async              │
   └─────────────────────────────────────────┘

4. Gateway streams events to channel
   └─ EventTextDelta, EventToolUse, EventComplete

5. Channel sends response to user
```

### Compaction Flow

```
1. ShouldCompact() returns true
   └─ totalTokens >= maxTokens - reserveTokens

2. CompactionManager.Compact()
   ├─ Try checkpoint fast-path
   │   └─ Recent checkpoint? Use its summary
   │
   ├─ Try Ollama
   │   └─ Success? Done
   │   └─ Failure? Increment count
   │
   ├─ Try Anthropic (fallback)
   │   └─ Success? Done
   │   └─ Failure? Continue
   │
   └─ Emergency truncation
       └─ Stub summary, keep 20%, mark for retry

3. Truncate in-memory messages

4. Write compaction record to SQLite

5. Background retry (if emergency)
   └─ Goroutine checks every 60s
   └─ Loads messages from SQLite
   └─ Retries LLM summary generation
```

---

## Data Flow

### Message Persistence

Every message is persisted to SQLite:

```
User sends message
    │
    ▼
gateway.RunAgent()
    │
    ├─► sess.AddUserMessage()
    │       │
    │       └─► g.persistMessage(role="user")
    │               │
    │               └─► store.AppendMessage()
    │
    ├─► LLM response
    │       │
    │       └─► sess.AddAssistantMessage()
    │               │
    │               └─► g.persistMessage(role="assistant")
    │
    └─► Tool execution
            │
            ├─► sess.AddToolUse()
            │       └─► g.persistMessage(role="tool_use")
            │
            └─► sess.AddToolResult()
                    └─► g.persistMessage(role="tool_result")
```

### Session State

```
┌─────────────────────────────────────────────────────────────┐
│                    In-Memory (Session)                       │
│  ┌─────────────────────────────────────────────────────┐    │
│  │ Messages[]  │ Recent messages only (after compaction) │   │
│  │ TotalTokens │ Estimated token count                   │   │
│  │ Checkpoint  │ Last checkpoint reference               │   │
│  └─────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────┘
                              │
                              │ Compaction truncates
                              │ in-memory only
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                    SQLite (Persistent)                       │
│  ┌─────────────────────────────────────────────────────┐    │
│  │ messages    │ ALL messages (full history)            │    │
│  │ checkpoints │ All checkpoint records                 │    │
│  │ compactions │ All compaction records                 │    │
│  └─────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────┘
```

---

## Package Structure

```
goclaw/
├── cmd/goclaw/          # Main entry point
│   └── main.go
│
├── internal/
│   ├── gateway/         # Central orchestrator
│   │   └── gateway.go
│   │
│   ├── session/         # Session management
│   │   ├── manager.go       # Session lifecycle
│   │   ├── session.go       # In-memory state
│   │   ├── compaction.go    # CompactionManager
│   │   ├── checkpoint.go    # CheckpointGenerator
│   │   ├── sqlite_store.go  # SQLite backend
│   │   └── jsonl_store.go   # JSONL backend (legacy)
│   │
│   ├── llm/             # LLM clients
│   │   ├── client.go        # Anthropic client
│   │   └── ollama.go        # Ollama client
│   │
│   ├── tools/           # Agent tools
│   │   ├── registry.go
│   │   ├── read.go
│   │   ├── write.go
│   │   ├── edit.go
│   │   ├── exec.go
│   │   └── message.go
│   │
│   ├── telegram/        # Telegram channel
│   │   └── bot.go
│   │
│   ├── tui/             # Terminal UI
│   │   └── tui.go
│   │
│   ├── context/         # Prompt construction
│   │   ├── prompt.go
│   │   └── cache.go
│   │
│   ├── config/          # Configuration
│   │   └── config.go
│   │
│   ├── sandbox/         # File security
│   │   └── sandbox.go
│   │
│   └── logging/         # Structured logging
│       └── logging.go
│
├── docs/                # Documentation
└── goclaw.json          # Configuration
```

---

## Concurrency Model

### Background Goroutines

| Goroutine | Purpose |
|-----------|---------|
| Compaction retry | Retries failed summary generation |
| Prompt cache poller | Detects workspace file changes |
| Media cleanup | Removes expired media files |
| Checkpoint generation | Async checkpoint creation |

### Synchronization

- `sync.Mutex` for shared state (session, compaction manager)
- `sync.atomic` for flags (inProgress)
- `context.Context` for cancellation
- Channels for shutdown coordination

---

## See Also

- [Session Management](./session-management.md) - Compaction and checkpoints
- [Configuration](./configuration.md) - All config options
- [Tools](./tools.md) - Available agent tools
