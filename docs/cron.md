# Cron & Heartbeat

GoClaw includes a cron system for scheduled tasks and periodic heartbeat checks.

## Configuration

In `goclaw.json`:

```json
{
  "cron": {
    "enabled": true
  },
  "heartbeat": {
    "enabled": true,
    "interval_minutes": 30
  }
}
```

## Heartbeat

The heartbeat system periodically runs the agent with a prompt to check on things.

### How it works

1. Every `interval_minutes`, the cron service triggers a heartbeat
2. It reads `HEARTBEAT.md` from your workspace
3. If the file is empty (only comments/whitespace), the heartbeat is skipped (saves tokens)
4. Otherwise, the agent runs with the heartbeat prompt

### HEARTBEAT.md

Create `HEARTBEAT.md` in your workspace root with instructions for periodic checks:

```markdown
# HEARTBEAT.md

Check the driveway camera and report status.
```

**To disable heartbeat without changing config:** Empty the file or leave only comments:

```markdown
# HEARTBEAT.md
# Currently disabled - nothing to check
```

### Manual trigger

Use `/heartbeat` command in Telegram to trigger an immediate heartbeat check.

### Default prompt

If no custom prompt is configured:

```
Read HEARTBEAT.md if it exists (workspace context). Follow it strictly. 
Do not infer or repeat old tasks from prior chats. If nothing needs attention, 
reply HEARTBEAT_OK.
```

## Scheduled Jobs

Cron jobs are defined in `cron.json` in your workspace:

```json
{
  "jobs": [
    {
      "id": "morning-briefing",
      "schedule": "0 8 * * *",
      "prompt": "Good morning! Give me a weather update.",
      "enabled": true
    }
  ]
}
```

### Job fields

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Unique job identifier |
| `schedule` | string | Cron expression (minute hour day month weekday) |
| `prompt` | string | The prompt to send to the agent |
| `enabled` | bool | Whether the job is active |
| `deliver` | bool | If true, delivers response to owner's channels |
| `fresh_context` | bool | If true, runs with fresh session (no history) |

### Cron expressions

Standard 5-field cron format:

```
┌───────────── minute (0-59)
│ ┌───────────── hour (0-23)
│ │ ┌───────────── day of month (1-31)
│ │ │ ┌───────────── month (1-12)
│ │ │ │ ┌───────────── day of week (0-6, Sun=0)
│ │ │ │ │
* * * * *
```

Examples:
- `0 8 * * *` - Daily at 8:00 AM
- `*/30 * * * *` - Every 30 minutes
- `0 9 * * 1` - Every Monday at 9:00 AM
- `0 0 1 * *` - First day of each month at midnight

### Hot reload

Changes to `cron.json` are automatically detected and applied without restart.

## Message delivery from heartbeat/cron

When the agent runs during heartbeat or cron jobs, it can send messages/media using the `message` tool. Since there's no active chat session:

- **Text responses**: Automatically mirrored to all channels (telegram, http, tui)
- **Media/files**: Use `message` tool with `action="send"` and `filePath`. Omit `channel` to broadcast to all available channels.

Example agent usage:
```json
{
  "action": "send",
  "filePath": "/tmp/screenshot.png",
  "caption": "Camera snapshot"
}
```

This broadcasts to telegram (owner's chat) and HTTP (web UI if connected).
