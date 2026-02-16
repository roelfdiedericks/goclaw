---
title: "Deployment"
description: "Running GoClaw in production with systemd and containers"
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
goclaw gateway
# Or with full path:
~/.goclaw/bin/goclaw gateway
```

---

## Systemd Service

### Create Service File

```bash
sudo cat > /etc/systemd/system/goclaw.service << 'EOF'
[Unit]
Description=GoClaw AI Agent Gateway
After=network.target

[Service]
Type=simple
User=goclaw
Group=goclaw
WorkingDirectory=/home/goclaw
ExecStart=/home/goclaw/.goclaw/bin/goclaw gateway
Restart=always
RestartSec=5

# Logging
StandardOutput=journal
StandardError=journal

# Security
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=/home/goclaw/.goclaw /home/goclaw/.openclaw

[Install]
WantedBy=multi-user.target
EOF
```

**Note:** All configuration (API keys, tokens) must be in `goclaw.json`. GoClaw does not read environment variables at runtime.

### Enable and Start

```bash
sudo systemctl daemon-reload
sudo systemctl enable goclaw
sudo systemctl start goclaw

# Check status
sudo systemctl status goclaw

# View logs
sudo journalctl -u goclaw -f
```

---

## Docker

GoClaw images are published to GitHub Container Registry (GHCR).

### Quick Start

```bash
# Pull latest stable
docker pull ghcr.io/roelfdiedericks/goclaw:latest

# Or pull specific version
docker pull ghcr.io/roelfdiedericks/goclaw:0.1.0

# Or pull latest beta
docker pull ghcr.io/roelfdiedericks/goclaw:beta
```

Using the provided Docker Compose:

```bash
cd docker
docker-compose up
```

On first run, the container will:
1. Generate default `goclaw.json` and `users.json`
2. Create a random password for the owner account
3. Print the password and exit

Then:
1. Note the generated password from the output
2. Edit the config to add your API key (see below)
3. Run `docker-compose up -d` to start normally

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
| `~/.goclaw/media/` | Temporary media files |
| `./goclaw.json` | Configuration |
| `./users.json` | User authorization |

### Backup

Back up these files regularly:
```bash
# SQLite database (contains all conversation history)
cp ~/.goclaw/sessions.db backup/sessions-$(date +%Y%m%d).db

# Configuration
cp goclaw.json backup/
cp users.json backup/
```

---

## Monitoring

### Status Check

```bash
# Check if daemon is running (reads PID file)
goclaw status

# Or check systemd status
sudo systemctl status goclaw
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

GoClaw exposes metrics at `/metrics` (Prometheus format) and `/api/metrics` (JSON).

See [Metrics](metrics.md) for details.

---

## Security Considerations

For comprehensive security guidance, see the [Security](security.md) section.

### API Keys

- Never commit API keys to git
- Store secrets only in `goclaw.json` (not environment variables)
- Protect config with `chmod 0600`
- Rotate keys periodically

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
