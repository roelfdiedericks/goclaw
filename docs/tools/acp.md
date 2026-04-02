---
title: "ACP Tools"
description: "Agent-facing tools for attaching, steering, and inspecting ACP sessions from GoClaw"
section: "Tools"
weight: 35
---

# ACP Tools

GoClaw exposes ACP control to the agent through two tools:

- `acp_control`
- `acp_inspect`

These are different from the user-facing `/acp` slash command. Use `/acp` when you want to control ACP manually from chat. Use these tools when the agent itself needs to attach to, inspect, or steer an ACP session.

## `acp_control`

`acp_control` changes ACP session state for the current GoClaw session identity.

Supported actions:

- `attach`
- `detach`
- `close`
- `cancel`
- `set_mode`
- `steer`

In the current MVP, `attach` targets the Cursor driver over local stdio.

### `attach`

Create a new ACP session, or attach to an existing one if `sessionId` is provided.

```json
{
  "action": "attach",
  "driver": "cursor",
  "cwd": "/path/to/project",
  "mode": "agent"
}
```

Attach to an existing ACP session:

```json
{
  "action": "attach",
  "driver": "cursor",
  "sessionId": "SESSION_ID"
}
```

| Parameter | Required | Description |
|-----------|----------|-------------|
| `action` | Yes | `attach` |
| `driver` | No | ACP driver ID. Defaults to `cursor` |
| `cwd` | No | Working directory for the attached ACP session |
| `mode` | No | Initial ACP mode, such as `agent`, `plan`, or `ask` |
| `sessionId` | No | Existing ACP session ID to load instead of creating a new one |

### `set_mode`

Change the attached ACP session mode:

```json
{
  "action": "set_mode",
  "mode": "plan"
}
```

### `steer`

Send a text prompt into the attached ACP session:

```json
{
  "action": "steer",
  "message": "Review the patch and summarize the remaining risks."
}
```

By default, steering detaches after completion. To keep the ACP session attached, set `stayAttached` to `true`:

```json
{
  "action": "steer",
  "message": "Stay in ACP mode for one more turn.",
  "stayAttached": true
}
```

| Parameter | Required | Description |
|-----------|----------|-------------|
| `action` | Yes | `steer` |
| `message` | Yes | Prompt to send into the attached ACP session |
| `stayAttached` | No | Keep the ACP session attached after steering. Defaults to `false` |

### `cancel`

Cancel the currently running ACP prompt without closing the session:

```json
{
  "action": "cancel"
}
```

### `detach`

Detach GoClaw from the ACP session while leaving the external ACP session available to re-attach later:

```json
{
  "action": "detach"
}
```

### `close`

Close the ACP session entirely:

```json
{
  "action": "close"
}
```

## `acp_inspect`

`acp_inspect` is a read-only view into the ACP state for the current GoClaw session.

Minimal call:

```json
{}
```

Optional detail hint:

```json
{
  "detail": "full"
}
```

The tool output can include:

- whether the session is attached
- ACP session ID, driver, transport, mode, and CWD
- current state and buffered event count
- last assistant text, last question, and last plan overview
- current ACP todo list
- pending interactive requests
- recent driver extension events

This is useful when the agent needs to decide whether to attach, whether an interactive prompt is still pending, or what the attached Cursor session last asked for.

## When To Use These Tools

Use ACP tools when the agent must:

- inspect whether an ACP session is already attached
- attach to Cursor before steering an external session
- switch ACP mode before sending a prompt
- cancel a blocked interactive ACP request
- keep or release ACP attachment intentionally after steering

Do not use ACP tools as a replacement for normal GoClaw conversation unless the task specifically needs an attached ACP session.

## See Also

- [ACP Sessions](../acp.md) — User-facing ACP workflow
- [Channel Commands](../commands.md) — Manual `/acp` commands
- [Channels](../channels.md) — Channel-specific ACP presentation
