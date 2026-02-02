# Skills Tool Specification

## Overview

A `skills` tool that exposes the runtime's skill registry to the agent. Reduces round-tripping when the agent needs to know what capabilities are available, why something is ineligible, or how to enable a skill.

## Motivation

Currently, to understand available skills, the agent must:
1. List skill directories
2. Read each SKILL.md file
3. Parse metadata for binary requirements
4. Run `which` on each binary
5. Cross-reference results

This works but is clunky (5+ tool calls). The runtime **already does this at startup** — the agent should be able to query that knowledge directly.

## Tool Definition

```json
{
  "name": "skills",
  "description": "Query the skills registry. List available skills, check eligibility, get details and install hints.",
  "input_schema": {
    "type": "object",
    "properties": {
      "action": {
        "type": "string",
        "enum": ["list", "info", "check"],
        "description": "Action to perform"
      },
      "skill": {
        "type": "string",
        "description": "Skill name (required for 'info' and 'check' actions)"
      },
      "filter": {
        "type": "string",
        "enum": ["all", "eligible", "ineligible"],
        "default": "all",
        "description": "Filter for 'list' action"
      },
      "verbose": {
        "type": "boolean",
        "default": false,
        "description": "Include full details in list output"
      }
    },
    "required": ["action"]
  }
}
```

## Actions

### `list` — List skills

Returns all skills known to the runtime with their eligibility status.

**Input:**
```json
{
  "action": "list",
  "filter": "eligible"
}
```

**Output:**
```json
{
  "count": 11,
  "skills": [
    {
      "name": "weather",
      "emoji": "🌤️",
      "description": "Get current weather and forecasts (no API key required)",
      "eligible": true
    },
    {
      "name": "discord",
      "emoji": "💬",
      "description": "Control Discord via the discord tool",
      "eligible": true
    }
  ]
}
```

**With `verbose: true`:**
```json
{
  "count": 11,
  "skills": [
    {
      "name": "weather",
      "emoji": "🌤️",
      "description": "Get current weather and forecasts",
      "eligible": true,
      "path": "/home/openclaw/.openclaw/workspace/goclaw/skills/weather/SKILL.md",
      "requires": {
        "bins": ["curl"],
        "env": [],
        "os": []
      }
    }
  ]
}
```

### `info` — Get skill details

Returns full information about a specific skill.

**Input:**
```json
{
  "action": "info",
  "skill": "himalaya"
}
```

**Output:**
```json
{
  "name": "himalaya",
  "emoji": "📧",
  "description": "CLI to manage emails via IMAP/SMTP",
  "eligible": false,
  "path": "/home/openclaw/.openclaw/workspace/goclaw/skills/himalaya/SKILL.md",
  "requires": {
    "bins": ["himalaya"],
    "env": [],
    "os": []
  },
  "missing": {
    "bins": ["himalaya"],
    "env": [],
    "os": []
  },
  "install": [
    {
      "id": "cargo",
      "kind": "cargo",
      "crate": "himalaya",
      "label": "Install via cargo"
    },
    {
      "id": "brew",
      "kind": "brew", 
      "formula": "himalaya",
      "label": "Install via Homebrew"
    }
  ]
}
```

### `check` — Check why skill is ineligible

Focused output explaining why a skill can't be used and how to fix it.

**Input:**
```json
{
  "action": "check",
  "skill": "video-frames"
}
```

**Output (ineligible):**
```json
{
  "name": "video-frames",
  "eligible": false,
  "reasons": [
    "Missing binary: ffmpeg"
  ],
  "fixes": [
    "apt install ffmpeg",
    "brew install ffmpeg"
  ]
}
```

**Output (eligible):**
```json
{
  "name": "video-frames",
  "eligible": true,
  "reasons": [],
  "fixes": []
}
```

**Output (OS mismatch):**
```json
{
  "name": "apple-notes",
  "eligible": false,
  "reasons": [
    "Requires macOS (current: linux)"
  ],
  "fixes": []
}
```

## Implementation Notes

### Data Source

GoClaw already loads and filters skills at startup (`internal/skills/loader.go`). The tool just needs to expose the existing `SkillRegistry`:

```go
type SkillRegistry struct {
    All        map[string]*Skill
    Eligible   map[string]*Skill
    Ineligible map[string]*Skill
}

type Skill struct {
    Name        string
    Description string
    Emoji       string
    Path        string
    Requires    Requirements
    Install     []InstallOption
    Eligible    bool
    Missing     Requirements  // What's preventing eligibility
}
```

### Tool Registration

```go
// internal/tools/skills.go
func NewSkillsTool(registry *skills.SkillRegistry) tools.Tool {
    return &SkillsTool{registry: registry}
}

func (t *SkillsTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
    var params struct {
        Action  string `json:"action"`
        Skill   string `json:"skill"`
        Filter  string `json:"filter"`
        Verbose bool   `json:"verbose"`
    }
    // ...
}
```

### Error Cases

| Condition | Response |
|-----------|----------|
| Unknown skill name | `{"error": "skill not found: foo"}` |
| Missing required param | `{"error": "skill name required for 'info' action"}` |
| Invalid action | `{"error": "unknown action: foo"}` |

## Benefits

1. **Single tool call** instead of 5+ for skill discovery
2. **Consistent with runtime** — shows exactly what GoClaw sees
3. **Install hints** — agent can suggest how to enable skills
4. **OS awareness** — explains darwin-only skills on Linux
5. **Structured output** — easy to parse and format for user

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

## Future Extensions

- `skills search <query>` — Fuzzy search skill names/descriptions
- `skills enable/disable <name>` — Runtime toggle without uninstalling
- `skills trust <source>` — Add to trusted sources
- `skills audit <name>` — Deep scan of skill for security review

## Priority

Medium — useful quality-of-life improvement, not blocking. The agent *can* figure this out manually, it's just slower and clunkier.

## See Also

- [Architecture](../docs/architecture.md) — System overview
- [internal/skills/](../internal/skills/) — Skill loading implementation
