# Exec Tool Sandboxing Specification

## Overview

Sandbox child processes spawned by the `exec` tool using **bubblewrap** (`bwrap`) — a lightweight, unprivileged sandboxing tool used by Flatpak.

The goal is simple: **restrict file access to the workspace directory** while allowing system binaries and network access to work normally.

## Why Bubblewrap

| Approach | Pros | Cons |
|----------|------|------|
| Command blocklist | Simple | Bypassable (`/usr/bin/rm`, aliases) |
| chroot | Strong isolation | Requires root or complex setup |
| Docker | Full isolation | Daemon, YAML configs, heavyweight |
| seccomp | Kernel-enforced | Blocks syscalls, not paths |
| **bubblewrap** | Unprivileged, path-based, no daemon | Single binary dependency |

Bubblewrap is used by Flatpak and [recommended by Anthropic for Claude Code sandboxing](https://code.claude.com/docs/en/sandboxing). It has 5.7k stars and is battle-tested.

## Key Insights from Real-World Usage

From research into how others use bubblewrap:

### 1. Environment Variables Leak Secrets
By default, environment variables are inherited — including `AWS_SECRET_ACCESS_KEY`, `SSH_AUTH_SOCK`, etc. **Always use `--clearenv`** to start clean:
```bash
--clearenv \
--setenv PATH "/usr/bin:/bin" \
--setenv HOME "$WORKSPACE" \
```

### 2. Process List Leaks Information  
Without `--unshare-pid`, sandboxed processes can see all host processes via `ps aux`. Always unshare PID namespace.

### 3. Binding All of /etc is Dangerous
Don't do `--ro-bind /etc /etc`. This exposes:
- `/etc/shadow` (password hashes)
- `/etc/sudoers`
- Application configs with secrets

Instead, bind only what's needed (resolv.conf, hosts, ssl, passwd, group, localtime, nsswitch.conf).

### 4. Abstract Unix Sockets Bypass Filesystem Isolation
Abstract unix sockets (those starting with `@`) exist in the network namespace, not filesystem. If you `--share-net` but want to block D-Bus access, you need additional measures.

### 5. Symlink Handling Varies by Distro
- Arch/Fedora: `/bin` → `/usr/bin`, `/lib` → `/usr/lib`
- Debian/Ubuntu: Separate directories (transitioning)

The safest approach is to check what exists and bind accordingly.

## How It Works

```
┌─────────────────────────────────────────────────────────────┐
│ GoClaw Process                                              │
│                                                             │
│   exec(command="curl https://example.com -o file.txt")      │
│         │                                                   │
│         ▼                                                   │
│   ┌─────────────────────────────────────────────────────┐   │
│   │ bwrap --ro-bind /usr /usr \                         │   │
│   │       --ro-bind /lib /lib \                         │   │
│   │       --ro-bind /etc/resolv.conf /etc/resolv.conf \ │   │
│   │       --bind /workspace /workspace \                │   │
│   │       --share-net \                                 │   │
│   │       -- sh -c "curl ..."                           │   │
│   └─────────────────────────────────────────────────────┘   │
│         │                                                   │
│         ▼                                                   │
│   ┌─────────────────────────────────────────────────────┐   │
│   │ Sandboxed Process                                   │   │
│   │                                                     │   │
│   │   ✅ Can read /usr, /lib, /bin (system binaries)   │   │
│   │   ✅ Can read /etc/resolv.conf (DNS works)         │   │
│   │   ✅ Can access network (--share-net)              │   │
│   │   ✅ Can read/write /workspace                     │   │
│   │   ❌ Cannot see /home, ~/.ssh, ~/.aws              │   │
│   │   ❌ Cannot see /etc (except allowed files)        │   │
│   │   ❌ Cannot write outside /workspace               │   │
│   └─────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

## Bubblewrap Command

Based on real-world usage patterns from the community:

```bash
bwrap \
  # System binaries (read-only)
  --ro-bind /usr /usr \
  --ro-bind /lib /lib \
  --ro-bind /lib64 /lib64 \
  --ro-bind /bin /bin \
  --ro-bind /sbin /sbin \
  # Minimal /etc files (not all of /etc!)
  --ro-bind /etc/resolv.conf /etc/resolv.conf \
  --ro-bind /etc/hosts /etc/hosts \
  --ro-bind /etc/ssl /etc/ssl \
  --ro-bind /etc/passwd /etc/passwd \
  --ro-bind /etc/group /etc/group \
  --ro-bind /etc/nsswitch.conf /etc/nsswitch.conf \
  --ro-bind /etc/localtime /etc/localtime \
  # Workspace (only writable location)
  --bind "$WORKSPACE" "$WORKSPACE" \
  # Isolated directories
  --tmpfs /tmp \
  --proc /proc \
  --dev /dev \
  # Network access
  --share-net \
  # Security: clear env, isolate PIDs
  --clearenv \
  --setenv PATH "/usr/bin:/bin:/usr/sbin:/sbin" \
  --setenv HOME "$WORKSPACE" \
  --setenv TERM "xterm" \
  --setenv LANG "${LANG:-C.UTF-8}" \
  --setenv USER "$USER" \
  --unshare-pid \
  --die-with-parent \
  --chdir "$WORKSPACE" \
  -- sh -c "$COMMAND"
```

### What Each Flag Does

| Flag | Purpose |
|------|---------|
| `--ro-bind /usr /usr` | System binaries (read-only) |
| `--ro-bind /lib /lib` | Shared libraries (read-only) |
| `--ro-bind /lib64 /lib64` | 64-bit libraries (read-only) |
| `--ro-bind /bin /bin` | Core binaries (read-only) |
| `--ro-bind /sbin /sbin` | System binaries (read-only) |
| `--ro-bind /etc/resolv.conf` | DNS resolution works |
| `--ro-bind /etc/hosts` | Hostname lookup works |
| `--ro-bind /etc/ssl` | HTTPS/TLS works |
| `--ro-bind /etc/passwd` | User lookup works (for `whoami`, etc.) |
| `--ro-bind /etc/group` | Group lookup works |
| `--ro-bind /etc/nsswitch.conf` | Name service switch config |
| `--ro-bind /etc/localtime` | Correct timezone for timestamps |
| `--bind $WORKSPACE $WORKSPACE` | **Writable** workspace directory |
| `--tmpfs /tmp` | Isolated temp directory |
| `--proc /proc` | Process info works |
| `--dev /dev` | Devices work (stdin/stdout/stderr) |
| `--share-net` | Network access (no isolation) |
| `--unshare-pid` | Isolated PID namespace |
| `--die-with-parent` | Kill sandbox if GoClaw dies |
| `--clearenv` | Clear all environment variables (security!) |
| `--setenv PATH "..."` | Set minimal PATH |
| `--setenv HOME "$WORKSPACE"` | Set HOME to workspace |
| `--setenv TERM "xterm"` | Terminal type for curses/colors |
| `--setenv LANG "..."` | Locale (preserve from host or C.UTF-8) |
| `--setenv USER "$USER"` | Username (some tools expect this) |
| `--chdir $WORKSPACE` | Start in workspace |

### What's Blocked

- `/home/*` — User home directories invisible
- `~/.ssh` — SSH keys invisible
- `~/.aws` — AWS credentials invisible
- `~/.config` — User configs invisible
- `/etc/*` — Most of /etc invisible (only specific files allowed)
- Writes outside workspace — Silently fail or error

## Implementation

### Config

```json
{
  "tools": {
    "exec": {
      "enabled": true,
      "sandbox": {
        "enabled": true,
        "bwrapPath": "",
        "extraRoBind": [],
        "extraBind": [],
        "extraEnv": {},
        "allowNetwork": true,
        "clearEnv": true
      },
      "timeout": 300
    }
  }
}
```

| Option | Default | Description |
|--------|---------|-------------|
| `enabled` | `true` | Enable sandboxing |
| `bwrapPath` | auto-detect | Path to bwrap binary |
| `extraRoBind` | `[]` | Additional read-only paths |
| `extraBind` | `[]` | Additional writable paths |
| `extraEnv` | `{}` | Additional environment variables |
| `allowNetwork` | `true` | Allow network access |
| `clearEnv` | `true` | Clear environment (recommended!) |

### Go Implementation

```go
// internal/tools/exec.go

func (t *ExecTool) Execute(ctx context.Context, input ExecInput) (string, error) {
    if t.config.Sandbox.Enabled {
        return t.executeWithSandbox(ctx, input)
    }
    return t.executeUnsandboxed(ctx, input)
}

func (t *ExecTool) executeWithSandbox(ctx context.Context, input ExecInput) (string, error) {
    bwrapPath := t.config.Sandbox.BwrapPath
    if bwrapPath == "" {
        var err error
        bwrapPath, err = exec.LookPath("bwrap")
        if err != nil {
            L_warn("bwrap not found, running unsandboxed")
            return t.executeUnsandboxed(ctx, input)
        }
    }

    args := t.buildBwrapArgs(input.Command)
    cmd := exec.CommandContext(ctx, bwrapPath, args...)
    
    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr

    err := cmd.Run()
    
    output := stdout.String()
    if stderr.Len() > 0 {
        output += "\n" + stderr.String()
    }

    return output, err
}

func (t *ExecTool) buildBwrapArgs(command string) []string {
    workspace := t.workspacePath

    args := []string{
        // System binaries (read-only)
        "--ro-bind", "/usr", "/usr",
        "--ro-bind", "/lib", "/lib",
        "--ro-bind", "/bin", "/bin",
        "--ro-bind", "/sbin", "/sbin",
        
        // /etc files needed for basic operation
        "--ro-bind", "/etc/resolv.conf", "/etc/resolv.conf",
        "--ro-bind", "/etc/hosts", "/etc/hosts",
        "--ro-bind", "/etc/passwd", "/etc/passwd",
        "--ro-bind", "/etc/group", "/etc/group",
        "--ro-bind", "/etc/nsswitch.conf", "/etc/nsswitch.conf",
    }

    // /etc/localtime for correct timestamps
    if pathExists("/etc/localtime") {
        args = append(args, "--ro-bind", "/etc/localtime", "/etc/localtime")
    }

    // /lib64 if exists (64-bit systems)
    if pathExists("/lib64") {
        args = append(args, "--ro-bind", "/lib64", "/lib64")
    }

    // /etc/ssl for HTTPS
    if pathExists("/etc/ssl") {
        args = append(args, "--ro-bind", "/etc/ssl", "/etc/ssl")
    }

    // /etc/ca-certificates (Debian/Ubuntu)
    if pathExists("/etc/ca-certificates") {
        args = append(args, "--ro-bind", "/etc/ca-certificates", "/etc/ca-certificates")
    }

    // /etc/pki (RHEL/Fedora)
    if pathExists("/etc/pki") {
        args = append(args, "--ro-bind", "/etc/pki", "/etc/pki")
    }

    // Extra read-only binds from config
    for _, path := range t.config.Sandbox.ExtraRoBind {
        if pathExists(path) {
            args = append(args, "--ro-bind", path, path)
        }
    }

    // Workspace (writable)
    args = append(args, "--bind", workspace, workspace)

    // Extra writable binds from config
    for _, path := range t.config.Sandbox.ExtraBind {
        if pathExists(path) {
            args = append(args, "--bind", path, path)
        }
    }

    // Isolated /tmp
    args = append(args, "--tmpfs", "/tmp")

    // /proc and /dev
    args = append(args, "--proc", "/proc")
    args = append(args, "--dev", "/dev")

    // Network
    if t.config.Sandbox.AllowNetwork {
        args = append(args, "--share-net")
    } else {
        args = append(args, "--unshare-net")
    }

    // Clear environment (security: prevents AWS_SECRET_ACCESS_KEY etc from leaking)
    if t.config.Sandbox.ClearEnv {
        args = append(args, "--clearenv")
        // Set minimal required environment
        args = append(args, "--setenv", "PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
        args = append(args, "--setenv", "HOME", workspace)
        args = append(args, "--setenv", "TERM", "xterm")
        
        // Preserve LANG and USER from host (tools misbehave without these)
        if lang := os.Getenv("LANG"); lang != "" {
            args = append(args, "--setenv", "LANG", lang)
        } else {
            args = append(args, "--setenv", "LANG", "C.UTF-8")
        }
        if user := os.Getenv("USER"); user != "" {
            args = append(args, "--setenv", "USER", user)
        }
        
        // Add extra env vars from config
        for k, v := range t.config.Sandbox.ExtraEnv {
            args = append(args, "--setenv", k, v)
        }
    }

    // PID namespace and cleanup
    args = append(args,
        "--unshare-pid",
        "--die-with-parent",
        "--chdir", workspace,
        "--",
        "sh", "-c", command,
    )

    return args
}

func pathExists(path string) bool {
    _, err := os.Stat(path)
    return err == nil
}
```

### Detecting Bubblewrap

GoClaw does **not** bundle bwrap — it's the user's responsibility to install it. This keeps the binary clean and avoids owning someone else's security surface.

```go
func (t *ExecTool) checkBwrapAvailable() (string, error) {
    // Check config path first
    if t.config.Sandbox.BwrapPath != "" {
        if _, err := os.Stat(t.config.Sandbox.BwrapPath); err == nil {
            return t.config.Sandbox.BwrapPath, nil
        }
    }

    // Try to find in PATH
    path, err := exec.LookPath("bwrap")
    if err != nil {
        return "", fmt.Errorf(`sandbox enabled but bwrap not found

Install bubblewrap:
  Debian/Ubuntu:  apt install bubblewrap
  Fedora/RHEL:    dnf install bubblewrap
  Arch:           pacman -S bubblewrap

Or disable sandbox: set tools.exec.sandbox.enabled = false`)
    }

    return path, nil
}
```

**Behavior:**
- **Startup:** Log warning if bwrap not found (non-fatal)
- **Exec with sandbox enabled:** Return helpful error with install instructions
- **Exec with sandbox disabled:** Run unsandboxed (no warning)

## Installation

### Debian/Ubuntu
```bash
sudo apt install bubblewrap
```

### Fedora/RHEL
```bash
sudo dnf install bubblewrap
```

### Arch Linux
```bash
sudo pacman -S bubblewrap
```

### macOS
Not supported. Bubblewrap is Linux-only. On macOS, sandbox is disabled (with warning).

### Verification
```bash
bwrap --version
```

## Examples

### Example 1: curl (network access)

```bash
exec(command="curl -s https://wttr.in/Johannesburg")
```

**Sandboxed as:**
```bash
bwrap ... --share-net -- sh -c "curl -s https://wttr.in/Johannesburg"
```
- ✅ Network works
- ✅ /etc/resolv.conf available for DNS
- ✅ /etc/ssl available for HTTPS

### Example 2: grep (file access)

```bash
exec(command="grep -r TODO .")
```

**Sandboxed as:**
```bash
bwrap ... --chdir /workspace -- sh -c "grep -r TODO ."
```
- ✅ Can read workspace files
- ❌ Cannot read /home/user/secrets

### Example 3: git clone

```bash
exec(command="git clone https://github.com/user/repo.git")
```

**Sandboxed as:**
```bash
bwrap ... --share-net --bind /workspace /workspace -- sh -c "git clone ..."
```
- ✅ Network works
- ✅ Can write to workspace
- ❌ Cannot read ~/.ssh (no SSH clone)
- ❌ Cannot read ~/.gitconfig (use --extraRoBind if needed)

### Example 4: Attempted escape

```bash
exec(command="cat /etc/shadow")
```

**Result:**
```
cat: /etc/shadow: No such file or directory
```

```bash
exec(command="cat ~/.ssh/id_rsa")
```

**Result:**
```
cat: /home/user/.ssh/id_rsa: No such file or directory
```

## Limitations

### 1. Linux Only
Bubblewrap uses Linux namespaces. No macOS/Windows support.

```go
//go:build linux

func (t *ExecTool) executeWithSandbox(...) { ... }
```

```go
//go:build !linux

func (t *ExecTool) executeWithSandbox(ctx context.Context, input ExecInput) (string, error) {
    L_warn("sandbox not available on this platform, running unsandboxed")
    return t.executeUnsandboxed(ctx, input)
}
```

### 2. No SSH Access by Default
SSH keys in ~/.ssh are not visible. If needed:
```json
{
  "sandbox": {
    "extraRoBind": ["/home/user/.ssh"]
  }
}
```

### 3. No User Config Access
~/.gitconfig, ~/.npmrc, etc. are not visible. Add via extraRoBind if needed.

### 4. Requires bwrap Binary
If bubblewrap isn't installed, falls back to unsandboxed execution (with warning).

### 5. XDG Runtime Directory
Some tools expect `/run/user/$UID`. May need `--tmpfs /run` or selective bind for specific tools.

### 6. Symlink Handling
On some distros `/bin` → `/usr/bin`. The `pathExists` check handles this, but behavior may vary.

### 7. Error Messages
When a file access fails in sandbox, the error is "No such file or directory" — not a permission error. This is expected and intentional (the file literally doesn't exist in the sandbox's view), but may confuse users debugging issues.

## Security Layers

Defense in depth — sandbox is one layer:

```
┌─────────────────────────────────────────────────────────────┐
│ Layer 1: Bubblewrap Sandbox (this spec)                     │
│   - Filesystem isolation                                    │
│   - Only workspace is writable                              │
│   - Sensitive paths invisible                               │
├─────────────────────────────────────────────────────────────┤
│ Layer 2: Working Directory                                  │
│   - Commands default to workspace                           │
│   - Relative paths resolve safely                           │
├─────────────────────────────────────────────────────────────┤
│ Layer 3: Timeout                                            │
│   - Kill long-running commands                              │
│   - Prevent resource exhaustion                             │
├─────────────────────────────────────────────────────────────┤
│ Layer 4: User Permissions                                   │
│   - Non-owner users may have exec disabled                  │
│   - Policy-based tool access                                │
└─────────────────────────────────────────────────────────────┘
```

## Implementation Phases

### Phase 1: Basic Integration
- [ ] Add sandbox config to exec tool
- [ ] Implement bwrap wrapper
- [ ] Auto-detect bwrap availability
- [ ] Graceful fallback when unavailable
- [ ] Add logging

### Phase 2: Configuration
- [ ] extraRoBind / extraBind options
- [ ] Network toggle
- [ ] Per-command sandbox bypass (for trusted commands)

### Phase 3: Documentation
- [ ] Installation guide per distro
- [ ] Troubleshooting guide
- [ ] Security documentation

## Testing

```bash
# Test basic isolation
bwrap --ro-bind /usr /usr --ro-bind /bin /bin --ro-bind /lib /lib \
      --proc /proc --dev /dev --tmpfs /tmp \
      -- ls /home
# Should show empty or error

# Test workspace access
bwrap --ro-bind /usr /usr --ro-bind /bin /bin --ro-bind /lib /lib \
      --bind /tmp/test /tmp/test --proc /proc --dev /dev \
      --chdir /tmp/test \
      -- touch testfile && ls -la
# Should work

# Test network
bwrap --ro-bind /usr /usr --ro-bind /bin /bin --ro-bind /lib /lib \
      --ro-bind /etc/resolv.conf /etc/resolv.conf \
      --ro-bind /etc/ssl /etc/ssl \
      --proc /proc --dev /dev --share-net \
      -- curl -s https://example.com
# Should work
```

## Summary

| Aspect | Details |
|--------|---------|
| **Mechanism** | bubblewrap (bwrap) filesystem namespaces |
| **Goal** | Restrict file access to workspace only |
| **System binaries** | Work via ro-bind of /usr, /lib, /bin |
| **Network** | Works (--share-net), or can disable |
| **DNS/HTTPS** | Works via ro-bind of /etc/resolv.conf, /etc/ssl |
| **Writes** | Only to workspace |
| **Dependency** | `bubblewrap` package |
| **Platforms** | Linux only (graceful fallback elsewhere) |

Simple, effective, battle-tested. 🐀
