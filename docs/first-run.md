---
title: "First Run"
description: "Your first 5 minutes with GoClaw after installation"
section: "Getting Started"
weight: 3
---

# First Run

This guide walks you through your first 5 minutes with GoClaw after installation.

## After Installation

If you installed via the one-line installer:

```bash
curl -fsSL https://goclaw.org/install.sh | sh
```

You now have:
- `goclaw` binary in `~/.goclaw/bin/`
- PATH updated (restart your shell or run `source ~/.bashrc`)

Verify the installation:

```bash
goclaw version
```

## Setup Wizard

Run the interactive setup wizard:

```bash
goclaw onboard
```

The wizard walks you through:

1. **OpenClaw Detection** — If found, offers to import settings (API keys, workspace, Telegram token)
2. **Agent Identity** — Set agent name, emoji, and optional typing text
3. **Workspace** — Creates your agent's home directory with identity files
4. **User Account** — Create your owner account with optional Telegram/WhatsApp IDs
5. **Channels** — Enable Telegram, HTTP, WhatsApp as needed
6. **LLM Provider** — Configure and test your primary model provider
7. **Voice Settings (optional)** — Configure speech-to-text and voice LLM
8. **Security & Skills** — Choose a security preset (`Assistant`, `Permissive`, `Hardened`, or `Custom`) and skill source permissions
9. **Review & Finish** — Confirm settings and write config

After setup completes, you'll have:
- `~/.goclaw/goclaw.json` — Main configuration
- `~/.goclaw/users.json` — User accounts
- `~/.goclaw/workspace/` — Agent workspace with identity files

## Starting GoClaw

### Interactive TUI (Recommended for First Run)

```bash
goclaw tui
```

Opens an interactive terminal chat. You can immediately start talking to your agent. Press `Ctrl+C` to exit.

### Background Daemon

```bash
goclaw start
```

Runs GoClaw as a background daemon with automatic restart on crash. Use this for production.

```bash
goclaw status   # Check if running
goclaw restart  # Restart after updates or config changes
goclaw stop     # Stop the daemon
```

### Foreground Mode

```bash
goclaw gateway
```

Runs in the foreground with logs to terminal. Useful for debugging.

```bash
goclaw gateway -d   # Debug logging
goclaw gateway -t   # Trace logging (very verbose)
```

## Verifying It Works

On successful startup, you'll see:

```
INFO  gateway: starting
INFO  config: loaded goclaw.json
INFO  llm: registry created providers=1 agent_models=1
INFO  anthropic: initialized model=claude-sonnet-4-20250514
INFO  telegram: bot started username=MyAgentBot
INFO  http: listening addr=:1337
```

Key indicators:
- LLM provider initialized with your model
- Channels started (Telegram, HTTP, etc.)
- No error messages

## Quick Test

### Via TUI

If using `goclaw tui`, just type a message:

```
You: Hello! What can you do?
```

### Via Telegram

If Telegram is configured, message your bot. It should respond within a few seconds.

### Via Web UI

If HTTP is enabled, open your browser:

```
http://localhost:1337/chat
```

## Common First-Run Issues

### "no providers configured"

Run `goclaw onboard` to configure at least one LLM provider.

### "failed to authenticate" (Telegram)

Check your bot token in `goclaw.json`. Get a new token from [@BotFather](https://t.me/BotFather).

### "connection refused" (Ollama)

Ensure Ollama is running: `ollama serve`

### "invalid API key"

Double-check your API key in `goclaw.json`. Keys are stored in `llm.providers.<name>.apiKey`.

## Re-running Setup

You can re-run the wizard anytime:

```bash
goclaw onboard            # Friendly first-run / guided setup path
goclaw setup              # Auto-detect: edit if config exists, wizard if new
goclaw setup wizard       # Force full wizard (re-walk all steps)
goclaw setup edit         # Edit existing config (menu-based)
```

---

## Next Steps

- [Telegram Setup](telegram.md) — Configure your Telegram bot
- [WhatsApp Setup](whatsapp.md) — Link your WhatsApp account
- [Web UI](web-ui.md) — Access the HTTP interface
- [Commands](commands.md) — Slash commands reference
- [Tools](tools.md) — Available agent tools
- [Configuration](configuration.md) — Full config reference
