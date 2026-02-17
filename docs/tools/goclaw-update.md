---
title: "GoClaw Update"
description: "Check for and install GoClaw updates from within the agent"
section: "Tools"
weight: 50
---

# GoClaw Update Tool

Check for GoClaw updates and install them from within the agent.

## Overview

The `goclaw_update` tool allows the agent to check for new GoClaw releases and perform self-updates. This enables hands-free updates via natural language:

```
User: "Check if there's a new version of goclaw"
Agent: [calls goclaw_update with action='check']

User: "Update yourself to the latest version"
Agent: [calls goclaw_update with action='update']
```

### Sandbox Exemption

This tool runs **outside the agent sandbox**. It has privileged access to:

- Replace the running GoClaw binary
- Restart the GoClaw process
- Access system paths

This is necessary because the update process requires writing to the binary location and restarting the process — operations that would be blocked by the sandbox.

---

## Actions

### check

Check if a newer version is available without installing.

```json
{
  "action": "check"
}
```

**Response:**

```
Current version: 0.1.0
Latest stable version: 0.2.0

A new version is available!

Changelog:
----------
- Added session supervision
- Fixed memory leak in compaction
- Improved error messages

To install this update, call goclaw_update with action='update'.
```

### update

Download and install the latest version.

```json
{
  "action": "update"
}
```

**Response:**

```
Current version: 0.1.0
Latest stable version: 0.2.0

A new version is available!

Downloading update...
Download complete. Checksum verified.
Installing update...
Update installed! GoClaw will restart.
```

After successful installation, GoClaw restarts automatically. The agent session continues with the new version.

---

## Parameters

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `action` | string | Yes | - | `check` or `update` |
| `channel` | string | No | `stable` | Release channel: `stable` or `beta` |
| `force` | boolean | No | `false` | Reinstall even if already on latest |

### Channels

| Channel | Description |
|---------|-------------|
| `stable` | Production-ready releases (default) |
| `beta` | Pre-release versions for testing |

**Example — check beta channel:**

```json
{
  "action": "check",
  "channel": "beta"
}
```

### Force Reinstall

Use `force: true` to reinstall the current version. Useful for repairing corrupted installations.

```json
{
  "action": "update",
  "force": true
}
```

---

## System-Managed Installations

If GoClaw is installed in a system location (`/usr/bin/`, `/usr/local/bin/`, `/opt/`), self-update is **disabled** to prevent conflicts with package managers.

The tool will return:

```
GoClaw is installed at a system-managed location (/usr/bin/goclaw).

Self-update is disabled to prevent conflicts with the package manager.

To update, use the system package manager or download the latest .deb from:
https://github.com/roelfdiedericks/goclaw/releases/latest
```

See [Updating](../updating.md#system-managed-installations-deb) for instructions on updating `.deb` installations.

---

## Update Process

When `action='update'` is called:

1. **Check** — Query GitHub API for latest release
2. **Compare** — Compare versions to determine if update needed
3. **Download** — Fetch the release archive for current OS/architecture
4. **Verify** — Check SHA256 checksum against `checksums.txt`
5. **Extract** — Unpack the `goclaw` binary from the archive
6. **Replace** — Atomically replace the current binary
7. **Restart** — Exec the new binary (seamless process replacement)

The update is atomic — if any step fails, the original binary is preserved.

### Restart Behavior

The restart method depends on how GoClaw was started:

| Mode | Behavior |
|------|----------|
| Foreground (`goclaw gateway`) | Process replaces itself via `exec()` |
| TUI (`goclaw tui`) | TUI restarts with new version |
| Daemon (`goclaw start`) | Daemon restarts, PID file updated |
| Systemd | GoClaw exits, systemd restarts it |
| Docker | Update works but is not persistent |

---

## Examples

**Check for updates:**

```
User: "Is there a new version of goclaw available?"
```

**Update to latest stable:**

```
User: "Update goclaw to the latest version"
```

**Try beta features:**

```
User: "Update goclaw to the latest beta"
```

**Repair installation:**

```
User: "Reinstall the current version of goclaw"
```

---

## Error Handling

| Error | Cause | Solution |
|-------|-------|----------|
| `failed to check for updates` | Network issue or GitHub API down | Retry later |
| `no stable releases found` | No releases on channel | Try different channel |
| `download failed` | Network issue during download | Retry |
| `checksum mismatch` | Corrupted download | Auto-retried, then fails |
| `failed to apply update` | Permission denied | Check binary location permissions |

---

## See Also

- [Updating](../updating.md) — Full update guide including CLI options
- [Deployment](../deployment.md) — Production deployment
- [Tools](../tools.md) — Tool overview
