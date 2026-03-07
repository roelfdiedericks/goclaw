---
title: "Deployment"
description: "Running GoClaw in production with supervisor mode and containers"
section: "Advanced"
weight: 60
---

# Deployment

Guide for running GoClaw in production.

## Quick Start (Development)

```bash
# Build and run with debug logging
make debug

# Or build and run normally
make run
```

---

## Production Setup

### 1. Install Binary

Install via the one-line installer (recommended):

```bash
curl -fsSL https://goclaw.org/install.sh | sh
```

Or build from source:

```bash
make build
# Creates: ./goclaw
mkdir -p ~/.goclaw/bin
mv goclaw ~/.goclaw/bin/
```

### 2. Create Configuration

```bash
cp goclaw.example.json goclaw.json
# Edit with your settings
```

Required settings:
- `llm.providers.<name>.apiKey` — Your LLM provider API key
- `telegram.botToken` (if using Telegram)

### 3. Create Users File

```bash
cat > users.json << 'EOF'
{
  "users": [
    {
      "name": "Your Name",
      "role": "owner",
      "identities": [
        {"provider": "telegram", "id": "YOUR_USER_ID"}
      ]
    }
  ]
}
EOF
```

### 4. Run

```bash
# Start as background daemon (recommended)
goclaw start

# Or run in foreground
goclaw gateway
```

---

## Daemon Mode (Recommended)

GoClaw has a built-in daemon mode with supervisor that keeps the gateway running:

```bash
# Start as background daemon
goclaw start

# Check status
goclaw status

# Stop the daemon
goclaw stop
```

The daemon:
- Daemonizes and detaches from the terminal
- Runs a supervisor that automatically restarts the gateway on crash
- Writes PID file for process management
- Handles graceful shutdown on SIGINT/SIGTERM

### Foreground Mode

To run in the foreground (useful for debugging or containers):

```bash
goclaw gateway
```

This runs without daemonizing — useful when you want to see logs directly or when running inside Docker/containers.

---

## Docker

GoClaw images are published to GitHub Container Registry (GHCR).

### Quick Start

```bash
# Pull latest stable
docker pull ghcr.io/roelfdiedericks/goclaw:latest

# Or pull specific version
docker pull ghcr.io/roelfdiedericks/goclaw:0.1.0
```

Using the provided Docker Compose:

```bash
cd docker
docker-compose up
```

### First Run Options

On first run (no config exists), you have two options:

**Option 1: Interactive Setup Wizard (Recommended)**

The container will print instructions and exit. Run the wizard interactively:

```bash
docker exec -it goclaw goclaw setup
```

This walks you through LLM providers, channels, and user configuration.

**Option 2: Quick Start with Defaults**

Set `GOCLAW_QUICK_START=1` to auto-generate default configs:

```yaml
# In docker-compose.yml
environment:
  - GOCLAW_QUICK_START=1
```

This generates `goclaw.json` and `users.json` with a random password, then starts the gateway. Edit the config afterward to add your API key.

### Editing the Config

The config files are stored in a Docker volume. To edit them:

```bash
# Find where Docker stores the volume
docker volume inspect docker_goclaw-config

# Edit the config (path from above)
sudo vim /var/lib/docker/volumes/docker_goclaw-config/_data/goclaw.json
```

At minimum, replace `YOUR_ANTHROPIC_API_KEY` with your actual API key.

### Alternative: Pre-create Config Files

If you prefer to manage configs outside Docker:

```bash
# Generate configs locally (if goclaw is installed)
goclaw setup generate > goclaw.json
goclaw setup generate --users --with-password > users.json
```

Then mount them in docker-compose.yml:

```yaml
volumes:
  - ./goclaw.json:/home/goclaw/.goclaw/goclaw.json:ro
  - ./users.json:/home/goclaw/.goclaw/users.json:ro
```

### Docker Files Reference

The repository includes:

- `docker/Dockerfile` — Multi-stage build for minimal image
- `docker/docker-compose.yml` — Ready-to-use compose configuration
- `docker/entrypoint.sh` — Handles first-run setup

### View Logs

```bash
docker-compose logs -f goclaw
```

---

## Data Directories

| Path | Purpose |
|------|---------|
| `~/.goclaw/sessions.db` | SQLite session database |
| `~/.goclaw/memorygraph.db` | Memory Graph database |
| `~/.goclaw/whatsapp.db` | WhatsApp session (if enabled) |
| `~/.goclaw/media/` | Temporary media files |
| `~/.goclaw/stt/whisper/` | Whisper STT models |
| `./goclaw.json` | Configuration |
| `./users.json` | User authorization |

### Backup

Back up these files regularly:
```bash
# SQLite databases (conversation history, memory graph)
cp ~/.goclaw/sessions.db backup/sessions-$(date +%Y%m%d).db
cp ~/.goclaw/memorygraph.db backup/memorygraph-$(date +%Y%m%d).db

# Configuration
cp goclaw.json backup/
cp users.json backup/

# WhatsApp session (if using WhatsApp)
cp ~/.goclaw/whatsapp.db backup/whatsapp-$(date +%Y%m%d).db
```

---

## Monitoring

### Status Check

```bash
# Check if gateway is running (reads PID file)
goclaw status
```

### Logging

Enable debug or trace logging with flags:

```bash
# Debug logging
goclaw gateway -d

# Trace logging (very verbose)
goclaw gateway -t

# Or via make (during development)
make debug
```

### Metrics

GoClaw exposes metrics via the HTTP channel:
- `/metrics` — Web UI dashboard with auto-refresh
- `/api/metrics` — JSON snapshot for programmatic access

See [Metrics](metrics.md) for details.

---

## Security Considerations

For comprehensive security guidance, see the [Security](security.md) section.


See [Environment variables and secrets](security-envvars.md) for why GoClaw uses config-only secrets.

### Network

- Bind to localhost only (default) for local-only access
- Use firewall rules to limit access
- GoClaw is designed for trusted network environments

### File Access

- GoClaw has read/write access to the workspace
- Config files are excluded from agent tool access
- Consider running as unprivileged user
- See [Sandbox](sandbox.md) for execution isolation

### User Authorization

- Only authorized users can interact with the bot
- Review `users.json` regularly
- See [Roles](roles.md) for access control configuration

---

## See Also

- [Configuration](./configuration.md) - All config options
- [Troubleshooting](./troubleshooting.md) - Common issues
- [Architecture](./architecture.md) - System overview
