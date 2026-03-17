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
| Exec Tool | Shell commands | Managed OS sandbox backend | Off |
| Browser | Chromium | Managed OS sandbox backend | Off |

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

## Managed Exec/Browser Sandbox

GoClaw can sandbox the `exec` tool and managed browser launches using the platform backend:

- Linux: [bubblewrap](https://github.com/containers/bubblewrap) (`bwrap`)
- macOS: `sandbox-exec` (Seatbelt)

### Prerequisites

Linux (bubblewrap):

```bash
# Debian/Ubuntu
sudo apt install bubblewrap

# Arch
sudo pacman -S bubblewrap
```

macOS uses the system `sandbox-exec` binary.

### Sandbox Modes

GoClaw supports explicit modes. Availability depends on platform backend:

| Mode | Linux (bubblewrap) | macOS (seatbelt) | Description |
|------|---------------------|------------------|-------------|
| `home` | Yes | Yes | Linux: full isolated sandbox home; macOS: policy-managed real home (dot dirs blocked) |
| `autodocs-read` | Yes | Yes | Workspace + top-level non-hidden real-home directories as read-only |
| `autodocs-write` | Yes | Yes | Workspace + top-level non-hidden real-home directories as read-write |
| `volumes` | Yes | No | Only configured volume mount points are persisted |
| `ephemeral` | Yes | No | Nothing persists between runs |

### Configuration

```json
{
  "sandbox": {
    "general": {
      "enabled": true,
      "mode": "home",
      "dataDir": "",
      "extraPaths": [],
      "execEnabled": true,
      "browserEnabled": true,
      "fileToolsEnabled": true
    },
    "bubblewrap": {
      "path": "",
      "volumes": []
    },
    "seatbelt": {
      "path": ""
    }
  }
}
```

| Option | Default | Description |
|--------|---------|-------------|
| `sandbox.general.enabled` | `true` | Master sandbox switch |
| `sandbox.general.mode` | `home` | Active mode |
| `sandbox.general.dataDir` | `~/.goclaw/sandbox` | Backing storage root |
| `sandbox.general.extraPaths` | `[]` | Extra PATH entries inside sandboxed exec |
| `sandbox.general.execEnabled` | `true` | Enable sandbox for exec tool |
| `sandbox.general.browserEnabled` | `true` | Enable sandbox for managed browser launches |
| `sandbox.general.fileToolsEnabled` | `true` | Enable file tools path sandboxing |
| `sandbox.bubblewrap.path` | (search PATH) | Custom `bwrap` binary path (Linux) |
| `sandbox.bubblewrap.volumes` | `~/.local`, `~/.config`, `~/.cache` | Persisted home paths in `volumes` mode (Linux) |
| `sandbox.seatbelt.path` | (search PATH) | Custom `sandbox-exec` path (macOS) |

### Setup Wizard Security Presets

The setup wizard Security step exposes these preset labels:

| Preset | Effective behavior |
|--------|--------------------|
| `Assistant (recommended)` | Enables sandboxing for exec, browser, and file tools. Uses `autodocs-write` mode. |
| `Permissive` | Disables sandboxing globally (`sandbox.general.enabled=false`). Mode is not applicable while disabled. |
| `Hardened` | Enables sandboxing for exec, browser, and file tools. Uses `home` mode (Linux isolated home; macOS policy-managed real home with dot dirs blocked). |
| `Custom (advanced)` | Keeps your manual advanced settings (mode + per-category toggles) instead of applying a preset mapping. |

Consent behavior in the wizard:

- `Assistant`, `Permissive`, and `Hardened` require explicit acknowledgment before continuing.
- `Custom (advanced)` is an explicit manual path and does not require preset consent.

### Home Mode

On Linux, the default mode creates a full isolated home directory at `~/.goclaw/sandbox/home/`. Agent-installed tools persist across runs, but are isolated from your real home.

On macOS, `home` mode is policy-managed over real home paths (Seatbelt + file-tools policy), with hidden dot-directories blocked by default.

Recommendation:

- Linux: `home` is the recommended default for strong isolation with persistence.
- macOS: use `autodocs-read` or `autodocs-write` when you want visible-directory-only access; use `home` only when broader real-home policy access is intentional.

```json
{
  "sandbox": {
    "general": {
      "mode": "home"
    }
  }
}
```

### Autodocs Modes

Autodocs modes expose selected real-home directories (top-level, non-hidden, non-symlinked) such as `~/Desktop`, `~/Documents`, `~/Pictures`, and similar.

#### `autodocs-read`

- Exposed autodocs directories are read-only
- Writes to those roots are blocked
- Hidden directories like `~/.ssh` are outside allowed roots

#### `autodocs-write`

- Same roots as `autodocs-read`
- Exposed autodocs directories are writable

### Volumes Mode (Linux)

Only specific directories persist. Useful when you want controlled isolation:

```json
{
  "sandbox": {
    "general": {
      "mode": "volumes"
    },
    "bubblewrap": {
      "volumes": ["~/.local", "~/.config", "~/.npm-global"]
    }
  }
}
```

### Ephemeral Mode (Linux)

Nothing persists between runs. Maximum security but agent-installed tools are lost:

```json
{
  "sandbox": {
    "general": {
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

- Hidden/sensitive home paths outside allowed roots (for example `~/.ssh`, `~/.goclaw`)
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
    "general": {
      "extraPaths": ["/opt/mytools/bin", "~/.custom/bin"]
    }
  }
}
```

### Enabling via Setup

Run `goclaw setup edit` and select "Sandbox" to configure modes and options.

Or during initial setup, the wizard detects the platform sandbox backend and offers to enable sandboxing.

## Browser URL Safety

The browser tool validates URLs before navigation to prevent SSRF (Server-Side Request Forgery) attacks. This protection is **always active**, regardless of sandbox backend status, because filesystem sandboxing does not restrict outbound network destinations.

### Blocked Destinations

| Category | Examples | Why Blocked |
|----------|----------|-------------|
| Loopback | `127.0.0.1`, `localhost`, `::1` | Local service access |
| Private networks | `10.x.x.x`, `192.168.x.x`, `172.16-31.x.x` | Internal network access |
| Link-local | `169.254.x.x`, `fe80::` | Network infrastructure |
| Cloud metadata | `169.254.169.254`, `metadata.google.internal` | Credential theft |

### Evasion Prevention

The URL safety check resolves hostnames to IPs before validation, catching common evasion techniques:

- Decimal IP encoding (`2130706433` = `127.0.0.1`)
- Hex IP encoding (`0x7f000001` = `127.0.0.1`)
- Octal encoding (`0177.0.0.1` = `127.0.0.1`)
- DNS rebinding (`localtest.me` → `127.0.0.1`)
- Short forms (`127.1` → `127.0.0.1`)
- IPv4-mapped IPv6 (`::ffff:127.0.0.1`)

### Allowed

Only `http://` and `https://` URLs to public internet addresses are allowed.

---

## Security Considerations

### Defense in Depth

The layers complement each other:

1. **File tools sandbox** — Prevents direct file access outside workspace
2. **Managed exec/browser sandbox** — Prevents shell commands from escaping
3. **Isolated home** — Agent tools don't mix with your real environment

### What Sandboxing Does NOT Protect Against

- Network-based attacks (network access is allowed)
- Side-channel attacks
- Bugs in the OS sandbox backend itself
- Actions within the workspace (agent can still modify workspace files)

### Recommendations

1. **Use `home` mode** for general use — good balance of security and usability
2. **Use `autodocs-read`** when the agent needs common user docs/media directories but should not modify them
3. **Use `ephemeral` mode** for untrusted prompts — nothing persists
4. **Use `sandbox: false`** sparingly — only for trusted admin users
5. **Review `extraPaths`** — they become accessible inside sandbox

## Troubleshooting

### "bwrap not found"

Install bubblewrap on Linux (see Prerequisites above).

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
