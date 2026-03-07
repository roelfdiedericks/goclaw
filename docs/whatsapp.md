---
title: "WhatsApp Channel"
description: "Personal WhatsApp integration via linked device protocol"
section: "Channels"
weight: 3
---

# WhatsApp Channel

The WhatsApp channel lets you interact with GoClaw through your personal WhatsApp account using the linked device protocol (whatsmeow). Unlike the Telegram bot, this is **your personal WhatsApp** — the agent responds as you.

## Setup

### 1. Link Your Device

Before enabling WhatsApp, you need to pair your phone:

```bash
goclaw whatsapp link
```

This displays a QR code in your terminal. Scan it with WhatsApp:

1. Open WhatsApp on your phone
2. Go to **Settings > Linked Devices**
3. Tap **Link a Device**
4. Scan the QR code

Once paired, you'll see:
```
Paired successfully! JID: 1234567890@s.whatsapp.net
You can now enable WhatsApp in goclaw.json and start the gateway.
```

### 2. Enable the Channel

Add to your `goclaw.json`:

```json
{
  "channels": {
    "whatsapp": {
      "enabled": true
    }
  }
}
```

### 3. Configure User Identity

Use the CLI to set your WhatsApp ID:

```bash
goclaw users set-whatsapp <username> <phone_number>
```

For example:

```bash
goclaw users set-whatsapp alice 27821234567
```

The phone number should be in international format without the `+` — just the country code and number as shown in your JID (e.g., `27821234567` from `27821234567@s.whatsapp.net`).

Alternatively, edit `users.json` directly:

```json
{
  "users": [
    {
      "id": "alice",
      "name": "Alice",
      "role": "owner",
      "whatsapp_id": "27821234567"
    }
  ]
}
```

## Device Management

### Check Status

```bash
goclaw whatsapp status
```

Output:
```
Status: Paired
  JID: 1234567890@s.whatsapp.net
```

### Unlink Device

To remove the pairing:

```bash
goclaw whatsapp unlink
```

This clears the local session. You can re-pair with `goclaw whatsapp link`.

## Features

### Text Messages

Send text messages to yourself, and GoClaw responds as your assistant.

### Voice Notes

Voice notes are automatically transcribed using the configured STT provider (if enabled), then processed by the agent.

### Images

Images are downloaded and passed to the agent as vision content blocks (if the LLM supports vision).

### Reactions

The message tool supports WhatsApp reactions:
```json
{
  "action": "react",
  "channel": "whatsapp",
  "chatId": "1234567890",
  "messageId": "...",
  "emoji": "👍"
}
```

### Media Sending

The agent can send images, videos, audio, and documents via the `media_display` tool.

## Session Storage

WhatsApp session data is stored in:
```
~/.goclaw/whatsapp.db
```

This SQLite database contains encryption keys and session state. **Do not share this file** — it grants full access to your WhatsApp account.

## Limitations

### Personal Account Only

This is your personal WhatsApp, not a business API. The agent responds as you. Consider the implications:

- Messages appear to come from you
- Contacts can't tell it's an AI
- Use responsibly

### No Group Support

Group messages are ignored by default. The channel only responds to direct messages.

### One Device Per Instance

Each GoClaw instance can only be linked to one WhatsApp account. The linked device protocol allows multiple linked devices (phone, desktop, web), so GoClaw is just another linked device.

### Session Expiry

WhatsApp may invalidate linked device sessions after extended periods of inactivity. If you see "logged out" errors, run `goclaw whatsapp link` again.

## Troubleshooting

### "no whatsapp device paired"

Run `goclaw whatsapp link` to pair your phone.

### "logged out: 401"

Your session was invalidated. Run:
```bash
goclaw whatsapp unlink
goclaw whatsapp link
```

### QR Code Expired

QR codes expire after about 60 seconds. Run `goclaw whatsapp link` again to get a new one.

### Messages Not Arriving

1. Check the channel is enabled: `goclaw status`
2. Verify your `whatsapp_id` in `users.json` matches your JID
3. Check logs for authentication errors

---

## See Also

- [Channels Overview](channels.md) — All available channels
- [Telegram](telegram.md) — Telegram bot setup
- [Configuration](configuration.md) — Full config reference
