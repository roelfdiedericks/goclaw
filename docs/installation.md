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
- Install the binary to `~/.goclaw/bin/goclaw`
- Prefer an existing user-owned `bin` directory already on your `PATH`
- Otherwise add `~/.goclaw/bin` to the startup file for your current shell

**Options:**

```bash
# Install latest beta
curl -fsSL https://goclaw.org/install.sh | sh -s -- --channel beta

# Install specific version
curl -fsSL https://goclaw.org/install.sh | sh -s -- --version 0.1.1
```

Verify the installation:

```bash
goclaw version
```

The installer never relies on privileged locations like `/usr/local/bin`. It stays in user-owned directories on both Linux and macOS.

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

**What's included:**
- Dependencies: `bubblewrap` (sandboxing), `ffmpeg` (audio processing)
- Bundled Whisper model at `/usr/share/goclaw/stt/ggml-tiny.en.bin`

After installation, run `goclaw onboard` for guided first-time setup.

### Docker

```bash
docker pull ghcr.io/roelfdiedericks/goclaw:latest
```

**First run — Interactive setup (recommended):**

```bash
# Start container (will pause waiting for setup)
docker run -d --name goclaw \
  -p 1337:1337 \
  -v goclaw-data:/home/goclaw/.goclaw \
  ghcr.io/roelfdiedericks/goclaw:latest

# Run the setup wizard
docker exec -it goclaw goclaw onboard

# Restart to apply config
docker restart goclaw
```

**First run — Quick start with defaults:**

```bash
docker run -d --name goclaw \
  -p 1337:1337 \
  -v goclaw-data:/home/goclaw/.goclaw \
  -e GOCLAW_QUICK_START=1 \
  ghcr.io/roelfdiedericks/goclaw:latest
```

This generates default configs with a random password (shown in logs). You'll need to edit `goclaw.json` to add your API key.

**What's included:**
- Debian-based image with `ffmpeg` for audio processing
- Bundled Whisper model (`ggml-tiny.en.bin`) for local speech-to-text
- Non-root user (`goclaw`)

See [Deployment](deployment.md#docker) for Docker Compose setup.

### Windows (via WSL2)

GoClaw runs on Windows through WSL2 (Windows Subsystem for Linux).

**Automated installer:**

```powershell
# Run in PowerShell as Administrator
irm https://goclaw.org/install-windows.ps1 | iex
```

This will:
- Enable WSL2 features (may require restart)
- Install Debian WSL distribution
- Run the Linux installer inside WSL
- Create a "GoClaw" desktop shortcut
- Auto-install the optional `libwebkit2gtk-4.1-0` runtime inside WSL so the embedded web setup UI is more likely to work without extra manual steps

**Manual setup:**

1. Install WSL2: `wsl --install -d Debian`
2. Open Debian and run the Linux installer:
   ```bash
   curl -fsSL https://goclaw.org/install.sh | sh
   goclaw onboard
   ```

If you skip optional GUI dependencies or want to install them later, the embedded web setup UI on Debian/Ubuntu uses `libwebkit2gtk-4.1-0`.

### Run Setup Wizard

After installing the binary, run the interactive setup wizard:

```bash
goclaw onboard
```

If you already have a GoClaw configuration, use `goclaw setup edit` to adjust it or `goclaw setup` to let GoClaw choose between edit and wizard automatically.

The wizard will:

1. **Detect OpenClaw** — If found, offer to import settings (API keys, workspace, Telegram token)
2. **Set agent identity** — Configure name, emoji, and optional typing text
3. **Create workspace** — Set up your agent's home directory with default files
4. **Set up user** — Create your owner account with optional Telegram/WhatsApp IDs
5. **Configure channels** — Enable Telegram, HTTP, and WhatsApp as needed
6. **Configure provider** — Select and test your primary LLM provider
7. **Configure security** — Choose a sandbox preset (`Assistant`, `Permissive`, `Hardened`, or `Custom`) and skill source permissions
8. **Review settings** — Confirm and finish setup

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
- **C toolchain** — Required for SQLite with FTS5 and local `whisper.cpp`

```bash
# Debian/Ubuntu
sudo apt install build-essential

# macOS
xcode-select --install
brew install make
brew install cmake libomp
```

### Build Steps

```bash
# Clone the repository
git clone https://github.com/roelfdiedericks/goclaw.git
cd goclaw

# macOS only: use modern GNU Make from Homebrew
gmake deps

# Linux
make deps

# macOS only
gmake build

# Linux
make build

# Install to ~/.goclaw/bin/
mkdir -p ~/.goclaw/bin
mv goclaw ~/.goclaw/bin/

# Add to PATH (if not already)
echo 'export PATH="$PATH:$HOME/.goclaw/bin"' >> ~/.bashrc
source ~/.bashrc
```

For local development, run `goclaw setup` before `make debug` or `gmake debug`. The debug target expects a valid `goclaw.json`, `users.json`, and provider configuration to already exist.

On macOS, the system `make` is too old for this project. Use Homebrew GNU Make:

```bash
# One-off
gmake build
gmake debug

# Optional: make Homebrew GNU Make the default in your shell
export PATH="$(brew --prefix make)/libexec/gnubin:$PATH"
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

- Linux (amd64 or arm64) for packaged installs and releases
- macOS for source builds
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
| Audio processing | FFmpeg | `sudo apt install ffmpeg` |
| Browser automation | Chromium | Auto-downloaded on first use |
| Local embeddings (alternative) | Ollama | [ollama.ai](https://ollama.ai) |
| Local LLM | LM Studio | [lmstudio.ai](https://lmstudio.ai) |

**Note:** The `.deb` package and Docker image include `bubblewrap` and `ffmpeg` automatically.

GoClaw includes a built-in local embeddings provider (`hugot-local`) for semantic search. Ollama is optional if you want to use it as an alternative embeddings backend.

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

The **workspace** (where your agent's SOUL.md, memory/, etc. live) is configured separately and defaults to `~/.goclaw/workspace/`. If migrating from OpenClaw, the setup wizard can import your existing workspace path.

On startup, GoClaw also repairs missing stock workspace files and can refresh bundled identity templates such as `AGENTS.md`, `SOUL.md`, `USER.md`, `TOOLS.md`, `IDENTITY.md`, and `HEARTBEAT.md` when the workspace copy still matches a known stock version. Customized files are preserved and `BOOTSTRAP.md` is never auto-updated.

**OpenClaw compatibility:**

If running alongside OpenClaw, GoClaw can inherit sessions from `~/.openclaw/agents/main/sessions/`. This is configured via `session.inherit` in goclaw.json. The inherited sessions are merged into GoClaw's own session database.

---

## OpenClaw Users

If you have an existing OpenClaw installation, GoClaw can import your settings:

```bash
goclaw onboard
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

If you update with `--no-restart` while the daemon is already running, restart it manually so the new binary is actually used:

```bash
goclaw restart
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
cp goclaw ~/.goclaw/bin/
goclaw restart
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

GoClaw requires CGO for SQLite with FTS5 support, and source builds also expect local `whisper.cpp` libraries:

```bash
# Linux: ensure build tools are installed
sudo apt install build-essential

# macOS: ensure Command Line Tools and Homebrew deps are installed
xcode-select --install
brew install make
brew install cmake libomp

# macOS: use modern GNU Make
gmake deps
gmake build

# Linux
make deps
make build
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

Alternatively, skip the import and configure your workspace manually via `goclaw setup edit` → Gateway → Working Directory, or edit `~/.goclaw/goclaw.json`:

```json
{
  "gateway": {
    "workingDir": "/path/to/your/workspace"
  }
}
```

---

## Next Steps

- [Configuration](configuration.md) — Configure LLM providers, Telegram, and other settings
- [First Run](first-run.md) — Start GoClaw and verify everything works
