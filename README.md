---
title: "About GoClaw"
description: "GoClaw is a Golang AI agent gateway with transcript search, multi-channel support, and persistent memory"
section: "About"
weight: 1
landing: true
---

# GoClaw

GoClaw is a Golang implementation of a certain molty bot, compatible with OpenClaw session formats and "soul-ness".

Originally intended as a "minimum viable" replacement for OpenClaw, it has molted to reasonable feature parity with OpenClaw. It's not a complete replacement for OpenClaw but it's very driveable.

GoClaw has a superpower called **transcript search** — a persistent, searchable conversation history that survives context compaction. Your bot is able to recall detailed chat messages from its birth. Long live the memories!

Telegram, HTTP (web), and TUI interfaces are the primary methods for interaction at the moment.

GoClaw can run side-by-side with OpenClaw in the same workspace directory. The two "consciousness" streams are merged at startup to create one unified timeline, and GoClaw monitors your OpenClaw session to sync any new interactions in real-time. Two brains, one identity. It can also run completely standalone if you prefer.

A SQLite database with vector extensions manages session storage, semantic memory search, and session transcripts.

GoClaw is a bit opinionated about security, considering the brave new era we're entering. Tool sandboxing and exec bubblewrap if available. The managed Chromium install can also be bubblewrapped (tested on Ubuntu). Many other guardrails also exist. Of course you can disable this if you want your bot to have unfettered, dangerous access. Nothing is ever entirely secure, but one can try.

*This AI agent was written by an AI agent, under human guidance*

---

## Quick Start

### Install

```bash
curl -fsSL https://goclaw.org/install.sh | sh
```

### Interactive Setup (Recommended)

```bash
goclaw setup
```

The setup wizard will:

1. **Detect OpenClaw** — If found, offer to import settings (API keys, workspace, Telegram token)
2. **Create workspace** — Set up your agent's home directory with default files
3. **Configure providers** — Select and test LLM providers (Anthropic, Ollama, LM Studio, etc.)
4. **Set up user** — Create your owner account with optional Telegram ID
5. **Test connections** — Validate API keys and fetch available models
6. **Optionally launch browser** — Set up authenticated browser profiles

After setup, start GoClaw:

```bash
goclaw tui           # Interactive TUI mode (recommended)
goclaw gateway       # Foreground mode (logs to terminal)
goclaw start         # Daemon mode (background)
```

### Manual Setup

For manual tweaks, use `goclaw setup edit` to access the menu-based editor. For full manual configuration, create `~/.goclaw/goclaw.json` and `~/.goclaw/users.json` by hand — see [Configuration Reference](docs/configuration.md) for the schema.

### Re-configure

```bash
goclaw setup              # Auto-detect: edit if config exists, wizard if new
goclaw setup wizard       # Force full wizard (re-walk all steps)
goclaw setup edit         # Edit existing config (menu-based)
goclaw config             # View current configuration
goclaw config path        # Show config file location
```

### Other Installation Methods

See [Installation Guide](docs/installation.md) for:
- **Debian/Ubuntu** — `.deb` packages with bundled dependencies
- **Docker** — Container images on `ghcr.io`
- **Windows** — WSL2 installer script
- **Build from source** — For development or custom builds

---

## Superpowers

### Transcript Search — Your Agent Never Forgets

Every conversation is indexed into a local, searchable database with semantic embeddings. Transcripts survive context compaction — nothing is ever truly lost. The agent can recover context, find previous decisions, or recall discussions from weeks ago.

```
Agent: "What did we decide about the authentication system?"
→ Searches 500+ conversation chunks
→ Finds relevant discussion from 2 weeks ago
→ "We decided to use JWT tokens with refresh rotation..."
```

See [Transcript Search](docs/transcript-search.md) for full documentation.

### Memory Search — Workspace Knowledge

Search your memory files (`memory/*.md`, `MEMORY.md`) with hybrid semantic + keyword search. The agent finds relevant notes, decisions, and context from your written records.

See [Memory Search](docs/memory-search.md) for details.

### Memory Graph — Structured Knowledge

A semantic knowledge graph that automatically extracts entities, facts, and relationships from conversations. Unlike file-based memory, Memory Graph provides structured, queryable memory with tools like `recall`, `store`, and `query`.

See [Memory Graph](docs/memory-graph.md) for details.

### Managed Browser — First-Class Web Access

GoClaw includes a managed Chromium browser with auto-download/update:

- **`web_fetch`** — Automatic browser fallback for JavaScript-rendered pages
- **`browser` tool** — Full automation: navigate, click, type, screenshot
- **Persistent Profiles** — Authenticated sessions survive restarts
- **Domain Mapping** — Route sites to specific profiles (e.g., `*.twitter.com` → `twitter`)

See [Browser Tool](docs/tools/browser.md) for full documentation.

---

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

See [Session Management](docs/session-management.md) for details.

### Text LLM Providers

| Provider | Driver | Use Cases |
|----------|--------|-----------|
| **Anthropic** | `anthropic` | Agent responses (Claude Opus, Sonnet, Haiku) |
| **xAI** | `xai` | Low-latency, Grok models, server-side tools (gRPC) |
| **Ollama** | `ollama` | Local inference, embeddings, summarization |
| **OpenAI** | `openai` | GPT models, OpenAI-compatible APIs |
| **OpenAI Next** | `oai-next` | WebSocket streaming, reduced latency |

The `oai-next` driver uses OpenAI's WebSocket API for lower-latency streaming compared to the standard HTTP driver.

### Speech-to-Text (STT)

STT transcribes voice notes from Telegram and WhatsApp into text for the agent:

| Provider | Driver | Notes |
|----------|--------|-------|
| **Whisper.cpp** | `whispercpp` | Local, bundled model in `.deb`/Docker |
| **OpenAI Whisper** | `openai` | Cloud API |
| **Groq** | `groq` | Fast cloud inference |
| **Google** | `google` | Google Speech-to-Text API |

The Debian package and Docker images include a bundled Whisper model (`ggml-tiny.en.bin`) for zero-config local transcription.

### Realtime Voice LLM Providers

GoClaw has a separate **VoiceLLM Registry** for real-time voice conversations:

| Provider | Driver | Use Cases |
|----------|--------|-----------|
| **xAI Voice** | `xai` | Grok-based real-time voice |

Voice providers maintain per-session WebSocket connections and handle bidirectional audio streaming. They power the HTTP Voice channel for spoken conversations.

See [Channels](docs/channels.md#http-voice-channel) for voice setup.

### LLM Routing & Purpose Chains

GoClaw routes LLM requests based on **purpose**. Each purpose has a model chain with automatic failover:

| Purpose | Used For | Typical Provider |
|---------|----------|------------------|
| `agent` | Main conversation, tool use | Anthropic Claude |
| `summarization` | Checkpoints, compaction | Ollama / Haiku |
| `embeddings` | Memory, transcript, Memory Graph search | Ollama |

Additional purposes (`heartbeat`, `cron`, `hass`, `memoryExtraction`) fall back to `agent` if not configured.

If a provider fails, GoClaw automatically tries the next model in the chain. See [LLM Providers](docs/llm-providers.md#purpose-chains) for configuration details.

### Metrics & Cost Tracking

GoClaw tracks LLM costs automatically using embedded `models.json` pricing data:

- **Per-turn costs** — Input, output, and cache token costs calculated per request
- **Accumulated totals** — Running cost totals across sessions
- **Persistence** — Metrics survive restarts (stored in SQLite)
- **Web UI** — Interactive tree view at `/metrics`
- **JSON API** — Programmatic access via `/api/metrics`

See [Metrics](docs/metrics.md) for details.

### Session Storage

Sessions are stored in SQLite (`~/.goclaw/sessions.db`) with full message history. Even after compaction truncates in-memory messages, the full history remains in the database for:

- Audit trails
- Embeddings/summarization rebuilds
- Memory Graph extraction
- Future analysis

---

## OpenClaw Compatibility

GoClaw can run alongside OpenClaw in the same workspace. On first run, the setup wizard detects OpenClaw and offers to import:

- Workspace path and identity files
- Telegram bot token
- Anthropic API key
- Browser settings

Session transcripts from both systems are merged into a unified searchable history. GoClaw monitors OpenClaw sessions in real-time, so conversations from either system stay in sync.

After setup, `goclaw.json` is the authoritative config. See [Configuration](docs/configuration.md) for details.

---

## Documentation

Full documentation available at [goclaw.org/docs](https://goclaw.org/docs/) or in the [docs/](docs/) folder:

### Getting Started

- [Installation](docs/installation.md) — Download, build, and install
- [Configuration](docs/configuration.md) — Config file reference
- [First Run](docs/first-run.md) — Starting the gateway

### Core Concepts

- [Concepts Overview](docs/concepts.md) — Key concepts explained
- [Architecture](docs/architecture.md) — System components
- [Session Management](docs/session-management.md) — Context and compaction

### LLM Providers

- [Provider Overview](docs/llm-providers.md) — Multi-provider setup
- [Anthropic](docs/providers/anthropic.md) — Claude models
- [OpenAI Compatible](docs/providers/openai.md) — OpenAI, LM Studio, OpenRouter
- [Ollama](docs/providers/ollama.md) — Local models
- [xAI](docs/providers/xai.md) — Grok models

### Channels

- [Channels Overview](docs/channels.md) — Communication interfaces
- [Telegram](docs/telegram.md) — Bot setup
- [Web UI](docs/web-ui.md) — HTTP interface
- [Commands](docs/commands.md) — Slash commands

### Tools

- [Tools Overview](docs/tools.md) — Available tools
- [Browser](docs/tools/browser.md) — Web automation
- [Home Assistant](docs/tools/hass.md) — Smart home
- [Cron](docs/tools/cron.md) — Scheduling

### Agent Memory

- [Memory Overview](docs/agent-memory.md) — Memory architecture
- [Memory Search](docs/memory-search.md) — Workspace files
- [Transcript Search](docs/transcript-search.md) — Conversation history
- [Embeddings](docs/embeddings.md) — Vector search

### Advanced

- [Advanced Topics](docs/advanced.md) — Deep dives
- [Roles & Access](docs/roles.md) — RBAC and auth
- [Skills](docs/skills.md) — Extensibility
- [Sandbox](docs/sandbox.md) — Security
- [Deployment](docs/deployment.md) — Production setup
- [Troubleshooting](docs/troubleshooting.md) — Common issues

---

## Related Projects

- [OpenClaw](https://github.com/openclaw/openclaw) — The original Molt/Clawdbot
