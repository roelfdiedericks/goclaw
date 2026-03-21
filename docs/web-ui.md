---
title: "Web UI"
description: "Built-in HTTP server with web chat interface"
section: "Channels"
weight: 20
---

# Web UI

GoClaw includes a built-in HTTP server with a browser chat interface.
Use it when you want a local web dashboard for chatting, uploads, and supervision.

## Configuration

```json
{
  "channels": {
    "http": {
      "enabled": true,
      "listen": ":8080"
    }
  }
}
```

| Option | Default | Description |
|--------|---------|-------------|
| `enabled` | `true` when unset | Enable HTTP server |
| `listen` | `:1337` | Address to listen on (e.g., `:8080`, `127.0.0.1:8080`) |

## Open the Chat

When GoClaw is running, open:

```
http://localhost:1337/
```

Or directly:

```
http://localhost:1337/chat
```

Read-only transcript view:

```
http://localhost:1337/chat/transcript
```

## What You Can Do in the Web UI

- Real-time streaming responses
- Ongoing message history in your browser
- File uploads from the paperclip button
- Drag and drop files into chat
- Paste images directly into chat
- Optional tool/thinking visibility (based on your session settings)
- Read-only transcript view in a separate window

## Streaming Display Modes

Use the **Stream** dropdown in the chat header:

- **Plain**: fastest, minimal formatting while text is still arriving
- **Debounced Markdown**: markdown refreshes in short intervals
- **Per-Token Markdown**: markdown updates on every streamed chunk

Final response formatting is always applied when a turn completes.

## File Uploads

You can upload one or more files with each message.

- Images are sent as image content when supported by the active model.
- Other files are attached with filename and type metadata.
- Uploaded files are stored in the media library under channel/user upload paths.
- Original upload filenames are preserved in stored media filenames (sanitized and uniqued).

## Transcript View

The **Transcript** button opens a read-only page from your browser-saved chat history.

- It is local to your browser profile.
- It is designed for reading/printing, not editing.
- Links open in a new tab so the transcript page stays intact.

## Chat History and Performance

The chat keeps a working window in the DOM for smooth performance and lets you load earlier messages as needed.

If your chat is very long:
- use **Load earlier messages** near the top
- keep the browser tab open for best continuity
- use transcript view for long-form reading

## Accessibility and Link Behavior

- Connection status and completion announcements are screen-reader friendly.
- Icon controls include labels.
- Links clicked inside chat messages open in a new tab.

## Authentication

HTTP login uses `users.json` credentials. When credentials are present, the web UI prompts for login.

Example:

```json
{
  "users": [
    {
      "name": "Alice",
      "role": "owner",
      "identities": [
        {"provider": "http", "id": "alice"}
      ],
      "credentials": [
        {"type": "password", "hash": "<argon2-hash>", "label": "web-login"}
      ]
    }
  ]
}
```

## Security

- Use `127.0.0.1:1337` for local-only access.
- Use `0.0.0.0:1337` only on trusted networks.
- Enable credentials for shared environments.
- Do not expose GoClaw directly to the public internet.

## Next Steps

- [Tools](tools.md) — Available tools and tool execution behavior
- [Voice](voice.md) — Voice conversations
- [Channels](channels.md) — Other channel options
- [Roles](roles.md) — Access control
- [Configuration](configuration.md) — Full config reference

---
