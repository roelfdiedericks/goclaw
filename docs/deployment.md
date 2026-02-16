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

### 1. Build Binary

```bash
make build
# Creates: bin/goclaw
```

### 2. Create Configuration

```bash
cp goclaw.example.json goclaw.json
# Edit with your settings
```

Required settings:
- `llm.apiKey` or `ANTHROPIC_API_KEY` env var
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
./bin/goclaw
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
WorkingDirectory=/opt/goclaw
ExecStart=/opt/goclaw/bin/goclaw
Restart=always
RestartSec=5

# Environment
Environment=ANTHROPIC_API_KEY=sk-ant-...
Environment=TELEGRAM_BOT_TOKEN=123456:ABC...

# Logging
StandardOutput=journal
StandardError=journal

# Security
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=/opt/goclaw /home/goclaw/.openclaw

[Install]
WantedBy=multi-user.target
EOF
```

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

### Dockerfile

```dockerfile
FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o bin/goclaw ./cmd/goclaw

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /app/bin/goclaw .
COPY goclaw.json .
COPY users.json .

EXPOSE 8080
CMD ["./goclaw"]
```

### Docker Compose

```yaml
version: '3.8'

services:
  goclaw:
    build: .
    restart: always
    ports:
      - "8080:8080"
    volumes:
      - ./workspace:/workspace
      - goclaw-data:/home/goclaw/.openclaw
    environment:
      - ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY}
      - TELEGRAM_BOT_TOKEN=${TELEGRAM_BOT_TOKEN}
    
  ollama:
    image: ollama/ollama
    restart: always
    volumes:
      - ollama-data:/root/.ollama
    # GPU support (uncomment if available)
    # deploy:
    #   resources:
    #     reservations:
    #       devices:
    #         - capabilities: [gpu]

volumes:
  goclaw-data:
  ollama-data:
```

### Run with Docker Compose

```bash
# Create .env file
cat > .env << 'EOF'
ANTHROPIC_API_KEY=sk-ant-...
TELEGRAM_BOT_TOKEN=123456:ABC...
EOF

# Start
docker-compose up -d

# View logs
docker-compose logs -f goclaw
```

---

## Environment Variables

| Variable | Description |
|----------|-------------|
| `ANTHROPIC_API_KEY` | Anthropic API key |
| `TELEGRAM_BOT_TOKEN` | Telegram bot token |
| `BRAVE_API_KEY` | Brave Search API key |

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

## Ollama Setup

GoClaw uses Ollama for:
- Compaction/checkpoint summaries
- Memory embeddings

### Install Ollama

```bash
curl -fsSL https://ollama.com/install.sh | sh
```

### Pull Required Models

```bash
# For summaries (choose one)
ollama pull qwen2.5:7b      # Good balance
ollama pull qwen2.5:14b     # Better quality
ollama pull llama3.2:3b     # Faster, smaller

# For embeddings
ollama pull nomic-embed-text
```

### Configure GoClaw

```json
{
  "session": {
    "compaction": {
      "ollama": {
        "url": "http://localhost:11434",
        "model": "qwen2.5:7b"
      }
    }
  },
  "memorySearch": {
    "enabled": true,
    "ollama": {
      "url": "http://localhost:11434",
      "model": "nomic-embed-text"
    }
  }
}
```

### Remote Ollama

If running Ollama on a different machine:

```json
{
  "session": {
    "compaction": {
      "ollama": {
        "url": "http://192.168.1.100:11434",
        "model": "qwen2.5:7b"
      }
    }
  }
}
```

Ensure Ollama is listening on all interfaces:
```bash
OLLAMA_HOST=0.0.0.0:11434 ollama serve
```

---

## Monitoring

### Status Check

```bash
# Check session status
curl http://localhost:8080/api/status
```

### Logging

Enable debug or trace logging with flags:

```bash
# Debug logging
./bin/goclaw gateway -d

# Trace logging (very verbose)
./bin/goclaw gateway -t

# Or via make
make debug
```

### Metrics

GoClaw exposes metrics at `/metrics` (Prometheus format) and `/api/metrics` (JSON).

See [Metrics](metrics.md) for details.

---

## Security Considerations

### API Keys

- Never commit API keys to git
- Use environment variables or secure secret management
- Rotate keys periodically

### Network

- Run behind a reverse proxy (nginx, caddy) for HTTPS
- Limit network access to trusted IPs
- Use firewall rules

### File Access

- GoClaw has read/write access to the workspace
- Be careful what files are accessible
- Consider running as unprivileged user

### User Authorization

- Only authorized users can interact with the bot
- Review `users.json` regularly
- Implement rate limiting for public-facing deployments

---

## Reverse Proxy (Nginx)

```nginx
server {
    listen 443 ssl http2;
    server_name goclaw.example.com;

    ssl_certificate /etc/letsencrypt/live/goclaw.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/goclaw.example.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

---

## See Also

- [Configuration](./configuration.md) - All config options
- [Troubleshooting](./troubleshooting.md) - Common issues
- [Architecture](./architecture.md) - System overview
