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

Sessions are identified by a **session key** (e.g., `telegram:123456789` or `main`). Each channel typically creates separate sessions per user.

See [Session Management](session-management.md) for details on compaction and checkpoints.

## Channels

**Channels** are communication interfaces between users and the agent:

| Channel | Description |
|---------|-------------|
| [Telegram](telegram.md) | Bot interface via Telegram |
| [TUI](tui.md) | Interactive terminal UI |
| [HTTP](web-ui.md) | Web interface and API |
| [Cron](cron.md) | Scheduled task execution |

Each channel adapts its input/output format but uses the same underlying gateway.

See [Channels](channels.md) for the full overview.

## Tools

**Tools** extend the agent's capabilities beyond text generation:

| Category | Examples |
|----------|----------|
| File operations | read, write, edit |
| System | exec (shell commands) |
| Search | memory_search, transcript_search, web_search |
| Integration | hass (Home Assistant), browser, cron |

Tools are registered with the gateway and exposed to the LLM via function calling. The agent decides when and how to use them.

See [Tools](tools.md) for the complete tool reference.

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

See [LLM Providers](llm-providers.md) for configuration.

## Memory

GoClaw has two memory systems:

### Workspace Memory
Traditional markdown files that the agent can read and write:
- `MEMORY.md` — Long-term curated memories
- `memory/*.md` — Daily notes and logs

### Semantic Memory
Embeddings-based search over memory files and conversation transcripts:
- **memory_search** — Search memory files by meaning
- **transcript_search** — Search past conversations

See [Agent Memory](agent-memory.md) for the memory architecture.

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
- [Configuration](configuration.md) — All configuration options
- [Session Management](session-management.md) — Context and compaction
- [LLM Providers](llm-providers.md) — Provider setup
