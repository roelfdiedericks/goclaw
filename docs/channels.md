---
title: "Channels"
description: "Communication interfaces for GoClaw: Telegram, WhatsApp, Web UI, Voice, and TUI"
section: "Channels"
weight: 1
landing: true
---

# Channels

Channels are communication interfaces that connect users to the GoClaw agent. Each channel adapts its input/output format but uses the same underlying gateway for processing.

## Available Channels

| Channel | Description | Documentation |
|---------|-------------|---------------|
| Telegram | Bot interface via Telegram messenger | [Telegram](telegram.md) |
| WhatsApp | Personal WhatsApp via linked device | [WhatsApp](whatsapp.md) |
| HTTP | Web interface and REST API | [Web UI](web-ui.md) |
| HTTP Voice | Real-time voice conversations | Below |
| TUI | Interactive terminal user interface | [TUI](tui.md) |
| Cron | Scheduled task execution | [Cron](cron.md) |

## Channel Architecture

```
┌───────────────────────────────────────────────────────────────────────┐
│                              Channels                                  │
│  ┌────────┐ ┌────────┐ ┌────────┐ ┌──────────┐ ┌─────┐ ┌──────┐      │
│  │Telegram│ │WhatsApp│ │  HTTP  │ │HTTP Voice│ │ TUI │ │ Cron │      │
│  │  Bot   │ │ Linked │ │ WebUI  │ │   WS     │ │Term │ │Sched │      │
│  └───┬────┘ └───┬────┘ └───┬────┘ └────┬─────┘ └──┬──┘ └──┬───┘      │
│      │          │          │           │          │       │           │
└──────┼──────────┼──────────┼───────────┼──────────┼───────┼───────────┘
       │          │          │           │          │       │
       └──────────┴──────────┴─────┬─────┴──────────┴───────┘
                                   │
                   ┌───────────────┴───────────────┐
                   │                               │
                   ▼                               ▼
          ┌────────────────┐             ┌─────────────────┐
          │    Gateway     │             │ VoiceLLM        │
          │  (Text Agent)  │             │ (Voice Agent)   │
          └────────────────┘             └─────────────────┘
```

**Text channels** (Telegram, WhatsApp, HTTP, TUI, Cron) use the main Gateway and LLM Registry.

**Voice channel** (HTTP Voice) uses a separate VoiceLLM Registry with per-session WebSocket connections to the configured real-time voice provider.

All channels:
1. Receive user input (messages, commands, scheduled triggers)
2. Authenticate the user
3. Call the gateway's agent loop
4. Stream responses back to the user

## Channel Commands

All text channels support the same set of slash commands:

| Command | Description |
|---------|-------------|
| `/status` | Session info and compaction health |
| `/clear` | Clear session history (alias: `/reset`) |
| `/cleartool` | Delete tool messages (fixes corruption) |
| `/stop` | Stop all running agent tasks |
| `/compact` | Force context compaction |
| `/help` | List available commands |
| `/skills` | List available skills |
| `/heartbeat` | Trigger heartbeat check |
| `/hass` | Home Assistant status/debug |
| `/llm` | LLM provider status and cooldowns |
| `/embeddings` | Embeddings status and rebuild |

See [Channel Commands](commands.md) for detailed documentation.

## Channel Configuration

All channel configs live under `channels` in `goclaw.json`:

### Telegram

```json
{
  "channels": {
    "telegram": {
      "enabled": true,
      "botToken": "123456:ABC..."
    }
  }
}
```

The setup wizard can detect `TELEGRAM_BOT_TOKEN` from your environment.

### WhatsApp

```json
{
  "channels": {
    "whatsapp": {
      "enabled": true
    }
  }
}
```

WhatsApp uses the linked device protocol (whatsmeow). Run `goclaw whatsapp link` first to scan a QR code with your phone. Session data persists in `~/.goclaw/whatsapp.db`.

**Note:** This is your personal WhatsApp, not a business API. The agent responds as you.

### HTTP/Web UI

```json
{
  "channels": {
    "http": {
      "enabled": true,
      "listen": ":1337"
    }
  }
}
```

### HTTP Voice

HTTP Voice is enabled via `voicellm` configuration (not `channels`):

```json
{
  "voicellm": {
    "enabled": true,
    "default": "xai",
    "providers": {
      "xai": {
        "driver": "xai",
        "apiKey": "xai-...",
        "voice": "Charon"
      }
    }
  }
}
```

The `/voice` page route is available on the HTTP server. When VoiceLLM is enabled and initialized, the WebSocket route `/voice/ws` is registered for real-time audio.

See [Configuration](configuration.md#voice-llm-real-time-voice) for full VoiceLLM options.

### TUI

The TUI is launched via command line:
```bash
goclaw tui
```

Optional config:
```json
{
  "channels": {
    "tui": {
      "showLogs": true
    }
  }
}
```

### Cron

Cron jobs are defined separately (not under `channels`):
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

Session keys are user-centric in the gateway:

- Owner users default to the primary session key (`primary`).
- Non-owner users default to `user:<id>`.
- Channels may pass an explicit session ID in some flows, which then becomes the session key for that request.

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
- **WhatsApp**: Reactions, replies, media
- **HTTP**: WebSocket push notifications

See [Tools](tools.md) for message tool documentation.

## Enabling Multiple Channels

You can enable multiple channels simultaneously:

```json
{
  "channels": {
    "telegram": {
      "enabled": true,
      "botToken": "..."
    },
    "whatsapp": {
      "enabled": true
    },
    "http": {
      "enabled": true,
      "listen": ":1337"
    }
  }
}
```

Each channel operates independently with its own sessions, but they share:
- The same gateway instance
- The same tools and skills
- The same user registry
- The same Memory Graph

## HTTP Voice Channel

HTTP Voice provides real-time voice conversations via WebSocket. Unlike text channels, it:

- Uses **VoiceLLM Registry** instead of the standard LLM Registry
- Maintains **per-session WebSocket** connections to the voice provider
- Supports **interruption** — user can speak while agent is talking
- Handles **audio streaming** in both directions

### Architecture

```
Browser/Client                   GoClaw                    Voice Provider
    │                              │                              │
    │◄──── WebSocket ────────────►│                              │
    │      (audio chunks)          │◄──── WebSocket ────────────►│
    │                              │      (real-time voice API)   │
    │  mic audio ─────────────────►│ ─────────────────────────────►
    │◄──────────────────────────── │ ◄─────────────────────────────
    │  speaker audio               │        agent audio           │
```

### Supported Providers

| Provider | Driver | Features |
|----------|--------|----------|
| xAI Voice | `xai` | Grok-based real-time voice |

### VoiceLLM vs LLM Registry

Voice uses a completely separate provider system:

| Aspect | LLM Registry | VoiceLLM Registry |
|--------|--------------|-------------------|
| Config key | `llm.providers` | `voicellm.providers` |
| Connection | Per-request | Per-session WebSocket |
| I/O | Text | Audio streams |
| Interruption | N/A | Native support |
| Tools | Full tool system | Via voice API |

---

## See Also

- [Telegram](telegram.md) — Telegram bot setup
- [WhatsApp](whatsapp.md) — WhatsApp linked device
- [TUI](tui.md) — Terminal interface
- [Web UI](web-ui.md) — HTTP interface
- [Voice](voice.md) — Real-time voice conversations
- [Cron](cron.md) — Scheduled tasks
- [Channel Commands](commands.md) — Slash commands
- [Configuration](configuration.md) — Full config reference
