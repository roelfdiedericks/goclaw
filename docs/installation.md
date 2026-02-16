# Installation

This guide covers installing GoClaw and getting it running on your system.

## Quick Install

### Download Binary (Recommended)

Download the latest release for your platform:

```bash
# Linux (amd64)
curl -LO https://github.com/roelfdiedericks/goclaw/releases/latest/download/goclaw-linux-amd64
chmod +x goclaw-linux-amd64
sudo mv goclaw-linux-amd64 /usr/local/bin/goclaw

# Linux (arm64)
curl -LO https://github.com/roelfdiedericks/goclaw/releases/latest/download/goclaw-linux-arm64
chmod +x goclaw-linux-arm64
sudo mv goclaw-linux-arm64 /usr/local/bin/goclaw
```

Verify the installation:

```bash
goclaw version
```

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

- **Go 1.22+** — [golang.org/dl](https://golang.org/dl/)
- **Git**
- **GCC** — Required for SQLite with FTS5 (full-text search)

```bash
# Ubuntu/Debian
sudo apt install build-essential

# Fedora
sudo dnf install gcc
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

# Install to PATH
sudo mv goclaw /usr/local/bin/
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

- 2 GB RAM (for local embeddings with Ollama)
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

GoClaw uses these directories:

```
~/.goclaw/
├── goclaw.json       # Main configuration
├── users.json        # User accounts and permissions
└── browser/          # Managed Chromium installation + profiles

~/.openclaw/
├── sessions.db       # SQLite database (sessions, transcripts, embeddings)
├── workspace/        # Agent workspace (SOUL.md, memory/, etc.)
└── openclaw.json     # OpenClaw config (if running side-by-side)
```

**Why two directories?**

- `~/.goclaw/` — GoClaw-specific configuration
- `~/.openclaw/` — Shared with OpenClaw for compatibility (sessions, workspace)

If you're running GoClaw standalone (no OpenClaw), you can configure it to use a different workspace path.

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

## Upgrading

### Binary Upgrade

```bash
# Download new version
curl -LO https://github.com/roelfdiedericks/goclaw/releases/latest/download/goclaw-linux-amd64
chmod +x goclaw-linux-amd64

# Stop running instance (if daemon mode)
goclaw stop

# Replace binary
sudo mv goclaw-linux-amd64 /usr/local/bin/goclaw

# Restart
goclaw start
```

### Source Upgrade

```bash
cd goclaw
git pull
make build
goclaw stop
sudo mv goclaw /usr/local/bin/
goclaw start
```

### Database Migrations

GoClaw automatically migrates the SQLite database on startup. No manual steps required.

---

## Uninstalling

```bash
# Stop daemon if running
goclaw stop

# Remove binary
sudo rm /usr/local/bin/goclaw

# Remove GoClaw configuration
rm -rf ~/.goclaw

# Remove shared data (WARNING: also removes OpenClaw data!)
# rm -rf ~/.openclaw
```

---

## Troubleshooting

### "goclaw: command not found"

Ensure `/usr/local/bin` is in your PATH:

```bash
echo $PATH | grep -q /usr/local/bin || echo 'export PATH=$PATH:/usr/local/bin' >> ~/.bashrc
source ~/.bashrc
```

### "Permission denied"

Make the binary executable:

```bash
chmod +x /usr/local/bin/goclaw
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
