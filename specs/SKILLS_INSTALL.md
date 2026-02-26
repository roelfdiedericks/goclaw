# Skills Install & Reload Actions

## Status

Extracted from `SKILLS_TOOL.md` (now in attic). The query side (`list`, `info`, `check`) is implemented. This spec covers the remaining write-side actions.

**Note:** The `skills/` directory is now write-protected in the file sandbox. The `install` action must write through the skills manager directly, bypassing the sandbox file tools. This is by design — skill installation is a privileged operation.

## Install Action

### Philosophy: Separation of Concerns

The `install` action handles **skill file management only** — fetching, validating, and writing SKILL.md files. It does NOT execute binary installations.

**Skills tool handles:**
- Fetching SKILL.md from trusted sources
- Validating source trust (clawhub, allowlisted repos, local)
- Scanning for dangerous patterns
- Writing to the skills directory
- Refusing untrusted/dangerous skills

**Agent handles (separately, in chat):**
- Running install commands from skill metadata
- `apt install ffmpeg`, `cargo install himalaya`, etc.
- User sees and approves these commands

This keeps dangerous operations (arbitrary SKILL.md content) gated through the tool, while keeping visible operations (binary installs) transparent in the conversation.

### Workflow Example

```
User: "install the himalaya email skill"

Agent calls: skills(action="install", skill="himalaya", from="clawhub")

Tool response:
{
  "installed": true,
  "name": "himalaya",
  "path": "/home/openclaw/.openclaw/workspace/goclaw/skills/himalaya/SKILL.md",
  "eligible": false,
  "missing": {"bins": ["himalaya"]},
  "install_hints": [
    {"kind": "cargo", "command": "cargo install himalaya"},
    {"kind": "brew", "command": "brew install himalaya"}
  ]
}

Agent: "Skill installed. It needs the himalaya binary. Want me to run:
       cargo install himalaya"

User: "yes"

Agent calls: exec("cargo install himalaya")

Agent calls: skills(action="reload")

Agent: "Done. himalaya skill is now eligible."
```

### Input

```json
{
  "action": "install",
  "skill": "himalaya",
  "from": "clawhub",
  "force": false
}
```

| Field | Type | Description |
|-------|------|-------------|
| skill | string | Skill name to install |
| from | string | Source: "clawhub", repo URL, or "local:/path" |
| force | bool | Overwrite existing skill (default: false) |

### Validation & Gating

Before writing a skill, the tool MUST:

1. **Check source trust**
   ```json
   {"error": "untrusted source", "source": "https://sketchy.site/skill.md", "hint": "Add to trusted sources or use --from clawhub"}
   ```

2. **Scan SKILL.md for dangerous patterns**
   - Embedded scripts that run on load
   - Environment variable exfiltration
   - Suspicious exec patterns
   - Obfuscated content
   
   ```json
   {"error": "dangerous pattern detected", "pattern": "env exfiltration", "line": 45}
   ```

3. **Check for conflicts**
   ```json
   {"error": "skill exists", "name": "himalaya", "hint": "Use force=true to overwrite"}
   ```

### Trusted Sources

Default trusted:
- `clawhub` — Official ClawHub registry (when API available)
- `local:*` — Local filesystem paths

Configurable in goclaw.json:
```json
{
  "skills": {
    "trustedSources": [
      "clawhub",
      "github.com/openclaw/*",
      "github.com/roelfdiedericks/*"
    ]
  }
}
```

### Output

**Success:**
```json
{
  "installed": true,
  "name": "himalaya",
  "path": "/path/to/skills/himalaya/SKILL.md",
  "eligible": false,
  "missing": {"bins": ["himalaya"]},
  "install_hints": [
    {"kind": "cargo", "command": "cargo install himalaya"}
  ]
}
```

**Failure:**
```json
{
  "installed": false,
  "error": "untrusted source",
  "source": "https://example.com/skill.md"
}
```

## Reload Action

Re-scan the skills directory and update the registry. Use after installing binaries.

```json
{"action": "reload"}
```

```json
{
  "reloaded": true,
  "previous": {"eligible": 11, "total": 55},
  "current": {"eligible": 12, "total": 55},
  "changes": [
    {"skill": "himalaya", "was": "ineligible", "now": "eligible"}
  ]
}
```

## Existing Infrastructure

- `internal/skills/installer.go` — Handles dependency installation (brew, go, uv, download). Node/npm blocked for security.
- `internal/skills/auditor.go` — Pattern scanning for dangerous content.
- `internal/skills/manager.go` — Singleton skill registry with load/reload capability.
- `internal/sandbox/sandbox.go` — `skills/` is in `writeProtectedDirs`, so the install action must write through the manager, not through file tools.

## Future Extensions

- `skills search <query>` — Fuzzy search skill names/descriptions
- `skills enable/disable <name>` — Runtime toggle without uninstalling
- `skills trust <source>` — Add to trusted sources
- `skills audit <name>` — Deep scan of skill for security review

## Priority

Medium — the query side works, this adds the write side.

## See Also

- [SKILLS_TOOL.md](attic/SKILLS_TOOL.md) — Original full spec (attic)
- [internal/skills/](../internal/skills/) — Skill loading implementation
- [internal/tools/skills/](../internal/tools/skills/) — Skills tool (query actions implemented)
