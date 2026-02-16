---
title: "Advanced"
description: "Advanced configuration, deployment, and troubleshooting"
section: "Advanced"
weight: 1
landing: true
---

# Advanced Topics

This section covers advanced configuration, deployment, and troubleshooting for GoClaw.

## Topics

### Deployment

| Topic | Description |
|-------|-------------|
| [Deployment](deployment.md) | Production deployment, systemd, Docker |
| [Sandbox](sandbox.md) | File sandboxing and bubblewrap |

### Operations

| Topic | Description |
|-------|-------------|
| [Metrics](metrics.md) | Prometheus metrics endpoint |
| [Troubleshooting](troubleshooting.md) | Common issues and solutions |

### Security

| Topic | Description |
|-------|-------------|
| [Roles](roles.md) | RBAC, authentication, user management |
| [Sandbox](sandbox.md) | Execution isolation |

---

## Supervisor Mode

GoClaw can run as a supervised daemon with automatic restart:

```bash
goclaw supervisor start
```

Features:
- **Process monitoring** — Spawns and monitors gateway subprocess
- **Crash recovery** — Exponential backoff restart (1s → 5min max)
- **State persistence** — Saves PID, crash count to `supervisor.json`
- **Output capture** — Circular buffer for crash diagnostics

### Commands

```bash
# Start supervised gateway
goclaw supervisor start

# Stop supervised gateway
goclaw supervisor stop

# View status
goclaw supervisor status
```

### Configuration

Supervisor reads from `goclaw.json`:
```json
{
  "gateway": {
    "pidFile": "goclaw.pid",
    "logFile": "goclaw.log"
  }
}
```

---

## Debug Logging

Enable verbose logging for troubleshooting:

```bash
# Debug level (-d)
./bin/goclaw gateway -d

# Trace level (-t) - very verbose
./bin/goclaw gateway -t

# Via make
make debug
```

### Log Levels

| Level | Flag | Use |
|-------|------|-----|
| `trace` | `-t` | Very verbose (cache hits, token counts) |
| `debug` | `-d` | Development details |
| `info` | (default) | Normal operation |
| `warn` | - | Potential issues |
| `error` | - | Errors only |

### Filtering Logs

```bash
# Only errors
./bin/goclaw gateway -d 2>&1 | grep -E "ERRO|error"

# Specific component
./bin/goclaw gateway -d 2>&1 | grep compaction

# Exclude noise
./bin/goclaw gateway -d 2>&1 | grep -v "TRAC"
```

---

## Request Tracing

Enable request dumps for API debugging:

```json
{
  "llm": {
    "registry": {
      "providers": {
        "claude": {
          "type": "anthropic",
          "trace": true,
          "dumpOnSuccess": true
        }
      }
    }
  }
}
```

Request dumps are saved to a temp directory with:
- Full request body
- Response body
- Timing information

---

## Database Management

### Location

SQLite database: `~/.goclaw/sessions.db`

### Inspection

```bash
# List tables
sqlite3 ~/.goclaw/sessions.db ".tables"

# Check integrity
sqlite3 ~/.goclaw/sessions.db "PRAGMA integrity_check"

# View session keys
sqlite3 ~/.goclaw/sessions.db "SELECT DISTINCT session_key FROM messages"
```

### Backup

```bash
cp ~/.goclaw/sessions.db ~/.goclaw/sessions.db.bak
```

### Recovery

If database is corrupted:
```bash
mv ~/.goclaw/sessions.db ~/.goclaw/sessions.db.corrupted
# GoClaw will create fresh database on restart
```

---

## OpenClaw Compatibility

GoClaw can run alongside OpenClaw with shared resources:

### Session Inheritance

```json
{
  "session": {
    "inherit": true,
    "inheritPath": "~/.openclaw/agents/main/sessions",
    "inheritFrom": "main"
  }
}
```

### Session Watcher

When inheritance is enabled, GoClaw monitors the OpenClaw session file for changes and injects new messages in real-time.

### Shared Directories

| Resource | GoClaw | OpenClaw |
|----------|--------|----------|
| Skills | `~/.goclaw/skills/` or `~/.openclaw/skills/` | `~/.openclaw/skills/` |
| Memory | Workspace `memory/` | Workspace `memory/` |
| Workspace | Configurable | `~/.openclaw/workspace/` |

---

## Performance Tuning

### Compaction

Aggressive compaction for constrained environments:
```json
{
  "session": {
    "summarization": {
      "compaction": {
        "reserveTokens": 20000,
        "maxMessages": 200,
        "keepPercent": 30
      }
    }
  }
}
```

### Prompt Cache

Reduce disk I/O with longer cache intervals:
```json
{
  "promptCache": {
    "pollInterval": 300
  }
}
```

### Embedding Model

Use faster embedding model for quick searches:
```json
{
  "memorySearch": {
    "ollama": {
      "model": "all-minilm"
    },
    "query": {
      "minScore": 0.4
    }
  }
}
```

---

## Extension Points

### Custom Tools

Tools are registered via the tool registry at startup. See `internal/tools/` for implementation examples.

### Custom Channels

Channels implement the channel interface. See `internal/telegram/` and `internal/http/` for examples.

### Skills

Add domain-specific capabilities via skills without code changes. See [Skills](skills.md).

---

## See Also

- [Deployment](deployment.md) — Production setup
- [Troubleshooting](troubleshooting.md) — Common issues
- [Sandbox](sandbox.md) — Execution isolation
- [Architecture](architecture.md) — System internals
