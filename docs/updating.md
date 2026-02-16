---
title: "Updating"
description: "How to update GoClaw across different deployment contexts"
section: "Getting Started"
weight: 5
---

# Updating GoClaw

GoClaw supports self-updates across various deployment contexts.

## Self-Update Command

The simplest way to update:

```bash
goclaw update
```

This will:
1. Check GitHub Releases for a newer version
2. Download the appropriate binary for your platform
3. Verify the checksum
4. Replace the running binary
5. Restart GoClaw (if running as a daemon)

### Options

| Flag | Description |
|------|-------------|
| `--check` | Check for updates without installing |
| `--channel CHANNEL` | Update to a specific channel (stable, beta) |
| `--no-restart` | Update but don't restart |
| `--force` | Update even if already on latest version |

**Examples:**

```bash
# Check for updates
goclaw update --check

# Update to latest beta
goclaw update --channel beta

# Update without restarting (for manual control)
goclaw update --no-restart
```

**Note:** The `--channel` flag applies only to that specific update command. It does not persist. To stay on beta, specify `--channel beta` each time you update.

---

## Deployment Context Behavior

`goclaw update` adapts to how GoClaw was started:

### Foreground Mode (`goclaw gateway`)

- Binary is replaced in-place
- Process restarts automatically via `exec()`
- No downtime (seamless replacement)

### TUI Mode (`goclaw tui`)

- Binary is replaced
- TUI restarts with the new version

### Daemon Mode (`goclaw start`)

- Binary is replaced
- Daemon process is restarted
- PID file is updated

### Supervised (systemd)

- Binary is replaced
- GoClaw exits
- systemd restarts it automatically

**Note:** Ensure your systemd unit has `Restart=always`.

### Docker

- Binary replacement inside the container is not persistent
- The update will work for the running container, but will be lost on restart
- **Recommended:** Pull a new image instead:

```bash
docker pull ghcr.io/roelfdiedericks/goclaw:latest
docker-compose up -d
```

---

## System-Managed Installations (.deb)

If GoClaw was installed via a `.deb` package, the binary will be in `/usr/bin/goclaw`.

In this case, `goclaw update` will **not** self-update. Instead, it will display a warning to use your package manager.

### Update .deb Package

**One-liner (auto-detects latest version):**

```bash
VERSION=$(curl -s https://api.github.com/repos/roelfdiedericks/goclaw/releases/latest | grep tag_name | cut -d'"' -f4 | tr -d 'v') && \
curl -LO "https://github.com/roelfdiedericks/goclaw/releases/download/v${VERSION}/goclaw_${VERSION}_linux_amd64.deb" && \
sudo dpkg -i goclaw_${VERSION}_linux_amd64.deb
```

**Manual download:**

1. Go to [github.com/roelfdiedericks/goclaw/releases/latest](https://github.com/roelfdiedericks/goclaw/releases/latest)
2. Download the `.deb` file for your architecture
3. Install with: `sudo dpkg -i goclaw_*.deb`

This prevents conflicts between system packages and self-managed updates.

---

## Agent-Initiated Updates

Your agent can update GoClaw using the built-in `goclaw_update` tool:

```
Agent: "Update yourself to the latest version"
→ Calls goclaw_update tool
→ Shows changelog
→ Asks for confirmation
→ Performs update
```

The `goclaw_update` tool runs outside the agent sandbox, allowing it to perform privileged operations (binary replacement) that normal tools cannot.

---

## Update Channels

GoClaw supports multiple release channels:

| Channel | Description |
|---------|-------------|
| `stable` | Production-ready releases |
| `beta` | Pre-release testing |

**Stable** is the default. Beta releases may have breaking changes or experimental features.

---

## Checking Current Version

```bash
goclaw --version
```

Output includes:
- Version number
- Build date
- Git commit (if available)

---

## Rollback

If an update causes issues, you can reinstall a specific version:

```bash
curl -fsSL https://goclaw.org/install.sh | sh -s -- --version 0.1.0
```

Or download directly from GitHub Releases and replace the binary manually.

---

## See Also

- [Installation](installation.md) — Initial setup
- [Deployment](deployment.md) — Production deployment
