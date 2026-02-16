---
title: "Message"
description: "Send, edit, delete, and react to messages across channels"
section: "Tools"
weight: 70
---

# Message Tool

Send, edit, delete, and react to messages across channels.

## Actions

### send

Send a message to a channel.

```json
{
  "action": "send",
  "text": "Hello!",
  "channel": "telegram",
  "chatId": "123456"
}
```

Omit `channel` to broadcast to all connected channels.

| Parameter | Required | Description |
|-----------|----------|-------------|
| `action` | Yes | `send` |
| `text` | Yes | Message content |
| `channel` | No | Target channel (telegram, http, etc.) |
| `chatId` | No | Chat/conversation ID |
| `filePath` | No | Single media file to attach |
| `content` | No | Array for mixed text/media |

### edit

Edit an existing message.

```json
{
  "action": "edit",
  "messageId": "789",
  "text": "Updated message"
}
```

| Parameter | Required | Description |
|-----------|----------|-------------|
| `action` | Yes | `edit` |
| `messageId` | Yes | Message to edit |
| `text` | Yes | New content |

### delete

Delete a message.

```json
{
  "action": "delete",
  "messageId": "789"
}
```

| Parameter | Required | Description |
|-----------|----------|-------------|
| `action` | Yes | `delete` |
| `messageId` | Yes | Message to delete |

### react

Add a reaction to a message.

```json
{
  "action": "react",
  "messageId": "789",
  "emoji": "👍"
}
```

| Parameter | Required | Description |
|-----------|----------|-------------|
| `action` | Yes | `react` |
| `messageId` | Yes | Message to react to |
| `emoji` | Yes | Reaction emoji |

## Media

### Single File

```json
{
  "action": "send",
  "text": "Check this out",
  "filePath": "/path/to/image.png"
}
```

### Mixed Content

```json
{
  "action": "send",
  "content": [
    {"type": "text", "text": "Here are the files:"},
    {"type": "media", "path": "/path/to/doc.pdf"},
    {"type": "media", "path": "/path/to/image.png"}
  ]
}
```

## Channel Detection

When `channel` is omitted:
- Uses the channel that triggered the current request
- For broadcast, sends to all active channels

## Supported Channels

| Channel | Send | Edit | Delete | React |
|---------|------|------|--------|-------|
| Telegram | Yes | Yes | Yes | Yes |
| HTTP | Yes | No | No | No |

---

## See Also

- [Channels](../channels.md) — Channel overview
- [Telegram](../telegram.md) — Telegram bot
- [Tools](../tools.md) — Tool overview
