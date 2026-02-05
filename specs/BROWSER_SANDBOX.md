# Browser Sandboxing Specification

## Overview

Sandbox the managed Chromium browser using **bubblewrap** (`bwrap`) to restrict filesystem access. The browser can only access its profile directory, the workspace (for screenshots/downloads), and system libraries needed to run.

## Why Sandbox the Browser?

GoClaw downloads and manages a Chromium binary that runs with the user's permissions. This introduces risk:

| Risk | Example | Mitigation |
|------|---------|------------|
| `file://` URL access | Navigate to `file:///etc/passwd` | Sandbox: file doesn't exist |
| File upload exfiltration | Malicious page requests upload of `/etc/shadow` | Sandbox: file doesn't exist |
| Download overwrites | Download to `~/.ssh/authorized_keys` | Sandbox: path doesn't exist |
| Browser exploit | Zero-day escapes Chromium's internal sandbox | bwrap provides second layer |
| Agent manipulation | Prompt injection tricks agent into dangerous actions | Limited blast radius |

**Defense in depth:** Chromium has its own internal sandbox, but we add bubblewrap as an outer layer. If Chromium's sandbox fails, bwrap contains the damage.

## Trust Model

We're asking users to trust:
1. GoClaw binary (they can audit)
2. Chromium binary (downloaded from official go-rod CDN)
3. Websites the agent visits

We can't vet Chromium ourselves, but we can **contain it**. The browser should only be able to:
- Read system libraries (to run)
- Read/write its profile directory (cookies, cache, state)
- Read/write workspace media directories (screenshots, downloads)
- Access network (obviously)

Everything else — home directory, SSH keys, AWS credentials, other configs — should be invisible.

## Sandboxed Paths

### Read-Only (system binaries & libs)
```
/usr
/lib
/lib64
/bin
/sbin
/etc/resolv.conf
/etc/hosts
/etc/ssl
/etc/ca-certificates (Debian/Ubuntu)
/etc/pki (RHEL/Fedora)
/etc/passwd
/etc/group
/etc/nsswitch.conf
/etc/localtime
/etc/fonts
```

### Read-Write (browser needs these)
```
~/.openclaw/goclaw/browser/          # Managed Chromium + profiles
$WORKSPACE/media/browser/            # Screenshots
$WORKSPACE/media/downloads/          # Downloads
/tmp                                 # Chromium temp files (tmpfs)
```

### Blocked (invisible to browser)
```
~/.ssh/
~/.aws/
~/.config/ (except browser profile)
~/.gnupg/
~/Documents/
/etc/shadow
Everything else
```

## Bubblewrap Command

```bash
bwrap \
  # System binaries (read-only)
  --ro-bind /usr /usr \
  --ro-bind /lib /lib \
  --ro-bind /lib64 /lib64 \
  --ro-bind /bin /bin \
  --ro-bind /sbin /sbin \
  # Minimal /etc files
  --ro-bind /etc/resolv.conf /etc/resolv.conf \
  --ro-bind /etc/hosts /etc/hosts \
  --ro-bind /etc/ssl /etc/ssl \
  --ro-bind /etc/passwd /etc/passwd \
  --ro-bind /etc/group /etc/group \
  --ro-bind /etc/nsswitch.conf /etc/nsswitch.conf \
  --ro-bind /etc/localtime /etc/localtime \
  --ro-bind /etc/fonts /etc/fonts \
  # Browser directory (profiles, chromium binary)
  --bind "$BROWSER_DIR" "$BROWSER_DIR" \
  # Workspace media directories
  --bind "$WORKSPACE/media/browser" "$WORKSPACE/media/browser" \
  --bind "$WORKSPACE/media/downloads" "$WORKSPACE/media/downloads" \
  # Isolated /tmp for Chromium
  --tmpfs /tmp \
  # Required for Chromium
  --proc /proc \
  --dev /dev \
  --dev-bind /dev/dri /dev/dri \       # GPU acceleration (optional)
  --dev-bind /dev/shm /dev/shm \       # Shared memory (required)
  # Network access
  --share-net \
  # Security
  --clearenv \
  --setenv PATH "/usr/bin:/bin" \
  --setenv HOME "$BROWSER_DIR" \
  --setenv DISPLAY "$DISPLAY" \        # For headed mode
  --setenv XDG_RUNTIME_DIR /tmp \
  --unshare-pid \
  --die-with-parent \
  -- "$BROWSER_DIR/chromium/chrome" "$@"
```

## Chromium-Specific Requirements

### /dev/shm (Shared Memory)
Chromium requires `/dev/shm` for IPC between processes. Without it:
```
[ERROR:bus.cc] Failed to connect to the bus: Could not parse server address
```

Options:
1. `--dev-bind /dev/shm /dev/shm` — bind host's shared memory (simpler)
2. `--tmpfs /dev/shm` — isolated shared memory (more secure, may have size limits)

Recommend: `--dev-bind /dev/shm /dev/shm` for compatibility.

### /dev/dri (GPU)
For hardware acceleration (headed mode):
```bash
--dev-bind /dev/dri /dev/dri
```

Can be omitted for headless — Chromium falls back to software rendering.

### Fonts
Without fonts, pages render as boxes:
```bash
--ro-bind /etc/fonts /etc/fonts
--ro-bind /usr/share/fonts /usr/share/fonts
```

### D-Bus (Optional)
Some Chromium features need D-Bus. For headless automation, usually not required:
```bash
# Only if needed:
--ro-bind /run/dbus /run/dbus
```

## Implementation

### Config

```json
{
  "browser": {
    "sandbox": {
      "enabled": true,
      "bwrapPath": "",
      "extraRoBind": [],
      "extraBind": [],
      "gpu": true
    }
  }
}
```

| Option | Default | Description |
|--------|---------|-------------|
| `enabled` | `true` | Enable browser sandboxing |
| `bwrapPath` | auto-detect | Path to bwrap binary |
| `extraRoBind` | `[]` | Additional read-only paths |
| `extraBind` | `[]` | Additional writable paths |
| `gpu` | `true` | Bind /dev/dri for GPU acceleration |

### Go Implementation

```go
// internal/browser/sandbox.go

func (m *Manager) buildSandboxedCommand(chromiumPath string, args []string) *exec.Cmd {
    if !m.config.Sandbox.Enabled {
        return exec.Command(chromiumPath, args...)
    }
    
    bwrapPath, err := m.findBwrap()
    if err != nil {
        L_warn("bwrap not found, running browser unsandboxed", "error", err)
        return exec.Command(chromiumPath, args...)
    }
    
    bwrapArgs := m.buildBwrapArgs(chromiumPath, args)
    return exec.Command(bwrapPath, bwrapArgs...)
}

func (m *Manager) buildBwrapArgs(chromiumPath string, chromeArgs []string) []string {
    browserDir := m.browserDir      // ~/.openclaw/goclaw/browser
    workspaceMedia := m.workspacePath + "/media"
    
    args := []string{
        // System binaries (read-only)
        "--ro-bind", "/usr", "/usr",
        "--ro-bind", "/lib", "/lib",
        "--ro-bind", "/bin", "/bin",
        "--ro-bind", "/sbin", "/sbin",
    }
    
    // /lib64 if exists
    if pathExists("/lib64") {
        args = append(args, "--ro-bind", "/lib64", "/lib64")
    }
    
    // /etc files
    etcFiles := []string{
        "/etc/resolv.conf",
        "/etc/hosts",
        "/etc/passwd",
        "/etc/group",
        "/etc/nsswitch.conf",
        "/etc/localtime",
        "/etc/fonts",
    }
    for _, f := range etcFiles {
        if pathExists(f) {
            args = append(args, "--ro-bind", f, f)
        }
    }
    
    // SSL certificates (distro-specific)
    sslPaths := []string{"/etc/ssl", "/etc/ca-certificates", "/etc/pki"}
    for _, p := range sslPaths {
        if pathExists(p) {
            args = append(args, "--ro-bind", p, p)
        }
    }
    
    // Fonts
    if pathExists("/usr/share/fonts") {
        args = append(args, "--ro-bind", "/usr/share/fonts", "/usr/share/fonts")
    }
    
    // Browser directory (writable - profiles, cache)
    args = append(args, "--bind", browserDir, browserDir)
    
    // Workspace media directories (writable - screenshots, downloads)
    mediaDirs := []string{
        workspaceMedia + "/browser",
        workspaceMedia + "/downloads",
    }
    for _, d := range mediaDirs {
        ensureDir(d)
        args = append(args, "--bind", d, d)
    }
    
    // Extra binds from config
    for _, p := range m.config.Sandbox.ExtraRoBind {
        if pathExists(p) {
            args = append(args, "--ro-bind", p, p)
        }
    }
    for _, p := range m.config.Sandbox.ExtraBind {
        if pathExists(p) {
            args = append(args, "--bind", p, p)
        }
    }
    
    // /tmp, /proc, /dev
    args = append(args,
        "--tmpfs", "/tmp",
        "--proc", "/proc",
        "--dev", "/dev",
        "--dev-bind", "/dev/shm", "/dev/shm",
    )
    
    // GPU acceleration
    if m.config.Sandbox.GPU && pathExists("/dev/dri") {
        args = append(args, "--dev-bind", "/dev/dri", "/dev/dri")
    }
    
    // Network
    args = append(args, "--share-net")
    
    // Environment
    args = append(args,
        "--clearenv",
        "--setenv", "PATH", "/usr/bin:/bin",
        "--setenv", "HOME", browserDir,
        "--setenv", "XDG_RUNTIME_DIR", "/tmp",
    )
    
    // Display for headed mode
    if display := os.Getenv("DISPLAY"); display != "" {
        args = append(args, "--setenv", "DISPLAY", display)
        // X11 socket
        args = append(args, "--ro-bind", "/tmp/.X11-unix", "/tmp/.X11-unix")
    }
    
    // Wayland support
    if waylandDisplay := os.Getenv("WAYLAND_DISPLAY"); waylandDisplay != "" {
        args = append(args, "--setenv", "WAYLAND_DISPLAY", waylandDisplay)
        xdgRuntime := os.Getenv("XDG_RUNTIME_DIR")
        if xdgRuntime != "" {
            waylandSocket := xdgRuntime + "/" + waylandDisplay
            if pathExists(waylandSocket) {
                args = append(args, "--ro-bind", waylandSocket, "/tmp/"+waylandDisplay)
            }
        }
    }
    
    // Security flags
    args = append(args,
        "--unshare-pid",
        "--die-with-parent",
    )
    
    // Chromium command
    args = append(args, "--", chromiumPath)
    args = append(args, chromeArgs...)
    
    return args
}

func (m *Manager) findBwrap() (string, error) {
    if m.config.Sandbox.BwrapPath != "" {
        if pathExists(m.config.Sandbox.BwrapPath) {
            return m.config.Sandbox.BwrapPath, nil
        }
    }
    
    path, err := exec.LookPath("bwrap")
    if err != nil {
        return "", fmt.Errorf(`browser sandbox enabled but bwrap not found

Install bubblewrap:
  Debian/Ubuntu:  apt install bubblewrap
  Fedora/RHEL:    dnf install bubblewrap
  Arch:           pacman -S bubblewrap

Or disable sandbox: set browser.sandbox.enabled = false`)
    }
    
    return path, nil
}
```

### Integration with go-rod

go-rod needs to launch Chromium. We intercept the launch:

```go
// When creating browser launcher
func (m *Manager) createLauncher(profile string) *launcher.Launcher {
    chromiumPath := m.getChromiumPath()
    
    l := launcher.New().
        Bin(chromiumPath).
        UserDataDir(m.getProfileDir(profile))
    
    if m.config.Sandbox.Enabled && m.hasBwrap() {
        // Wrap the launcher with bwrap
        l = l.Set("wrapper", m.buildBwrapWrapper(chromiumPath))
    }
    
    return l
}
```

Alternatively, if go-rod doesn't support wrappers easily, we can:
1. Create a wrapper script that calls bwrap
2. Point go-rod at the wrapper script as the "browser binary"

```bash
#!/bin/bash
# ~/.openclaw/goclaw/browser/chromium-sandboxed.sh
exec bwrap [all the args] -- /actual/chromium "$@"
```

## Headless vs Headed

| Mode | Display | GPU | Use Case |
|------|---------|-----|----------|
| Headless | None | Optional | Normal automation |
| Headed | X11/Wayland | Yes | Debugging, visual confirmation |

For headed mode, we need to bind the display socket:
```bash
# X11
--ro-bind /tmp/.X11-unix /tmp/.X11-unix
--setenv DISPLAY :0

# Wayland
--ro-bind $XDG_RUNTIME_DIR/$WAYLAND_DISPLAY /tmp/$WAYLAND_DISPLAY
--setenv WAYLAND_DISPLAY $WAYLAND_DISPLAY
```

## Chrome Extension Relay

When using `profile="chrome"` (connecting to user's real Chrome via extension), the sandbox doesn't apply — we're connecting to an already-running browser, not launching one.

This is intentional: the user explicitly chose to use their real browser with all its access.

## Limitations

### 1. bwrap Required
Same as exec sandbox — user must install bubblewrap.

### 2. Some Sites May Break
Sites that need specific browser features (e.g., certain auth flows that expect specific directories) may fail. Can add paths via `extraRoBind`/`extraBind`.

### 3. Extensions
Browser extensions are stored in the profile directory, which is accessible. But extensions can't escape the sandbox either.

### 4. File Downloads
Downloads can only go to `$WORKSPACE/media/downloads/`. Attempting to download elsewhere will fail or be redirected.

### 5. Clipboard
Clipboard access may not work in sandboxed headless mode. Usually not needed for automation.

## Security Layers

```
┌─────────────────────────────────────────────────────────────┐
│ Layer 1: Bubblewrap Sandbox (this spec)                     │
│   - Filesystem isolation                                    │
│   - Only browser dir + workspace writable                   │
│   - Sensitive paths invisible                               │
├─────────────────────────────────────────────────────────────┤
│ Layer 2: Chromium's Internal Sandbox                        │
│   - Renderer process isolation                              │
│   - Site isolation                                          │
│   - Seccomp filters                                         │
├─────────────────────────────────────────────────────────────┤
│ Layer 3: Profile Isolation                                  │
│   - Separate profiles per domain                            │
│   - Cookies/auth don't leak between profiles                │
├─────────────────────────────────────────────────────────────┤
│ Layer 4: Agent Controls                                     │
│   - profileDomains config (agent can't pick profiles)       │
│   - URL validation could be added                           │
└─────────────────────────────────────────────────────────────┘
```

## Testing

```bash
# Test filesystem isolation
bwrap [sandbox args] -- chromium --headless --dump-dom "file:///etc/passwd"
# Should fail: file not found

# Test screenshot saving
bwrap [sandbox args] -- chromium --headless --screenshot=/workspace/media/browser/test.png https://example.com
# Should work

# Test blocked path
bwrap [sandbox args] -- chromium --headless --screenshot=/etc/test.png https://example.com
# Should fail: path not writable

# Test network
bwrap [sandbox args] -- chromium --headless --dump-dom https://example.com
# Should work
```

## Implementation Phases

### Phase 1: Basic Integration
- [ ] Add browser sandbox config
- [ ] Implement bwrap wrapper for Chromium launch
- [ ] Test with headless automation
- [ ] Graceful fallback when bwrap unavailable

### Phase 2: Display Support
- [ ] X11 socket binding for headed mode
- [ ] Wayland support
- [ ] GPU acceleration

### Phase 3: Hardening
- [ ] Verify all Chromium features work (downloads, screenshots, PDFs)
- [ ] Test with various sites
- [ ] Document any compatibility issues

## Summary

| Aspect | Details |
|--------|---------|
| **Goal** | Contain managed Chromium to browser dir + workspace |
| **Mechanism** | bubblewrap filesystem isolation |
| **Writable** | Browser profiles, workspace media dirs |
| **Blocked** | Home dir, SSH, AWS, system configs |
| **Network** | Full access (required) |
| **Dependency** | bubblewrap package |
| **Fallback** | Runs unsandboxed with warning |

The browser is a binary we introduce to the user's system. The least we can do is contain it. 🐀
