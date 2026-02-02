# GoClaw Documentation

GoClaw is a Go implementation of an AI agent gateway, compatible with OpenClaw session formats, and "soul-ness".

It reimplements many of OpenClaw's concepts and functionality in go, and can actually run side-by-side with OpenClaw, in the same workspace directory. 

It will read your openclaw.json file to overlay into it's own configuration file, so that it's easier to run side-by-side. It will also read your openclaw session history at startup so you can takeoff where you left in openclaw.
It will also monitor your openclaw session and add any interactions with openclaw to it's own context in realtime.

It was intended as a "mimimum viable" replacement for OpenClaw, focusing on Anthropic models for the main agent, and Ollama for context compaction and memory embeddings.

A SQLite database with vector extensions is used for session storage and semantic memory search.

It is intended to be feature-rich enough to use as a daily driver given the minimum viable design.

It was entirely written by Claude/Cursor.

## Documentation Index

### Core Concepts

- [Architecture Overview](./architecture.md) - System components and how they interact
- [Session Management](./session-management.md) - Compaction, checkpoints, and context window management
- [Configuration Reference](./configuration.md) - All configuration options explained

### Features

- [Telegram Integration](./telegram.md) - Bot setup and commands
- [Memory Search](./memory-search.md) - Semantic search over memory files
- [Tools](./tools.md) - Available agent tools

### Operations

- [Deployment](./deployment.md) - Running GoClaw in production
- [Troubleshooting](./troubleshooting.md) - Common issues and solutions

---

## Quick Start

1. Copy `goclaw.example.json` to `goclaw.json`
2. Add your Anthropic API key and Telegram bot token
3. Run `make run` or `make debug`
4. For a Text UI do `make tui`

## Key Concepts

### Context Window Management

GoClaw manages the LLM's context window automatically:

```
[0%]──────[25%]──────[50%]──────[75%]──────[95%]──────[100%]
           │          │          │           │
        Checkpoint Checkpoint Checkpoint  Compaction
        (optional) (optional) (optional)  (required)
```

- **Checkpoints** (optional): Rolling snapshots of conversation state, generated via LLM
- **Compaction** (required): Truncates old messages when context is nearly full

See [Session Management](./session-management.md) for details.

### LLM Tiering

GoClaw supports using different LLMs for different tasks:

| Task | Default | Purpose |
|------|---------|---------|
| Agent responses | Anthropic Claude | Main intelligence |
| Checkpoints | Ollama (local) | Cheap rolling summaries |
| Compaction | Ollama (local) | Cheap compaction summaries |
| Embeddings | Ollama (local) | Memory search vectors |

If Ollama fails, compaction falls back to the main Anthropic model automatically.

### Session Storage

Sessions are stored in SQLite (`~/.openclaw/sessions.db`) with full message history. Even after compaction truncates in-memory messages, the full history remains in the database for:

- Audit trails
- Summary retry after failures
- Future analysis

---

## Related Projects

- [OpenClaw]. The original Molt/Clawdbot
