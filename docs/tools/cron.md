---
title: "Cron"
description: "Schedule tasks to run at specific times or intervals"
section: "Tools"
weight: 40
---

# Cron Tool

Schedule tasks to run at specific times or intervals.

## Actions

### status

Get cron service status.

```json
{
  "action": "status"
}
```

### list

List all jobs with full details.

```json
{
  "action": "list"
}
```

### add

Create a new scheduled job.

```json
{
  "action": "add",
  "name": "morning-briefing",
  "description": "Daily morning briefing",
  "scheduleType": "cron",
  "cronExpr": "0 8 * * *",
  "timezone": "America/New_York",
  "sessionTarget": "main",
  "message": "Give me a morning briefing: weather, calendar, news",
  "deliver": true
}
```

| Parameter | Required | Description |
|-----------|----------|-------------|
| `name` | Yes | Job identifier |
| `description` | No | Human-readable description |
| `scheduleType` | Yes | `at`, `every`, or `cron` |
| `sessionTarget` | No | `main` (with context) or `isolated` (fresh) |
| `message` | Yes | Prompt to execute |
| `deliver` | No | Deliver output to channels |
| `enabled` | No | Enable job (default: true) |

### update

Modify an existing job.

```json
{
  "action": "update",
  "id": "abc123",
  "enabled": false
}
```

### remove

Delete a job.

```json
{
  "action": "remove",
  "id": "abc123"
}
```

### run

Execute a job immediately.

```json
{
  "action": "run",
  "id": "abc123"
}
```

### runs

View job execution history.

```json
{
  "action": "runs",
  "id": "abc123"
}
```

### kill

Clear stuck running state.

```json
{
  "action": "kill",
  "id": "abc123"
}
```

### wake

Send a wake event to inject text or trigger heartbeat.

```json
{
  "action": "wake",
  "message": "Check for new emails",
  "mode": "now"
}
```

| Parameter | Description |
|-----------|-------------|
| `message` | Text to inject |
| `mode` | `now` or `next-heartbeat` |

## Schedule Types

### at — One-shot

Run once at a specific time.

```json
{
  "scheduleType": "at",
  "at": "+5m"
}
```

Formats:
- Unix milliseconds: `1703275200000`
- ISO 8601: `2024-12-22T15:00:00Z`
- Relative: `+5m`, `+2h`, `+1d`

### every — Interval

Run repeatedly at intervals.

```json
{
  "scheduleType": "every",
  "every": "30m"
}
```

Formats: `30s`, `5m`, `2h`, `1d`

### cron — Cron expression

Standard 5-field cron expression.

```json
{
  "scheduleType": "cron",
  "cronExpr": "0 9 * * 1-5",
  "timezone": "America/New_York"
}
```

Format: `minute hour day month weekday`

Examples:
- `0 9 * * *` — 9 AM daily
- `0 9 * * 1-5` — 9 AM weekdays
- `*/15 * * * *` — Every 15 minutes
- `0 0 1 * *` — First of month at midnight

## Session Targets

| Target | Description |
|--------|-------------|
| `main` | Runs in primary session with conversation history |
| `isolated` | Runs in fresh session without context |

Use `main` for tasks that need prior context. Use `isolated` for standalone tasks.

## Configuration

```json
{
  "cron": {
    "enabled": true,
    "jobTimeoutMinutes": 30,
    "heartbeat": {
      "enabled": true,
      "interval": "30m",
      "prompt": "Check HEARTBEAT.md for tasks"
    }
  }
}
```

| Option | Default | Description |
|--------|---------|-------------|
| `enabled` | true | Enable cron scheduler |
| `jobTimeoutMinutes` | 30 | Job execution timeout (0 = none) |
| `heartbeat.enabled` | true | Enable heartbeat polling |
| `heartbeat.interval` | 30m | Heartbeat check interval |
| `heartbeat.prompt` | - | Custom heartbeat prompt |

## Examples

**Remind me in 20 minutes:**
```json
{
  "action": "add",
  "name": "reminder",
  "scheduleType": "at",
  "at": "+20m",
  "message": "Reminder: Take a break!",
  "deliver": true
}
```

**Check emails every hour:**
```json
{
  "action": "add",
  "name": "email-check",
  "scheduleType": "every",
  "every": "1h",
  "sessionTarget": "isolated",
  "message": "Check for important emails"
}
```

**Daily standup summary:**
```json
{
  "action": "add",
  "name": "standup",
  "scheduleType": "cron",
  "cronExpr": "0 9 * * 1-5",
  "timezone": "America/New_York",
  "message": "Summarize today's calendar and pending tasks"
}
```

---

## See Also

- [Tools](../tools.md) — Tool overview
- [Configuration](../configuration.md) — Full config reference
