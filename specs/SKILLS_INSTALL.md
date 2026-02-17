# Skills Installation - Security Analysis & Design

## Status: NOT IMPLEMENTED

The `Manager.Install()` function exists but is **dead code** - never called from anywhere. The `skills` tool only exposes `list`, `info`, `check` actions. This appears intentional due to unresolved security concerns.

## Current State

### What Exists

1. **Installer infrastructure** (`internal/skills/installer.go`):
   - `installBrew()` - runs `brew install <formula>`
   - `installGo()` - runs `go install <module>`
   - `installUV()` - runs `uv tool install <package>`
   - `installDownload()` - downloads from URL
   - Node.js **explicitly blocked** (security measure)

2. **InstallSpec in SKILL.md** - Skills can declare install options in frontmatter:
   ```yaml
   metadata:
     openclaw:
       install:
         - kind: brew
           formula: himalaya
           bins: [himalaya]
   ```

3. **Manager.Install()** - Orchestrates installation but is never invoked

### What's Missing

- No `skills(action="install")` in the tool schema
- No user approval flow
- No audit/verification before install
- No sandboxing of install commands

## Security Concerns

### 1. Supply Chain Attacks via Package Managers

Even with Node.js blocked, other package managers are vulnerable:

| Manager | Attack Vector |
|---------|---------------|
| brew | Malicious formula, tap hijacking |
| go install | Typosquatting, dependency confusion |
| uv/pip | PyPI package hijacking |
| download | Arbitrary URL execution |

**Risk**: Agent-controlled input to package managers = arbitrary code execution.

### 2. Skill File Injection

**Attack flow**:
1. Agent writes malicious SKILL.md to workspace `skills/` directory
2. File watcher detects change, reloads skill registry
3. Skill becomes eligible
4. Agent (or future install action) triggers install with attacker-controlled spec

**Mitigations needed**:
- Skills from workspace should require explicit user trust
- New skills should trigger audit before becoming eligible
- Install specs should be validated against allowlist

### Sandboxing as Defense-in-Depth

GoClaw's bubblewrap (bwrap) sandboxing provides significant protection against malicious skills:

**What sandboxing prevents:**
- Skills cannot access directories outside the allowed sandbox paths
- Sensitive files (`~/.ssh`, `~/.gnupg`, credentials) can be excluded from mounts
- Network access can be disabled for skills that don't need it
- Environment variables can be cleared to prevent credential leakage

**What sandboxing does NOT prevent:**
- **Install commands run unsandboxed** - `brew install`, `go install`, `uv tool install` need system access
- Package manager post-install scripts execute with full privileges
- Downloaded binaries are placed in system paths

**Implication**: Even with bwrap protecting tool execution, the install step itself is a privileged operation. A malicious skill's install spec could:
1. Install a trojanized package that runs code during install
2. Place a backdoored binary that later executes inside the sandbox

This is why user approval and package allowlists are critical for the install flow - sandboxing alone is not sufficient.

### 3. ClawHub Remote Installation

The `clawhub` skill (if binary is installed) allows:
```bash
clawhub install <skill-name>
```

This pulls skills from clawhub.com registry - a remote source the agent can invoke via exec.

**Concerns**:
- Registry could be compromised
- Agent could install any published skill
- No local approval before install

### 4. The OpenClaw Precedent

Skills were identified as a "known vulnerability" in OpenClaw. Specific issues (to be documented):
- [ ] What specific attacks were possible?
- [ ] What mitigations were attempted?
- [ ] Why was install functionality not completed?

## Proposed Design

### User Approval Flow

```
Agent: "The himalaya skill needs `himalaya` binary. Install via brew?"
       [Install options displayed]
       
User: Approves in UI

Agent: skills(action="install", skill="himalaya", spec="brew")

System: Runs `brew install himalaya` with output streaming
```

### Trust Levels

| Source | Trust | Install Allowed |
|--------|-------|-----------------|
| Bundled (`<goclaw>/skills/`) | High | Auto-approve known packages |
| Managed (`~/.goclaw/skills/`) | Medium | Require user approval |
| Workspace | Low | Require approval + audit |
| ClawHub | Low | Require approval + audit |

### Install Allowlist

Consider maintaining an allowlist of known-safe packages:

```yaml
# Known safe installs (vetted formulas/modules)
brew:
  - himalaya
  - ripgrep
  - jq
go:
  - github.com/charmbracelet/glow@latest
uv:
  - posting
```

Packages not on allowlist require explicit user confirmation with warning.

### Sandboxing Considerations

Install commands currently run unsandboxed. Options:
1. **Accept risk** - Package managers need system access anyway
2. **Dry-run first** - Show what would be installed before executing
3. **Container isolation** - Run installs in ephemeral container (complex)

Recommendation: Dry-run + user approval is practical; full sandboxing is overkill for package managers.

## Implementation Checklist

- [ ] Research OpenClaw's specific skill vulnerabilities
- [ ] Add `install` action to skills tool schema
- [ ] Implement user approval flow (UI/Telegram prompt)
- [ ] Add trust level checking before install
- [ ] Consider package allowlist
- [ ] Add install audit logging
- [ ] Handle clawhub integration securely
- [ ] Document security model for users

## Linter Warnings (G204 - Intentionally Unsuppressed)

The installer code triggers `gosec` G204 warnings for subprocess execution with variable input. These are **intentionally left unsuppressed** as a reminder that security review is required before enabling:

| File | Line | Code | Risk |
|------|------|------|------|
| `installer.go` | 82 | `exec.CommandContext(ctx, "brew", "install", spec.Formula)` | Formula from SKILL.md |
| `installer.go` | 120 | `exec.CommandContext(ctx, "go", "install", module)` | Module from SKILL.md |
| `installer.go` | 153 | `exec.CommandContext(ctx, "uv", "tool", "install", spec.Package)` | Package from SKILL.md |

**Why not suppress?**
- The `spec.*` values originate from SKILL.md files which could be agent-written
- No input validation or allowlist checking exists
- No user approval flow before execution
- Code is dead but warnings serve as visible reminder

**When to suppress:**
Once the implementation checklist above is complete (user approval, trust levels, allowlist), these can be suppressed with appropriate `//nolint:gosec` comments explaining the mitigations in place.

## References

- `internal/skills/installer.go` - Install implementations
- `internal/skills/manager.go:354` - Dead `Install()` function  
- `internal/tools/skills.go` - Tool schema (no install action)
- `specs/SKILLS_TOOL.md` - Original design (includes install in examples)
- `skills/clawhub/SKILL.md` - Remote skill installation via CLI
