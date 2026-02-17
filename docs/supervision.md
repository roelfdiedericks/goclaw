---
title: "Session Supervision"
description: "Real-time monitoring, guidance, and ghostwriting for active agent sessions"
section: "Advanced"
weight: 35
---

# Session Supervision

Session supervision allows owners to monitor, guide, and intervene in active agent sessions in real-time.

## Overview

When a user is chatting with the agent via Telegram, HTTP, or any channel, an owner can:

- **Watch** — See all messages, tool calls, and thinking in real-time
- **Guide** — Send instructions that the agent sees and responds to
- **Ghostwrite** — Send messages as if they came from the agent
- **Control** — Pause the agent (disable LLM) and take over manually

This is useful for:

- Debugging agent behavior
- Training or correcting the agent mid-conversation
- Taking over when the agent is stuck or making mistakes
- Monitoring sensitive conversations

### Availability

Session supervision is currently available only through the **HTTP/Web interface**. The TUI and Telegram channels do not have supervision capabilities.

### Who Can Supervise

Only users with the **owner** role can supervise sessions. This is enforced at the API level — non-owners receive a 403 Forbidden response.

Owners can supervise any active session, including:

- Their own sessions
- Other users' sessions (Telegram, HTTP)
- Cron job sessions

---

## Concepts

### Sessions

Every conversation has a session identified by a key:

| Channel | Session Key Format | Example |
|---------|-------------------|---------|
| Telegram | `telegram:<user_id>` | `telegram:123456789` |
| HTTP | `http:<session_id>` | `http:abc123` |
| TUI | `main` | `main` |
| Cron | `cron:<job_name>` | `cron:morning-briefing` |

### Supervision State

When you start supervising a session, the system tracks:

| State | Description |
|-------|-------------|
| `supervised` | Whether someone is watching |
| `supervisorID` | Who is supervising |
| `llmEnabled` | Whether the agent can respond |
| `pendingGuidance` | Guidance waiting to be consumed |
| `interruptFlag` | Request to stop current generation |

### Guidance vs Ghostwriting

| Action | What Happens | Agent Responds? |
|--------|--------------|-----------------|
| **Guidance** | Message added as user message with prefix, agent sees it | Yes |
| **Ghostwrite** | Message added as assistant message, delivered to user | No |

**Guidance** is like whispering instructions to the agent. The user sees the guidance (prefixed) and the agent's response.

**Ghostwriting** is pretending to be the agent. The user sees your message as if the agent wrote it. The agent doesn't respond.

---

## Using Supervision

### Web UI

The web interface at `http://localhost:1337` provides a supervision panel for owners:

1. Log in as an owner
2. View active sessions in the status panel
3. Click a session to start supervising
4. Use the supervision controls to guide or ghostwrite

### API Endpoints

All supervision endpoints require owner authentication and use the session key in the URL path.

#### List Sessions

```
GET /api/status
```

Returns session information for owners, including:

```json
{
  "sessions": [
    {
      "key": "telegram:123456789",
      "messages": 45,
      "totalTokens": 12500,
      "maxTokens": 200000,
      "contextUsage": 0.0625,
      "supervised": false,
      "llmEnabled": true,
      "updatedAt": "2026-02-17T10:30:00Z"
    }
  ]
}
```

#### Start Supervision (SSE Stream)

```
GET /api/sessions/:key/events
```

Opens a Server-Sent Events (SSE) stream for real-time supervision.

**Events received:**

| Event | Description |
|-------|-------------|
| `connected` | Initial connection with session info |
| `history` | Existing messages (sent on connect) |
| `user_message` | New user message |
| `start` | Agent started generating |
| `message` | Agent text delta |
| `thinking` | Agent thinking content |
| `thinking_delta` | Agent thinking delta |
| `tool_start` | Tool execution started |
| `tool_end` | Tool execution completed |
| `done` | Agent finished generating |
| `agent_error` | Agent encountered an error |

**Example event:**

```
event: message
data: {"runId":"abc123","content":"Hello, "}

event: tool_start
data: {"runId":"abc123","toolName":"read","toolId":"tool_1","input":"{\"path\":\"file.txt\"}"}

event: tool_end
data: {"runId":"abc123","toolName":"read","toolId":"tool_1","result":"file contents...","isError":false}

event: done
data: {"runId":"abc123","finalText":"Hello, I read the file for you."}
```

When you disconnect from the SSE stream, supervision ends automatically.

#### Send Guidance

```
POST /api/sessions/:key/guidance
Content-Type: application/json

{
  "content": "Please be more concise in your responses."
}
```

The guidance is:

1. Added to the session as a user message with the configured prefix (default: `[Supervisor]: `)
2. The agent sees and responds to it
3. The response is delivered to the user normally

**Response:**

```json
{
  "status": "delivered",
  "regenerating": true
}
```

#### Ghostwrite Message

```
POST /api/sessions/:key/message
Content-Type: application/json

{
  "content": "I apologize for the confusion. Let me clarify..."
}
```

The message is:

1. Added to the session as an assistant message
2. Delivered to the user's channel (Telegram, HTTP, etc.)
3. The agent does NOT respond (this IS the response)

**Response:**

```json
{
  "status": "sent",
  "messageId": "ghost_1708171234567890"
}
```

#### Toggle LLM

```
POST /api/sessions/:key/llm
Content-Type: application/json

{
  "enabled": false
}
```

When `enabled: false`:

- The agent will not respond to user messages
- You can ghostwrite responses instead
- The user doesn't know the agent is paused

When `enabled: true`:

- Normal operation resumes
- Agent responds to messages

**Response:**

```json
{
  "status": "ok",
  "llmEnabled": false
}
```

---

## Configuration

```json
{
  "supervision": {
    "guidance": {
      "prefix": "[Supervisor]: ",
      "systemNote": ""
    },
    "ghostwriting": {
      "typingDelayMs": 500
    }
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `guidance.prefix` | string | `"[Supervisor]: "` | Prefix prepended to guidance messages |
| `guidance.systemNote` | string | `""` | Optional system message injected with guidance |
| `ghostwriting.typingDelayMs` | int | `500` | Delay before delivering ghostwritten message |

### Guidance Prefix

The prefix helps the agent understand that the message is from the supervisor, not the user. The agent sees:

```
[Supervisor]: Please be more concise in your responses.
```

You can customize this to match your agent's system prompt or remove it entirely.

### Typing Delay

The `typingDelayMs` adds a small delay before delivering ghostwritten messages. This makes the response feel more natural (not instant) and gives you time to cancel if needed.

---

## Audit Trail

Supervised interventions are logged and tracked:

- Messages include `supervisor` field identifying who intervened
- Messages include `interventionType` field (`guidance` or `ghostwrite`)
- All supervision actions are logged with timestamps

This allows you to review what interventions were made and by whom.

---

## Security Considerations

### Owner-Only Access

Supervision is restricted to owners for good reason — it allows:

- Reading any user's conversation
- Injecting messages into any session
- Impersonating the agent

Do not grant owner role to users who shouldn't have this access.

### Visibility to Users

Users are NOT notified when their session is being supervised. The guidance prefix is visible, but they may not understand what it means.

Consider your privacy policies and user expectations when using supervision.

### Session Persistence

Supervision state is ephemeral — it's not persisted to the database. If GoClaw restarts, supervision ends. However, the messages (including guidance and ghostwritten messages) ARE persisted in the session history.

---

## Limitations

### Channel Support

Currently, supervision is only available through the HTTP API. Future versions may add:

- Telegram supervision commands
- TUI supervision panel
- CLI supervision tools

### No Interruption

While the code has infrastructure for interrupting generation (`interruptFlag`, `cancelFunc`), this is not yet exposed through the API. You can disable LLM and ghostwrite instead.

### Single Supervisor

Only one supervisor can watch a session at a time. Starting supervision from another client will close the previous SSE connection.

---

## See Also

- [Roles & Access Control](roles.md) — Owner role and permissions
- [Web UI](web-ui.md) — HTTP interface
- [Configuration](configuration.md) — Full config reference
- [Architecture](architecture.md) — System internals
