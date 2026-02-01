# GoClaw

A Go rewrite of OpenClaw — clean, single binary, no npm nonsense.

## Status

🚧 **Early Development** — just scaffolding for now.

## Goals

- Single binary deployment
- Compatible with existing OpenClaw workspaces
- Layered config: reads `openclaw.json`, overrides with `goclaw.json`
- Run side-by-side with OpenClaw during development

## Project Structure

```
goclaw/
├── cmd/goclaw/          # Main entry point
├── internal/
│   ├── config/          # Configuration loading & merging
│   ├── gateway/         # HTTP gateway server
│   ├── llm/             # LLM provider clients (Anthropic)
│   ├── channel/         # Channel adapters
│   │   └── telegram/    # Telegram bot
│   └── tools/           # Tool implementations
├── go.mod
└── README.md
```

## Building

```bash
cd goclaw
go build -o goclaw ./cmd/goclaw
./goclaw version
```

## Configuration

GoClaw uses layered configuration:

1. **Base:** Reads `~/.openclaw/openclaw.json` for shared settings
2. **Override:** Reads `~/.openclaw/goclaw.json` for instance-specific settings

This allows running both OpenClaw and GoClaw from the same workspace with different ports/bots.

### Example goclaw.json

```json
{
  "gateway": {
    "port": 3378
  },
  "telegram": {
    "botToken": "YOUR_DEV_BOT_TOKEN",
    "allowedUsers": [123456789]
  }
}
```

## Roadmap

See `projects/goclaw.md` for the full plan.

- [ ] Basic project structure
- [ ] Config loading with openclaw.json merge
- [ ] Telegram bot connection
- [ ] Anthropic streaming client
- [ ] Core tools (exec, read, write, edit)
- [ ] Session/context management
- [ ] Cron scheduler
- [ ] Memory/embedding search
