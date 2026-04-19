---
title: "A2A Tool"
description: "Agent-facing A2A tool for inspecting peer state and interacting with remote GoClaw nodes"
section: "Tools"
weight: 36
---

# A2A Tool

GoClaw exposes A2A operations to the agent through a native `a2a` tool.

This is different from the user-facing `/a2a` slash command:

- use `/a2a` when you want to inspect or control A2A manually from chat
- use the `a2a` tool when the agent itself needs structured A2A results

The tool returns structured JSON text rather than command-formatted text.

## Availability

The `a2a` tool is owner-only.

That restriction applies to all actions, including read-only inspection.

## Supported Actions

- `status`
- `peers`
- `tasks`
- `pair`
- `ping`
- `submit`
- `resume`
- `cancel`

## Examples

### `status`

```json
{
  "action": "status"
}
```

Returns the current A2A runtime status, including transport, readiness, address state, and peer counters.

### `peers`

```json
{
  "action": "peers",
  "filter": "connected"
}
```

Useful filters include:

- `all`
- `connected`
- `trusted`
- `authorized`
- `discovered`
- `relayed`
- `disconnected`

### `tasks`

```json
{
  "action": "tasks",
  "filter": "active",
  "peer": "wsl"
}
```

Useful filters include:

- `all`
- `active`
- `resumable`
- `failed`
- `inbound`
- `outbound`

### `pair`

```json
{
  "action": "pair"
}
```

Returns the local pairing payload for this node.

### `ping`

```json
{
  "action": "ping",
  "peer": "wsl"
}
```

Optional timeout override:

```json
{
  "action": "ping",
  "peer": "wsl",
  "timeoutSeconds": 5
}
```

### `submit`

```json
{
  "action": "submit",
  "peer": "wsl",
  "message": "Summarize the latest logs."
}
```

`submit` waits for the remote task updates and returns the final task snapshot in structured form.

### `resume`

```json
{
  "action": "resume",
  "peer": "wsl",
  "taskId": "remote-20260407T002849577468142"
}
```

### `cancel`

```json
{
  "action": "cancel",
  "peer": "wsl",
  "taskId": "remote-20260407T002849577468142"
}
```

## Output Shape

Typical responses include:

- `status`: the full A2A status object
- `peers`: peer list plus the filter used
- `tasks`: task list plus filter and optional peer scope
- `pair`: the pairing payload
- `ping`: peer ID, success, latency, relay flag, and message
- `submit` / `resume`: final task snapshot plus task ID
- `cancel`: cancellation snapshot

## Behavior Notes

- The tool is designed for structured automation, not human-formatted summaries.
- If A2A is disabled or not ready, the tool returns the underlying runtime error.
- Relay-backed communication can still succeed immediately while GoClaw tries a background direct upgrade for future traffic.

## See Also

- [A2A Networking](../a2a.md) — User-facing A2A overview and configuration
- [Channel Commands](../commands.md) — Manual `/a2a` usage
- [Roles](../roles.md) — Owner-only tool access
