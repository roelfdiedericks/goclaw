# Channels

Channels are communication interfaces that connect users to the GoClaw agent. Each channel adapts its input/output format but uses the same underlying gateway for processing.

## Available Channels

| Channel | Description | Documentation |
|---------|-------------|---------------|
| Telegram | Bot interface via Telegram messenger | [Telegram](telegram.md) |
| TUI | Interactive terminal user interface | [TUI](tui.md) |
| HTTP | Web interface and REST API | [Web UI](web-ui.md) |
| Cron | Scheduled task execution | [Cron](cron.md) |

## Channel Architecture

```
┌──────────────────────────────────────────────────────────────┐
│                         Channels                              │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐     │
│  │ Telegram │  │   TUI    │  │   HTTP   │  │   Cron   │     │
│  │   Bot    │  │ Terminal │  │  WebUI   │  │ Scheduler│     │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘     │
│       │             │             │             │            │
└───────┼─────────────┼─────────────┼─────────────┼────────────┘
        │             │             │             │
        └─────────────┴──────┬──────┴─────────────┘
                             │
                             ▼
                    ┌────────────────┐
                    │    Gateway     │
                    │  (Agent Loop)  │
                    └────────────────┘
```

All channels:
1. Receive user input (messages, commands, scheduled triggers)
2. Authenticate the user
3. Call the gateway's agent loop
4. Stream responses back to the user

## Channel Commands

All channels support the same set of slash commands:

| Command | Description |
|---------|-------------|
| `/status` | Session info and compaction health |
| `/clear` | Clear session history (alias: `/reset`) |
| `/cleartool` | Delete tool messages (fixes corruption) |
| `/compact` | Force context compaction |
| `/help` | List available commands |
| `/skills` | List available skills |
| `/heartbeat` | Trigger heartbeat check |
| `/hass` | Home Assistant status/debug |
| `/llm` | LLM provider status and cooldowns |
| `/embeddings` | Embeddings status and rebuild |

See [Channel Commands](commands.md) for detailed documentation.

## Channel Configuration

### Telegram

```json
{
  "telegram": {
    "enabled": true,
    "botToken": "123456:ABC..."
  }
}
```

Token can also be set via `TELEGRAM_BOT_TOKEN` environment variable.

### HTTP/Web UI

```json
{
  "http": {
    "enabled": true,
    "port": 8080
  }
}
```

### TUI

The TUI is launched via command line:
```bash
goclaw tui
```

Optional config:
```json
{
  "tui": {
    "showLogs": true
  }
}
```

### Cron

Cron jobs are defined in configuration:
```json
{
  "cron": {
    "enabled": true,
    "jobs": [
      {
        "name": "morning-briefing",
        "schedule": "0 8 * * *",
        "prompt": "Good morning! Give me a quick briefing.",
        "channel": "telegram"
      }
    ]
  }
}
```

## Session Keys

Each channel creates sessions with a specific key format:

| Channel | Session Key Format | Example |
|---------|-------------------|---------|
| Telegram | `telegram:<user_id>` | `telegram:123456789` |
| HTTP | `http:<session_id>` | `http:abc123` |
| TUI | `main` | `main` |
| Cron | `cron:<job_name>` | `cron:morning-briefing` |

## Message Tool

The `message` tool allows the agent to send messages to channels:

```json
{
  "action": "send",
  "channel": "telegram",
  "chatId": "123456789",
  "text": "Hello!"
}
```

It also supports channel-specific features:
- **Telegram**: Reactions, replies, formatting
- **HTTP**: WebSocket push notifications

See [Tools](tools.md) for message tool documentation.

## Enabling Multiple Channels

You can enable multiple channels simultaneously:

```json
{
  "telegram": {
    "enabled": true,
    "botToken": "..."
  },
  "http": {
    "enabled": true,
    "port": 8080
  }
}
```

Each channel operates independently with its own sessions, but they share:
- The same gateway instance
- The same tools and skills
- The same user registry

---

## See Also

- [Telegram](telegram.md) — Telegram bot setup
- [TUI](tui.md) — Terminal interface
- [Web UI](web-ui.md) — HTTP interface
- [Cron](cron.md) — Scheduled tasks
- [Channel Commands](commands.md) — Slash commands
- [Configuration](configuration.md) — Full config reference
