---
title: "Sandboxing"
description: "Security layers protecting your system from agent actions"
section: "Advanced"
weight: 50
---

# Sandboxing

GoClaw implements multiple layers of sandboxing to protect your system from unintended or malicious actions by the agent.

## Overview

| Layer | Scope | Mechanism | Default |
|-------|-------|-----------|---------|
| File Tools | read, write, edit | Path validation | Always on |
| Exec Tool | Shell commands | bubblewrap (Linux) | Off |
| Browser | Chromium | bubblewrap (Linux) | Off |

## File Tools Sandbox

The `read`, `write`, and `edit` tools validate all paths to restrict file access.

### Protections

- **Workspace Containment** — All paths must resolve within the workspace
- **Symlink Prevention** — Symlinks in paths are rejected to prevent escapes
- **Denied Files** — Sensitive files are always blocked, even within the workspace
- **Unicode Normalization** — Unicode space characters normalized to prevent confusion attacks

### Denied Files

These files are blocked to protect credentials:

- `users.json` — User credentials and hashes
- `goclaw.json` — API keys and tokens
- `openclaw.json` — API keys and tokens
- `.env` — Environment secrets

### User Override

Users with `sandbox: false` in `users.json` bypass path validation:

```json
{
  "name": "Admin",
  "role": "owner",
  "sandbox": false
}
```

**Warning**: This allows the agent to access any file the GoClaw process can read/write.

## Bubblewrap Sandbox

The exec tool and browser can use [bubblewrap](https://github.com/containers/bubblewrap) (`bwrap`) for kernel-level isolation on Linux.

### Prerequisites

Install bubblewrap:

```bash
# Debian/Ubuntu
sudo apt install bubblewrap

# Arch
sudo pacman -S bubblewrap
```

The Debian package and Docker images include bubblewrap.

### Sandbox Modes

GoClaw supports three sandbox modes:

| Mode | Description | Use Case |
|------|-------------|----------|
| `home` | Full isolated home directory | Recommended default |
| `volumes` | Only specific directories persist | Controlled persistence |
| `ephemeral` | Nothing persists between runs | Maximum security |

### Configuration

```json
{
  "sandbox": {
    "bubblewrap": {
      "mode": "home",
      "path": "",
      "dataDir": "",
      "volumes": [],
      "extraPaths": []
    }
  }
}
```

| Option | Default | Description |
|--------|---------|-------------|
| `mode` | `home` | Sandbox mode: `home`, `volumes`, or `ephemeral` |
| `path` | (search PATH) | Custom path to bwrap binary |
| `dataDir` | `~/.goclaw/sandbox` | Backing storage for isolated directories |
| `volumes` | `~/.local`, `~/.config`, `~/.cache` | Directories to persist (volumes mode only) |
| `extraPaths` | [] | Additional PATH entries inside sandbox |

### Home Mode (Recommended)

The default mode creates a full isolated home directory at `~/.goclaw/sandbox/home/`. Agent-installed tools persist across runs, but are isolated from your real home.

```json
{
  "sandbox": {
    "bubblewrap": {
      "mode": "home"
    }
  }
}
```

### Volumes Mode

Only specific directories persist. Useful when you want controlled isolation:

```json
{
  "sandbox": {
    "bubblewrap": {
      "mode": "volumes",
      "volumes": ["~/.local", "~/.config", "~/.npm-global"]
    }
  }
}
```

### Ephemeral Mode

Nothing persists between runs. Maximum security but agent-installed tools are lost:

```json
{
  "sandbox": {
    "bubblewrap": {
      "mode": "ephemeral"
    }
  }
}
```

### What Commands Can Access

Inside the sandbox:

| Path | Access | Notes |
|------|--------|-------|
| Workspace | Read/Write | Agent's working directory |
| Isolated home | Read/Write | Based on mode |
| `/usr`, `/lib`, `/bin`, `/sbin` | Read-only | System binaries |
| `/etc/resolv.conf`, `/etc/hosts` | Read-only | Network config |
| `/etc/ssl`, `/etc/ca-certificates` | Read-only | SSL certificates |
| `/tmp` | Read/Write | Isolated tmpfs |
| `/proc` | Read-only | Process information |

### What Commands Cannot Access

- Your real home directory (except workspace)
- Other users' files
- Host environment variables (API keys, tokens)
- System configuration outside allowed paths

### PATH Inside Sandbox

The sandbox automatically includes common user binary directories:

- `~/.local/bin`
- `~/.npm-global/bin`
- `~/go/bin`
- `~/.cargo/bin`
- `~/.bun/bin`
- `~/pip-tools/bin`

Add custom paths via `extraPaths`:

```json
{
  "sandbox": {
    "bubblewrap": {
      "extraPaths": ["/opt/mytools/bin", "~/.custom/bin"]
    }
  }
}
```

### Enabling via Setup

Run `goclaw setup edit` and select "Sandbox" to configure modes and options.

Or during initial setup, the wizard detects bwrap and offers to enable sandboxing.

## Security Considerations

### Defense in Depth

The layers complement each other:

1. **File tools sandbox** — Prevents direct file access outside workspace
2. **Bubblewrap sandbox** — Prevents shell commands from escaping
3. **Isolated home** — Agent tools don't mix with your real environment

### What Sandboxing Does NOT Protect Against

- Network-based attacks (network access is allowed)
- Side-channel attacks
- Bugs in bubblewrap itself
- Actions within the workspace (agent can still modify workspace files)

### Recommendations

1. **Use `home` mode** for general use — good balance of security and usability
2. **Use `ephemeral` mode** for untrusted prompts — nothing persists
3. **Use `sandbox: false`** sparingly — only for trusted admin users
4. **Review `extraPaths`** — they become accessible inside sandbox

## Troubleshooting

### "bwrap not found"

Install bubblewrap (see Prerequisites above).

### "namespace operation not permitted"

Some container environments restrict namespace creation:

1. Run GoClaw outside the container
2. Use `--privileged` flag with Docker
3. Use a different sandbox mode or disable

### Commands fail inside sandbox

The command may need paths not available. Add them to `extraPaths`:

```json
{
  "sandbox": {
    "bubblewrap": {
      "extraPaths": ["/opt/mytools"]
    }
  }
}
```

### Agent-installed tools not found

In `ephemeral` mode, tools don't persist. Switch to `home` or `volumes` mode.

In `volumes` mode, ensure the tool's install location is in your volumes list.

---

## See Also

- [Configuration](configuration.md) — Full config reference
- [Tools](tools.md) — Tool documentation
- [Deployment](deployment.md) — Production setup
