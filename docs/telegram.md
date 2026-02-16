---
title: "Telegram"
description: "Configure and use the Telegram bot channel"
section: "Channels"
weight: 10
---

# Telegram Integration

GoClaw includes a Telegram bot for interacting with the agent via chat.

## Setup

### 1. Create a Bot

1. Message [@BotFather](https://t.me/BotFather) on Telegram
2. Send `/newbot` and follow the prompts
3. Copy the bot token (looks like `123456789:ABCdefGHI...`)

### 2. Configure GoClaw

```json
{
  "telegram": {
    "enabled": true,
    "botToken": "YOUR_BOT_TOKEN"
  }
}
```

**Note:** The setup wizard (`goclaw setup`) can detect `TELEGRAM_BOT_TOKEN` from your environment and offer to use it.

### 3. Set Up User Access

Add authorized users to `users.json`:

```json
{
  "users": [
    {
      "name": "TheRoDent",
      "role": "owner",
      "identities": [
        {"provider": "telegram", "id": "123456789"}
      ]
    }
  ]
}
```

Find your Telegram user ID by messaging [@userinfobot](https://t.me/userinfobot).

---

## Commands

All channel commands are available via Telegram:

| Command | Description |
|---------|-------------|
| `/start` | Initialize bot conversation |
| `/status` | Show session info and compaction health |
| `/clear` | Clear session history (alias: `/reset`) |
| `/cleartool` | Delete tool messages (fixes corruption) |
| `/compact` | Force context compaction |
| `/help` | List available commands |
| `/skills` | List available skills |
| `/heartbeat` | Trigger heartbeat check |
| `/hass` | Home Assistant status/debug |
| `/llm` | LLM provider status and cooldowns |
| `/embeddings` | Embeddings status and rebuild |

See [Channel Commands](commands.md) for full documentation.

### `/status` Output

```
Session Status
Messages: 45
Tokens: 12,500 / 200,000 (6.3%)
Compactions: 0

Compaction Health
Ollama: healthy (0/3 failures)
Mode: normal
Last attempt: 5 min ago
```

When Ollama is failing:
```
Compaction Health
Ollama: degraded (3/3 failures)
Mode: fallback to main model
Reset in: 25 min
Pending retries: 1
```

### `/compact` Output

```
Compaction completed!
Tokens before: 175,000
Summary source: LLM
```

---

## Features

### Text Messages

Send any text message to chat with the agent. The agent has access to your workspace and can:
- Read and write files
- Execute commands
- Search memory
- Use configured tools

### Images

Send images to the bot. They're:
1. Downloaded and stored temporarily
2. Passed to the LLM as vision input
3. Cleaned up after TTL expires

Supported formats: JPEG, PNG, GIF, WebP

### Voice Messages

Voice messages are transcribed (if configured) and processed as text.

### Reactions

The agent can react to messages with emoji using the `message` tool:

```
React to user's message with 👍
```

### Replies

The agent can reply to specific messages:

```
Reply to message ID 123 with "Done!"
```

---

## Message Formatting

The agent's responses support Telegram formatting:

| Syntax | Result |
|--------|--------|
| `*bold*` | **bold** |
| `_italic_` | *italic* |
| `` `code` `` | `code` |
| ``` ```code block``` ``` | Code block |

---

## Multi-User Support

Each Telegram user gets their own session. Sessions are keyed by:
```
telegram:<user_id>
```

### Shared Sessions (Optional)

To share a session across users, configure:

```json
{
  "session": {
    "writeToKey": "shared"
  }
}
```

---

## Troubleshooting

### Bot Not Responding

1. Check bot token is correct
2. Verify `telegram.enabled: true`
3. Check logs for errors:
   ```bash
   make debug 2>&1 | grep telegram
   ```

### "Unauthorized" Errors

Add your user ID to `users.json`:
```json
{
  "users": [
    {
      "name": "TheRoDent",
      "role": "owner",
      "identities": [
        {"provider": "telegram", "id": "YOUR_USER_ID"}
      ]
    }
  ]
}
```

### Messages Not Sending

Check for rate limiting. Telegram limits:
- 30 messages/second to different chats
- 1 message/second to same chat
- 20 messages/minute to same group

### Images Not Processing

1. Check media directory exists: `~/.goclaw/media/`
2. Verify file size under limit (default 5MB)
3. Check supported format (JPEG, PNG, GIF, WebP)

---

## Security

### User Authorization

Only users in `users.json` can interact with the bot. Unauthorized users receive no response.

### Rate Limiting

Consider implementing rate limiting for public bots to prevent abuse.

### Sensitive Data

The agent has access to your workspace. Be careful about:
- API keys in files
- Private documents
- Executable commands

---

## See Also

- [Configuration](./configuration.md) - Telegram config options
- [Architecture](./architecture.md) - How channels work
- [Tools](./tools.md) - Message tool for Telegram actions
