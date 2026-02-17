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
| File Tools | read, write, edit | Go path validation | Always on |
| Exec Tool | Shell commands | bubblewrap (Linux) | Off |
| Browser | Chromium | bubblewrap (Linux) | Off |

## File Tools Sandbox

The `read`, `write`, and `edit` tools use Go-side path validation to restrict file access to the workspace directory.

### How It Works

1. **Path Resolution** - Relative paths are resolved against the current working directory
2. **Workspace Containment** - All resolved paths must be within the workspace root
3. **Symlink Prevention** - Symlinks in the path are rejected to prevent escapes
4. **Denied Files** - Sensitive files are always blocked, even within the workspace

### Denied Files

These files are blocked to protect credentials and configuration:

- `users.json` - Contains user credentials and hashes
- `goclaw.json` - Contains API keys and tokens
- `openclaw.json` - Contains API keys and tokens

### Unicode Normalization

Unicode space characters (non-breaking spaces, em spaces, etc.) are normalized to regular spaces to prevent path confusion attacks.

### User Override

Users with `sandbox: false` in `users.json` can bypass path validation:

```json
{
  "admin": {
    "name": "Admin User",
    "role": "owner",
    "sandbox": false
  }
}
```

**Warning**: Disabling sandbox allows the agent to read/write any file the GoClaw process can access.

## Exec Tool Sandbox (bubblewrap)

The exec tool can optionally use [bubblewrap](https://github.com/containers/bubblewrap) (`bwrap`) for kernel-level process isolation on Linux.

### What bubblewrap Does

- **Filesystem Isolation** - Only workspace directory is writable
- **Environment Isolation** - Clears environment variables (prevents API key leaks)
- **PID Namespace** - Process cannot see or signal other processes
- **Network Control** - Network access can be disabled

### Prerequisites

Install bubblewrap:

```bash
# Debian/Ubuntu
sudo apt install bubblewrap

# Arch
sudo pacman -S bubblewrap
```

### Configuration

In `goclaw.json`:

```json
{
  "tools": {
    "bubblewrap": {
      "path": ""  // Empty = search PATH, or explicit path to bwrap
    },
    "exec": {
      "timeout": 1800,  // 30 minutes (matches OpenClaw)
      "bubblewrap": {
        "enabled": false,      // Enable sandboxing
        "extraRoBind": [],     // Additional read-only mounts
        "extraBind": [],       // Additional writable mounts
        "extraEnv": {},        // Additional environment variables
        "allowNetwork": true,  // Allow network access
        "clearEnv": true       // Clear env before setting defaults
      }
    }
  }
}
```

### Enabled via Setup

Run `goclaw setup edit` and select "Sandboxing" to toggle exec sandboxing.

Or run `goclaw setup wizard` during initial setup - it will detect bwrap and offer to enable sandboxing.

### What Commands Can Access

When exec sandbox is enabled:

| Path | Access | Notes |
|------|--------|-------|
| Workspace | Read/Write | Agent's working directory |
| `/usr`, `/lib`, `/bin`, `/sbin` | Read-only | System binaries and libraries |
| `/etc/resolv.conf`, `/etc/hosts` | Read-only | Network configuration |
| `/etc/passwd`, `/etc/group` | Read-only | User/group info for tools |
| `/etc/ssl`, `/etc/ca-certificates` | Read-only | SSL certificates |
| `/tmp` | Read/Write | Isolated tmpfs (not host /tmp) |
| `/proc` | Read-only | Process information |
| `/dev` | Limited | Basic devices only |

### What Commands Cannot Access

- Home directory (except workspace)
- Other users' files
- System configuration outside allowed paths
- Host environment variables (unless explicitly passed)

### Environment Variables

When `clearEnv: true` (default), the sandbox starts with a clean environment:

| Variable | Value | Notes |
|----------|-------|-------|
| `PATH` | `/usr/bin:/bin:/usr/sbin:/sbin` | Standard paths |
| `HOME` | Workspace path | Agent's home |
| `TERM` | `xterm` | Terminal type |
| `LANG` | From host or `C.UTF-8` | Locale |
| `USER` | From host | Username |

Additional variables can be passed via `extraEnv`.

### User Override

Users with `sandbox: false` run commands without bubblewrap, regardless of global setting.

### Error Handling

- **bwrap not found at startup**: Warning logged, sandbox disabled in memory
- **bwrap fails during execution**: Hard error returned to agent
- **Non-Linux systems**: Sandbox unavailable, warning logged if enabled

## Browser Sandbox (bubblewrap)

The browser tool can also use bubblewrap to isolate the Chromium instance.

### Configuration

```json
{
  "tools": {
    "browser": {
      "enabled": true,
      "bubblewrap": {
        "enabled": false,     // Enable sandboxing
        "extraRoBind": [],    // Additional read-only mounts
        "extraBind": [],      // Additional writable mounts
        "gpu": true           // Enable GPU acceleration
      }
    }
  }
}
```

### What Browser Can Access

When browser sandbox is enabled:

| Path | Access | Notes |
|------|--------|-------|
| Workspace | Read/Write | For screenshots, downloads |
| Browser profile | Read/Write | Cookies, cache, settings |
| `/dev/shm` | Read/Write | Required for Chromium IPC |
| `/dev/dri` | Read-only | GPU acceleration (if enabled) |
| X11/Wayland socket | Read-only | Display access |
| Fonts | Read-only | System fonts |

### Limitations

- Browser sandbox requires headed mode to work properly with display
- GPU acceleration may not work in all configurations
- Some sites may detect sandboxed browsers

## Security Considerations

### Defense in Depth

The sandboxing layers complement each other:

1. **File tools sandbox** - Prevents direct file access outside workspace
2. **Exec sandbox** - Prevents shell commands from escaping
3. **Browser sandbox** - Prevents browser from accessing system files

### What Sandboxing Does NOT Protect Against

- Network-based attacks (exec sandbox allows network by default)
- Side-channel attacks
- Bugs in bubblewrap itself
- Actions within the workspace (agent can still delete workspace files)

### Recommendations

1. **Enable exec sandbox** if running on Linux with untrusted prompts
2. **Use `sandbox: false`** sparingly, only for trusted admin users
3. **Review `extraBind` paths** carefully - they become writable
4. **Consider `allowNetwork: false`** for highly sensitive environments

## Troubleshooting

### "bwrap not found"

Install bubblewrap (see Prerequisites above).

### "namespace operation not permitted"

Some container environments restrict namespace creation. Options:

1. Run GoClaw outside the container
2. Use `--privileged` flag with Docker
3. Disable sandbox (`enabled: false`)

### Commands fail inside sandbox

Check if the command needs access to paths not in the sandbox. Add them to `extraRoBind` or `extraBind`:

```json
{
  "tools": {
    "exec": {
      "bubblewrap": {
        "enabled": true,
        "extraRoBind": ["/opt/mytools"]
      }
    }
  }
}
```

### Environment variable missing

Add required variables to `extraEnv`:

```json
{
  "tools": {
    "exec": {
      "bubblewrap": {
        "enabled": true,
        "extraEnv": {
          "MY_VAR": "value"
        }
      }
    }
  }
}
```

## See Also

- [specs/EXEC_SANDBOX.md](../specs/EXEC_SANDBOX.md) - Detailed exec sandbox specification
- [specs/BROWSER_SANDBOX.md](../specs/BROWSER_SANDBOX.md) - Browser sandbox specification
- [Configuration](configuration.md) - Full configuration reference
- [Tools](tools.md) - Tool documentation
