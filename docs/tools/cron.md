---
title: "Cron"
description: "Schedule tasks to run at specific times or intervals"
section: "Tools"
weight: 40
---

# Cron Tool

Schedule assistant tasks to run at specific times or intervals.

Each cron job now answers three separate questions:

1. What prompt should run?
2. What should happen to the result?
3. Should the result be persisted?

The result handling modes are:

- `store_only` - run the task and keep the result internal
- `deliver` - run the task and deliver the final assistant result to channels
- `handoff_main` - run the task, then pass the result into the main agent flow

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

Create a new scheduled assistant task.

```json
{
  "action": "add",
  "name": "morning-briefing",
  "description": "Daily morning briefing",
  "scheduleType": "cron",
  "cronExpr": "0 8 * * *",
  "timezone": "America/New_York",
  "prompt": "Give me a morning briefing: weather, calendar, news",
  "resultMode": "deliver"
}
```

| Parameter | Required | Description |
|-----------|----------|-------------|
| `name` | Yes | Job identifier |
| `description` | No | Human-readable description |
| `scheduleType` | Yes | `at`, `every`, or `cron` |
| `prompt` | Yes | Assistant task prompt to execute |
| `resultMode` | Yes | `store_only`, `deliver`, or `handoff_main` |
| `persist` | No | Persist result override. Omit to use smart defaults |
| `channel` | No | Optional future delivery hint for `deliver` mode |
| `to` | No | Optional future delivery target hint for `deliver` mode |
| `bestEffort` | No | Optional future delivery hint for `deliver` mode |
| `timeoutSeconds` | No | Optional per-job timeout override |
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
  "text": "Check for new emails",
  "mode": "now"
}
```

| Parameter | Description |
|-----------|-------------|
| `text` | Text to inject |
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

## Result Modes

| Mode | Description |
|------|-------------|
| `store_only` | Run the task and do not deliver the final result |
| `deliver` | Run the task and deliver the final assistant result to channels |
| `handoff_main` | Run the task first, then pass its result into the main agent flow |

All cron tasks run as scheduled assistant tasks. The runtime handles isolation and follow-up behavior based on the result mode rather than a persisted `sessionTarget` field.

## Persistence

By default:

- `store_only` persists the result
- `deliver` persists the result
- `handoff_main` persists the result

Set `"persist": false` to suppress persistence for noisy jobs such as threshold checks that usually produce nothing interesting.

## Legacy Migration

Older cron files that still use the legacy `payload` / `sessionTarget` schema are migrated automatically on load.

- A backup is written to `jobs.json.bak`
- The file is rewritten in native format
- Legacy `systemEvent` jobs are converted to `handoff_main`
- Malformed legacy jobs fail load loudly instead of being silently skipped

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
  "prompt": "Reminder: Take a break!",
  "resultMode": "deliver"
}
```

**Check emails every hour:**
```json
{
  "action": "add",
  "name": "email-check",
  "scheduleType": "every",
  "every": "1h",
  "prompt": "Check for important emails",
  "resultMode": "store_only",
  "persist": false
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
  "prompt": "Summarize today's calendar and pending tasks",
  "resultMode": "handoff_main"
}
```

---

## See Also

- [Tools](../tools.md) — Tool overview
- [Configuration](../configuration.md) — Full config reference
