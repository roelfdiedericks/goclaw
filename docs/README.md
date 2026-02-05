# GoClaw Documentation

GoClaw is a Golang implementation of a certain molty bot, compatible with OpenClaw session formats and "soul-ness".

Originally intended as a "minimum viable" replacement for OpenClaw, it has molted to reasonable feature parity with OpenClaw. It's not a complete replacement for OpenClaw but it's very driveable.

GoClaw has a superpower called **transcript search** a persistent, searchable conversation history that survives context compaction. Your bot is able to recall detailed chat messages from it's birth. Long live the memories!

Telegram, http (web), and TUI interfaces are the primary methods for interaction at the moment.

GoClaw can run side-by-side with OpenClaw in the same workspace directory. The two "consciousness" streams are merged at startup to create one unified timeline, and GoClaw monitors your OpenClaw session to sync any new interactions in real-time. Two brains, one identity. It can also run completely standalone if you prefer.

A SQLite database with vector extensions manages session storage, semantic memory search, and session transcripts.

Goclaw is a bit opinionated about security, considering the brave new era we're entering. Tool sandboxing and exec bubblewrap if available. The managed chromium install can also be bubblewrapped. Many other guardrails also exist. Of course you can disable this if you want your bot to have unfettered, dangerous access. Nothing is ever entirely secure, but one can try. 

### OpenClaw Compatibility

On first run, GoClaw bootstraps from your existing `openclaw.json` — extracting workspace, Telegram, browser settings, and Anthropic API key. Other providers (Ollama, LM Studio) need manual configuration. See [OpenClaw Bootstrap](#openclaw-bootstrap) for details.

### Supported LLM Providers

| Provider | Use Cases |
|----------|-----------|
| **Anthropic** | Agent responses (Claude Opus, Sonnet, Haiku) |
| **Ollama** | Local inference, embeddings, summarization |
| **OpenAI-compatible** | LM Studio, LocalAI, Kimi, OpenRouter, etc. |

Different providers can be assigned to different tasks (agent, summarization, embeddings) with automatic fallback chains.

*This AI agent was written by an AI agent, under human guidance*

---

## Superpowers

### Transcript Search — Your Agent Never Forgets

GoClaw indexes every conversation into a searchable database with semantic embeddings. GoClaw transcripts are:

- **Local & Private** — Your conversations stay on your machine
- **Persistent** — Survives context compaction; nothing is ever truly lost
- **Cross-Platform** — Merges OpenClaw + GoClaw history into one searchable index
- **Real-time** — New messages indexed within 30 seconds
- **Hybrid Search** — Combines semantic understanding with keyword matching

Your agent can search past conversations to recover context after compaction, find previous decisions, or recall what you discussed weeks ago.

```
Agent: "What did we decide about the authentication system?"
→ Searches 500+ conversation chunks
→ Finds relevant discussion from 2 weeks ago
→ "We decided to use JWT tokens with refresh rotation..."
```

See [Transcript Search](./transcript-search.md) for full documentation.

### Memory Search — Workspace Knowledge

Search your memory files (`memory/*.md`, `MEMORY.md`) with the same hybrid semantic + keyword search. The agent can find relevant notes, decisions, and context from your written records.

See [Memory Search](./memory-search.md) for details.

### Managed Browser — First-Class Web Access

GoClaw includes a managed Chromium browser as a first-class citizen, not an afterthought:

- **`web_fetch`** — Automatically uses the browser for JavaScript-rendered pages (SPAs, dynamic content). Falls back gracefully when browser isn't needed.
- **`browser` tool** — Full browser automation: navigate, click, type, screenshot, extract content. Headless or headed operation.
- **Persistent Profiles** — Maintain authenticated sessions across restarts. Log in once, stay logged in.
- **Domain Mapping** — Route specific sites to specific profiles (e.g., `*.twitter.com` → `twitter` profile).
- **Stealth Mode** — Configurable anti-detection for sites that block automation.

The browser auto-downloads and updates Chromium, so there's nothing to install manually.

See [Browser Tool](./browser_tool.md) for full documentation.

---

## Documentation Index

### Core Concepts

- [Architecture Overview](./architecture.md) - System components and how they interact
- [Session Management](./session-management.md) - Compaction, checkpoints, and context window management
- [Configuration Reference](./configuration.md) - All configuration options explained

### Features

- [Transcript Search](./transcript-search.md) - Searchable conversation history with embeddings
- [Memory Search](./memory-search.md) - Semantic search over memory files
- [Browser Tool](./browser_tool.md) - Managed browser for web automation
- [Sandboxing](./sandbox.md) - File, exec, and browser isolation
- [Telegram Integration](./telegram.md) - Bot setup and commands
- [Cron & Heartbeat](./cron.md) - Scheduled tasks and periodic checks
- [Skills](./skills.md) - Extensible agent capabilities
- [Tools](./tools.md) - Available agent tools

### Operations

- [Deployment](./deployment.md) - Running GoClaw in production
- [Troubleshooting](./troubleshooting.md) - Common issues and solutions

---

## Quick Start

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

For manual tweaks, use `goclaw setup edit` to access the menu-based editor. For full manual configuration, create `~/.goclaw/goclaw.json` and `~/.goclaw/users.json` by hand — see [Configuration Reference](./configuration.md) for the schema.

### Re-configure

```bash
goclaw setup              # Auto-detect: edit if config exists, wizard if new
goclaw setup wizard       # Force full wizard (re-walk all steps)
goclaw setup edit         # Edit existing config (menu-based)
goclaw config             # View current configuration
goclaw config path        # Show config file location
```

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

| Task | Typical Choice | Purpose |
|------|----------------|---------|
| Agent responses | Anthropic Claude | Main intelligence |
| Summarization | Ollama / Haiku | Checkpoints and compaction |
| Embeddings | LM Studio / Ollama | Memory and transcript search |

Each task can have a fallback chain — if the primary provider fails, GoClaw automatically tries the next in the list.

### Session Storage

Sessions are stored in SQLite (`~/.openclaw/sessions.db`) with full message history. Even after compaction truncates in-memory messages, the full history remains in the database for:

- Audit trails
- Summary retry after failures
- Future analysis

---

## OpenClaw Bootstrap

On first run (when `goclaw.json` doesn't exist or is empty), GoClaw extracts settings from your existing OpenClaw installation:

**From `openclaw.json`:**
| Setting | GoClaw Equivalent |
|---------|-------------------|
| `agents.defaults.workspace` | Working directory |
| `agents.defaults.model.primary` | Primary agent model |
| `channels.telegram.botToken` | Telegram bot token |
| `tools.web.search.apiKey` | Brave search API key |
| `browser.*` | Browser tool settings |

**From `~/.openclaw/agents/main/agent/auth-profiles.json`:**
| Setting | GoClaw Equivalent |
|---------|-------------------|
| `profiles["anthropic:default"].key` | Anthropic API key |

**Not extracted** (configure manually):
- Ollama URL and settings
- OpenAI/LM Studio API keys
- Embedding provider configuration

After bootstrap, `goclaw.json` is the authoritative config. The bootstrap is Anthropic-oriented — you'll need to manually add local providers for embeddings and summarization.

---

## Related Projects

- [OpenClaw]. The original Molt/Clawdbot
