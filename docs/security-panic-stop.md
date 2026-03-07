---
title: "Panic Stop"
description: "Emergency stop to immediately cancel agent runs"
section: "Security"
weight: 15
---

# Panic Stop

Panic stop is a safety mechanism that immediately cancels all running agent sessions for a user. When triggered, any in-progress LLM calls are aborted and tool executions are halted.

## How It Works

GoClaw monitors all incoming messages for configured panic phrases. When a panic phrase is detected:

1. All active sessions for the user are cancelled
2. Running tool calls are interrupted
3. The agent stops immediately
4. You receive confirmation the stop was triggered

The check happens **before** any other processing, so even if the system is busy, the panic phrase takes priority.

## Default Behavior

Panic stop is enabled by default. The default phrase is `STOP` (case-insensitive).

Repeating the phrase also works: `stop stop stop` or `STOP STOP STOP` will trigger the stop.

## Configuration

Configure panic phrases in `goclaw.json`:

```json
{
  "safety": {
    "panicEnabled": true,
    "panicPhrases": ["STOP", "HALT", "EMERGENCY"]
  }
}
```

| Option | Default | Description |
|--------|---------|-------------|
| `panicEnabled` | `true` | Enable/disable panic phrase detection |
| `panicPhrases` | `["STOP"]` | List of phrases that trigger emergency stop |

### Multiple Phrases

You can configure multiple phrases for different preferences:

```json
{
  "safety": {
    "panicPhrases": ["STOP", "CANCEL", "ABORT", "HALT"]
  }
}
```

### Disabling

To disable panic stop entirely (not recommended):

```json
{
  "safety": {
    "panicEnabled": false
  }
}
```

## Supported Channels

Panic stop works across all channels:

- **HTTP / Web UI** — Type the phrase in the chat
- **Telegram** — Send the phrase as a message
- **WhatsApp** — Send the phrase as a message
- **TUI** — Type the phrase at the prompt

## When to Use

- Agent is stuck in a loop
- Wrong command is executing
- You need to regain control immediately
- Something unexpected is happening

## Limitations

- **Network calls in progress** — A network request that's already sent cannot be unsent
- **Completed actions** — Files already written or commands already finished cannot be undone
- **Non-message contexts** — Panic phrases only work through message channels, not webhooks or cron

## Alternatives

For less urgent situations:

- **`/stop` command** — Cancels the current session gracefully
- **Supervision** — Pause, guide, or interrupt via the web UI (owner only)

---

## See Also

- [Security](security.md) — Security overview
- [Supervision](web-ui.md#supervision) — Session monitoring and control
