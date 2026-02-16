---
title: "First Run"
description: "Run GoClaw for the first time and verify your setup"
section: "Getting Started"
weight: 3
---

# First Run

This guide walks you through running GoClaw for the first time after installation.

## Prerequisites

- GoClaw binary installed (see [Installation](installation.md))
- Configuration file created (see [Configuration](configuration.md))
- At least one LLM provider configured

## Starting the Gateway

The gateway is the main GoClaw process that handles all channels and agent interactions.

```bash
# Start the gateway
goclaw gateway

# Or with debug logging
goclaw gateway -d

# Or with trace logging (very verbose)
goclaw gateway -t
```

## Interactive TUI

For a terminal-based chat interface:

```bash
goclaw gateway -i
```

This starts the gateway with an interactive TUI where you can chat directly with your agent.

## Verifying the Setup

Once running, you should see log output indicating:

1. Configuration loaded successfully
2. LLM provider connected
3. Channels started (Telegram, HTTP, etc.)

Example output:

```
INFO  gateway: starting
INFO  config: loaded goclaw.json
INFO  anthropic: initialized model=claude-sonnet-4-20250514
INFO  telegram: bot started username=MyAgentBot
INFO  http: listening port=3333
```

## Next Steps

- [Set up Telegram](telegram.md) for mobile access
- [Configure Home Assistant](home-assistant.md) for smart home control
- [Add scheduled tasks](cron.md) for automated actions

## Troubleshooting

If the gateway fails to start:

1. Check your configuration file syntax: `goclaw validate`
2. Verify API keys are set correctly
3. Check the [Troubleshooting](troubleshooting.md) guide
