# Project: GoClaw

**Status:** Active Development
**Created:** 2026-02-01
**Owner:** RoDent

---

## Motivation

Node.js ecosystem is brittle. Dependencies break, npm is a mess, runtime overhead for something that should be a simple daemon. Go offers:
- Single binary deployment
- No runtime dependencies
- Rock-solid stability
- Cross-compilation for free
- Better fit for a long-running daemon

## MVP Scope

Deliberately minimal. Cover 90% of real usage:

### LLM Provider
- **Anthropic only** — Claude is the main model anyway
- Streaming responses
- Tool use / function calling
- Vision (image attachments)

### Channel
- **Telegram only** — good API, flexible, what we use
- Inline buttons
- Reactions (minimal mode)
- Media handling (images in/out)

### Core Tools

**File/Shell:**
- `read` — read file contents
- `write` — write files
- `edit` — surgical text replacement
- `exec` — run shell commands
- `process` — manage background processes (list, kill, send input)

**Web:**
- `web_search` — Brave Search API wrapper
- `web_fetch` — HTTP fetch + readability extraction

**Infrastructure:**
- `cron` — scheduled jobs (Go has great timer libs)
- `message` — send to Telegram
- `gateway` — self-management (restart, config)

**Memory:**
- `memory_search` — semantic search over markdown files
- `memory_get` — snippet retrieval

**Optional (later):**
- `image` — vision API passthrough
- `tts` — text-to-speech API call
- `browser` — headless browser control (complex, skip initially)

### Explicitly Out of Scope (MVP)
- WhatsApp (Meta API is painful)
- Discord, Slack, Signal
- Nodes (mobile companion)
- Canvas
- Multi-provider LLM support
- Sandboxing (most users run promiscuous anyway)

## Architecture Thoughts

```
┌─────────────────────────────────────────┐
│              Gateway (main)             │
├─────────────┬─────────────┬─────────────┤
│   Channel   │   LLM       │   Tools     │
│   Adapter   │   Client    │   Registry  │
│  (Telegram) │ (Anthropic) │             │
└─────────────┴─────────────┴─────────────┘
```

- **Single binary**, config via JSON/YAML file
- **Session state** in SQLite or flat files
- **Embedding search** — look at Go libs (e.g., `go-embeddings`, or call local ollama)
- **Streaming** — SSE from Anthropic → chunked Telegram edits

## Hard Parts

1. **Streaming responses** — need to buffer and edit Telegram messages as tokens arrive
2. **Context management** — track token usage, implement compaction when nearing limit
3. **Embedding/vector search** — either local lib or ollama sidecar
4. **Tool schema** — clean way to define tools with JSON schema for LLM

## Go Libraries

**In use:**
- `github.com/charmbracelet/log` — logging
- `github.com/alecthomas/kong` — CLI parsing
- `github.com/sevlyar/go-daemon` — daemonization

**To evaluate:**
- `github.com/charmbracelet/bubbletea` — TUI framework (Elm architecture)
- `github.com/charmbracelet/bubbles` — pre-built TUI components
- `github.com/charmbracelet/lipgloss` — terminal styling
- `github.com/go-telegram-bot-api/telegram-bot-api` — Telegram
- `github.com/mattn/go-sqlite3` — state persistence
- Anthropic client — write thin client or find existing
- Readability extraction — port or find Go equivalent
- Brave Search — just HTTP, no lib needed

## Compatibility Strategy

- **Config:** Read same `openclaw.json` format, ignore unsupported fields gracefully
- **Workspace:** Same directory structure, same `SKILL.md` / memory conventions
- **Side-by-side dev:** Run GoClaw in existing OpenClaw workspace, test incrementally
- **Graceful degradation:** Unknown config keys → warn and skip, don't crash

This lets you:
1. Copy existing config, tweak minimally
2. Run GoClaw against your real workspace
3. Flip between OpenClaw and GoClaw during development
4. Gradually reach feature parity without breaking daily use

**Tool fallback:** For unimplemented tools, shell out to OpenClaw wrappers. Full functionality from day one, replace with native Go as you go. No feature regression during porting.

**Layered config:**
```
openclaw.json (base)     →  workspace, skills, tools, memory, shared settings
       +
goclaw.json (overlay)    →  gateway port, telegram token, instance-specific overrides
       =  
merged runtime config    →  what GoClaw actually uses
```

Run both side-by-side:
- OpenClaw on port 3377, production bot
- GoClaw on port 1337, dev/test bot  
- Same workspace, skills, memory — different instances
- Compare behavior, test changes safely

## Progress

- [x] Project structure
- [x] Logging infrastructure (charmbracelet/log, unified L_info/L_error API)
- [x] CLI with Kong (gateway, start, stop, status, version)
- [x] Daemon support (go-daemon, PID file)
- [ ] Config loading with openclaw.json merge
- [ ] Telegram bot connection
- [ ] Anthropic streaming client
- [ ] Basic tool framework (exec, read, write, edit)
- [ ] Session/context management
- [ ] Cron scheduler
- [ ] Memory/embedding search

---

*Started 2026-02-01. Let's build this thing.*
