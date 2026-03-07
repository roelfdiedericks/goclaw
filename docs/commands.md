---
title: "Channel Commands"
description: "Slash commands available across all channels"
section: "Channels"
weight: 40
---

# Channel Commands

GoClaw provides slash commands available across all channels (Telegram, TUI, HTTP).

## Command Reference

| Command | Description |
|---------|-------------|
| `/status` | Show session info and compaction health |
| `/clear` | Clear conversation history |
| `/cleartool` | Delete all tool messages (fixes corruption) |
| `/stop` | Stop all running agent tasks |
| `/compact` | Force context compaction |
| `/help` | List available commands |
| `/skills` | List available skills |
| `/heartbeat` | Trigger heartbeat check |
| `/hass` | Home Assistant status and debug |
| `/llm` | LLM provider status and cooldown management |
| `/embeddings` | Embeddings status and rebuild |

## Command Details

### /status

Shows current session information and compaction health.

**Output:**
```
Session Status
  Messages: 45
  Tokens: 12,500 / 200,000 (6.3%)
  Compactions: 0

Compaction Health
  LLM: available

Skills: 54 total, 12 eligible
```

When compaction has issues:
```
Compaction Health
  LLM: unavailable
  Pending retries: 1
```

### /clear

Clears all conversation history for the current session. Alias: `/reset`

Use this when:
- Starting fresh on a new topic
- Session is corrupted
- Context has drifted too far from the task

### /cleartool

Deletes all tool-related messages (tool_use and tool_result) from the session.

Use this when:
- Tool message corruption is causing errors
- Orphaned tool results are confusing the agent
- Need to clean up without losing conversation context

### /stop

Stops all running agent tasks for your user across all sessions.

**Output:**
```
Stopped all agent tasks for user Alice
```

Use this when:
- Agent is stuck in a long-running operation
- Need to interrupt an unwanted task
- Agent is in an infinite loop or runaway state

Note: This is a "panic stop" that immediately cancels all in-progress agent runs. The agent will not complete any pending tool calls.

### /compact

Forces immediate context compaction, even if the token threshold hasn't been reached.

**Output:**
```
Compaction completed!
  Tokens before: 175,000
  Tokens after: 45,000
  Summary source: LLM
```

Use this when:
- Proactively reducing context before a long task
- Testing compaction behavior
- Freeing up context window space

### /help

Lists all available commands with descriptions.

### /skills

Lists available skills with their status.

**Output:**
```
Skills (12 eligible of 54)

Eligible:
  🌤️ weather - Get current weather and forecasts
  📧 himalaya - CLI to manage emails
  ...

Flagged (1):
  ⚠️ suspicious-skill - Security flag: env_file
```

### /heartbeat

Triggers a heartbeat check manually. Useful for testing heartbeat behavior or forcing the agent to check external sources.

### /hass

Home Assistant integration status and debugging.

**Usage:**
```
/hass           # Show connection status
/hass debug     # Show debug info
/hass info      # Show entity info
/hass subs      # Show active subscriptions
```

### /llm

LLM provider status and cooldown management.

**Usage:**
```
/llm            # Show provider status
/llm status     # Same as above
/llm reset      # Clear all cooldowns
```

**Output:**
```
LLM Provider Status

claude: healthy
ollama-qwen: cooldown (rate_limit), retry in 2m30s
ollama-embed: healthy
```

### /embeddings

Embeddings status and rebuild.

**Usage:**
```
/embeddings           # Show status
/embeddings status    # Same as above
/embeddings rebuild   # Rebuild all embeddings
```

**Output:**
```
Embeddings Status

Session transcripts: 1,234 chunks
Memory files: 56 chunks

Model: nomic-embed-text
Provider: ollama
```

## Channel-Specific Behavior

Commands work the same across all channels, but output formatting may vary:

| Channel | Formatting |
|---------|------------|
| Telegram | Markdown with bold headers |
| TUI | Plain text |
| HTTP | Plain text or JSON |

## Adding Custom Commands

Commands are registered at startup. Custom commands can be added by extending the command handler in the codebase.

---

## See Also

- [Channels](channels.md) — Channel overview
- [Telegram](telegram.md) — Telegram bot
- [TUI](tui.md) — Terminal interface
- [Web UI](web-ui.md) — HTTP interface
