# HTTP API

GoClaw includes a built-in HTTP server for web-based interactions and API access.

## Configuration

Enable the HTTP channel in your `goclaw.json`:

```json
{
  "channels": {
    "http": {
      "enabled": true,
      "port": 3333,
      "host": "127.0.0.1"
    }
  }
}
```

### Options

| Option | Default | Description |
|--------|---------|-------------|
| `enabled` | `false` | Enable HTTP channel |
| `port` | `3333` | Port to listen on |
| `host` | `127.0.0.1` | Bind address (use `0.0.0.0` for all interfaces) |

## Endpoints

### Chat

Send a message to the agent:

```bash
curl -X POST http://localhost:3333/chat \
  -H "Content-Type: application/json" \
  -d '{"message": "Hello, what time is it?"}'
```

Response:

```json
{
  "response": "It's currently 3:42 PM.",
  "session_id": "abc123"
}
```

### Health Check

```bash
curl http://localhost:3333/health
```

Returns `200 OK` if the gateway is running.

## Authentication

By default, the HTTP API has no authentication. For production use, place it behind a reverse proxy with authentication, or bind only to localhost.

## Web UI

When enabled, the HTTP channel also serves a simple web chat interface at the root URL:

```
http://localhost:3333/
```

## Security Considerations

- Never expose the HTTP API directly to the internet without authentication
- Use a reverse proxy (nginx, Caddy) with TLS and authentication
- Bind to `127.0.0.1` for local-only access
- Consider using the [Telegram channel](telegram.md) for remote access instead
