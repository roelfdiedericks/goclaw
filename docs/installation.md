---
title: "Installation"
description: "Install GoClaw from binary releases or build from source"
section: "Getting Started"
weight: 1
landing: true
---

# Installation

This guide covers installing GoClaw and getting it running on your system.

## Quick Install

### One-Line Installer (Recommended)

```bash
curl -fsSL https://goclaw.org/install.sh | sh
```

This will:
- Download the latest stable release for your platform
- Verify the checksum
- Install to `~/.goclaw/bin/goclaw`
- Add to your PATH (via symlink or shell config)

**Options:**

```bash
# Install latest beta
curl -fsSL https://goclaw.org/install.sh | sh -s -- --channel beta

# Install specific version
curl -fsSL https://goclaw.org/install.sh | sh -s -- --version 0.2.0
```

Verify the installation:

```bash
goclaw --version
```

### Install from Debian Package

**One-liner (auto-detects latest version):**

```bash
VERSION=$(curl -s https://api.github.com/repos/roelfdiedericks/goclaw/releases/latest | grep tag_name | cut -d'"' -f4 | tr -d 'v') && \
curl -LO "https://github.com/roelfdiedericks/goclaw/releases/download/v${VERSION}/goclaw_${VERSION}_linux_amd64.deb" && \
sudo dpkg -i goclaw_${VERSION}_linux_amd64.deb
```

**Manual download:**

1. Go to [github.com/roelfdiedericks/goclaw/releases/latest](https://github.com/roelfdiedericks/goclaw/releases/latest)
2. Download the `.deb` file for your architecture (`amd64` or `arm64`)
3. Install with: `sudo dpkg -i goclaw_*.deb`

### Docker

```bash
docker pull ghcr.io/roelfdiedericks/goclaw:latest
```

See [Deployment](deployment.md#docker) for Docker Compose setup.

### Run Setup Wizard

After installing the binary, run the interactive setup wizard:

```bash
goclaw setup
```

The wizard will:

1. **Detect OpenClaw** — If found, offer to import settings (API keys, workspace, Telegram token)
2. **Create workspace** — Set up your agent's home directory with default files
3. **Configure providers** — Select and test LLM providers (Anthropic, Ollama, LM Studio)
4. **Set up user** — Create your owner account with optional Telegram ID
5. **Test connections** — Validate API keys and fetch available models

After setup completes, you're ready to start GoClaw:

```bash
goclaw tui           # Interactive TUI mode (recommended for first run)
goclaw gateway       # Foreground mode (logs to terminal)
goclaw start         # Daemon mode (background)
```

---

## Building from Source

### Prerequisites

- **Go 1.25+** — [golang.org/dl](https://golang.org/dl/)
- **Git**
- **GCC** — Required for SQLite with FTS5 (full-text search)

```bash
# Debian/Ubuntu
sudo apt install build-essential
```

### Build Steps

```bash
# Clone the repository
git clone https://github.com/roelfdiedericks/goclaw.git
cd goclaw

# Build (uses Makefile with correct CGO flags)
make build

# Or build directly with Go (ensure CGO flags are set)
CGO_CFLAGS="-DSQLITE_ENABLE_FTS5" go build -o goclaw ./cmd/goclaw

# Install to ~/.goclaw/bin/
mkdir -p ~/.goclaw/bin
mv goclaw ~/.goclaw/bin/

# Add to PATH (if not already)
echo 'export PATH="$PATH:$HOME/.goclaw/bin"' >> ~/.bashrc
source ~/.bashrc
```

### Makefile Targets

| Target | Description |
|--------|-------------|
| `make build` | Build the binary |
| `make run` | Build and run gateway |
| `make tui` | Build and run with TUI + debug logging |
| `make debug` | Build and run with debug + dev mode |
| `make test` | Run tests |
| `make lint` | Run linter (installs golangci-lint if needed) |
| `make audit` | Run linter + vulnerability check |

---

## System Requirements

### Minimum

- Linux (amd64 or arm64)
- 512 MB RAM
- 100 MB disk space

### Recommended

- 2 GB RAM
- 1 GB disk space (for browser profiles and session storage)
- Bubblewrap installed (for sandboxed command execution)

### Optional Dependencies

GoClaw is a single static binary. These are optional but enhance functionality:

| Feature | Dependency | Installation |
|---------|------------|--------------|
| Sandboxed exec | Bubblewrap | `sudo apt install bubblewrap` |
| Browser automation | Chromium | Auto-downloaded on first use |
| Local embeddings | Ollama | [ollama.ai](https://ollama.ai) |
| Local LLM | LM Studio | [lmstudio.ai](https://lmstudio.ai) |

---

## Directory Structure

GoClaw stores all its data in `~/.goclaw/`:

```
~/.goclaw/
├── bin/
│   └── goclaw        # GoClaw binary (when installed via installer)
├── goclaw.json       # Main configuration
├── users.json        # User accounts and permissions
├── sessions.db       # SQLite database (sessions, transcripts, embeddings)
├── memory.db         # Memory search embeddings
└── browser/          # Managed Chromium installation + profiles
```

The **workspace** (where your agent's SOUL.md, memory/, etc. live) is configured separately and defaults to `~/.openclaw/workspace/` for compatibility with OpenClaw.

**OpenClaw compatibility:**

If running alongside OpenClaw, GoClaw can inherit sessions from `~/.openclaw/agents/main/sessions/`. This is configured via `session.inherit` in goclaw.json. The inherited sessions are merged into GoClaw's own session database.

---

## OpenClaw Users

If you have an existing OpenClaw installation, GoClaw can import your settings:

```bash
goclaw setup
# Select "Import from OpenClaw" when prompted
```

**What's imported:**

| From OpenClaw | To GoClaw |
|---------------|-----------|
| `agents.defaults.workspace` | Workspace path |
| `channels.telegram.botToken` | Telegram bot token |
| `tools.web.search.apiKey` | Brave search API key |
| `browser.*` | Browser tool settings |
| `auth-profiles.json` → Anthropic key | LLM API key |

**Not imported** (configure manually):
- Ollama settings
- LM Studio / OpenAI-compatible endpoints
- Embedding provider configuration

After import, `~/.goclaw/goclaw.json` becomes the authoritative config.

---

## Updating

### Self-Update (Recommended)

GoClaw can update itself:

```bash
goclaw update
```

This will:
- Check for new releases on GitHub
- Download and verify the new binary
- Replace itself and restart

**Options:**

```bash
goclaw update --check       # Check for updates without installing
goclaw update --channel beta  # Update to latest beta release
goclaw update --no-restart  # Update but don't restart (for manual control)
```

**Note:** If GoClaw was installed via a system package manager (e.g., dpkg, apt), `goclaw update` will warn you to use your package manager instead. This prevents conflicts with system-managed installations.

### Updating via Package Manager

For Debian/Ubuntu users who installed via `.deb`:

```bash
VERSION=$(curl -s https://api.github.com/repos/roelfdiedericks/goclaw/releases/latest | grep tag_name | cut -d'"' -f4 | tr -d 'v') && \
curl -LO "https://github.com/roelfdiedericks/goclaw/releases/download/v${VERSION}/goclaw_${VERSION}_linux_amd64.deb" && \
sudo dpkg -i goclaw_${VERSION}_linux_amd64.deb
```

### Source Upgrade

```bash
cd goclaw
git pull
make build
goclaw stop
cp goclaw ~/.goclaw/bin/
goclaw start
```

### Docker Updates

```bash
docker pull ghcr.io/roelfdiedericks/goclaw:latest
docker-compose up -d
```

### Database Migrations

GoClaw automatically migrates the SQLite database on startup. No manual steps required.

---

## Uninstalling

```bash
# Stop daemon if running
goclaw stop

# Remove binary and data directory
rm -rf ~/.goclaw

# If installed via symlink, remove it
rm -f ~/.local/bin/goclaw

# If installed via .deb package
sudo dpkg -r goclaw

# Remove shared data (WARNING: also removes OpenClaw data!)
# rm -rf ~/.openclaw
```

---

## Troubleshooting

### "goclaw: command not found"

If you used the installer, ensure your PATH includes `~/.goclaw/bin`:

```bash
# Check if in PATH
echo $PATH | tr ':' '\n' | grep goclaw

# If not, add it (or re-run installer)
echo 'export PATH="$PATH:$HOME/.goclaw/bin"' >> ~/.bashrc
source ~/.bashrc
```

On Linux, the installer tries to symlink to `~/.local/bin` if it's in your PATH. Check if the symlink exists:

```bash
ls -la ~/.local/bin/goclaw
```

### "Permission denied"

Make the binary executable:

```bash
chmod +x ~/.goclaw/bin/goclaw
```

### CGO / SQLite build errors

GoClaw requires CGO for SQLite with FTS5 support:

```bash
# Ensure GCC is installed
sudo apt install build-essential

# Build with explicit CGO flags
CGO_ENABLED=1 CGO_CFLAGS="-DSQLITE_ENABLE_FTS5" make build
```

### Bubblewrap sandbox fails

Some systems need unprivileged user namespaces enabled:

```bash
# Test bubblewrap
bwrap --ro-bind / / /bin/true

# If it fails, enable unprivileged namespaces
sudo sysctl -w kernel.unprivileged_userns_clone=1

# Make persistent
echo 'kernel.unprivileged_userns_clone=1' | sudo tee /etc/sysctl.d/99-userns.conf
```

See [Sandbox & Security](sandbox.md) for details on sandboxing options.

### Setup wizard fails to detect OpenClaw

Ensure OpenClaw config exists at the expected path:

```bash
ls ~/.openclaw/openclaw.json
ls ~/.openclaw/agents/main/agent/auth-profiles.json
```

---

## Next Steps

- [Configuration](configuration.md) — Configure LLM providers, Telegram, and other settings
- [First Run](first-run.md) — Start GoClaw and verify everything works
