---
title: "Web UI"
description: "Built-in HTTP server with web chat interface"
section: "Channels"
weight: 20
---

# Web UI

GoClaw includes a built-in HTTP server that provides a web chat interface and REST API.

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

## Web Chat Interface

Access the chat interface at the root URL:

```
http://localhost:8080/
```

Or the dedicated chat page:

```
http://localhost:8080/chat
```

The web UI provides:
- Real-time streaming responses
- Session persistence
- Tool call visibility
- Message history

## API Endpoints

### Send Message

```
POST /api/send
```

Send a message to the agent. Returns JSON with an ID and processing status.

### Events Stream

```
GET /api/events
```

Server-sent events stream for receiving agent responses.
Reconnection uses the standard `Last-Event-ID` header.

### Session Status

```
GET /api/status
```

Returns current session information (token count, message count, etc.).

### Media

```
GET /api/media?path=<media-path>
```

Retrieve media files (screenshots, images).

### Metrics API

```
GET /api/metrics
```

JSON metrics data. See [Metrics](metrics.md) for details.

### Session Actions

```
GET  /api/sessions/{sessionKey}/events
POST /api/sessions/{sessionKey}/guidance
POST /api/sessions/{sessionKey}/llm
POST /api/sessions/{sessionKey}/message
```

Owner-only supervision endpoints.

### Metrics Page

```
GET /metrics
```

Metrics dashboard page.

## Authentication

The HTTP channel supports password authentication via `users.json`:

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

When credentials are configured, the web UI prompts for login.

## Security

- **Local only**: Bind to `127.0.0.1:1337` for local access (default)
- **All interfaces**: Use `0.0.0.0:1337` with caution
- **Authentication**: Configure user credentials for access control

GoClaw is designed for trusted network environments. Do not expose directly to the internet.

## Development Mode

For development, run with debug logging:

```bash
./goclaw gateway -d
```

---

## See Also

- [Voice](voice.md) — Real-time voice conversations
- [Channels](channels.md) — Channel overview
- [Metrics](metrics.md) — Monitoring and metrics
- [Roles](roles.md) — Access control
- [Configuration](configuration.md) — Full config reference
