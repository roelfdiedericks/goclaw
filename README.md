# GoClaw

A Go rewrite of OpenClaw — clean, single binary, no npm nonsense.

## Status

🚧 **Early Development**

## Goals

- Single binary deployment
- Compatible with existing OpenClaw workspaces
- Layered config: reads `openclaw.json`, overrides with `goclaw.json`
- Run side-by-side with OpenClaw during development

## Usage

```bash
# Build
go build -o goclaw ./cmd/goclaw

# Run in foreground (dev mode)
./goclaw gateway

# Run as daemon
./goclaw start
./goclaw status
./goclaw stop

# With debug logging
./goclaw -d gateway
```

## Project Structure

```
goclaw/
├── cmd/goclaw/          # CLI entry point
├── internal/
│   ├── config/          # Configuration loading & merging
│   ├── logging/         # Global logging (L_info, L_error, etc.)
│   ├── gateway/         # HTTP gateway server
│   ├── llm/             # LLM provider clients (Anthropic)
│   ├── channel/         # Channel adapters
│   │   └── telegram/    # Telegram bot
│   └── tools/           # Tool implementations
├── go.mod
├── README.md
└── ROADMAP.md           # Full project plan
```

## Configuration

Layered config — share settings with OpenClaw, override what's different:

1. **Base:** `~/.openclaw/openclaw.json` (shared settings)
2. **Override:** `~/.openclaw/goclaw.json` (goclaw-specific)

### Example goclaw.json

```json
{
  "gateway": {
    "port": 1337
  },
  "telegram": {
    "botToken": "YOUR_DEV_BOT_TOKEN",
    "allowedUsers": [123456789]
  }
}
```

## Progress

- [x] Project structure
- [x] Logging infrastructure (charmbracelet/log)
- [x] CLI with Kong (gateway, start, stop, status, version)
- [x] Daemon support (go-daemon)
- [ ] Config loading with openclaw.json merge
- [ ] Telegram bot connection
- [ ] Anthropic streaming client
- [ ] Core tools (exec, read, write, edit)
- [ ] Session/context management
- [ ] Cron scheduler

See [ROADMAP.md](ROADMAP.md) for the full plan.
