---
title: "Skills"
description: "Extend agent capabilities with domain-specific knowledge"
section: "Advanced"
weight: 20
---

# GoClaw Skills System

GoClaw implements a skills system compatible with OpenClaw. Skills are markdown files that extend the agent's capabilities by providing domain-specific knowledge and instructions.

## Overview

Skills are loaded from configured directories in precedence order:

| Priority | Source | Path | Purpose |
|----------|--------|------|---------|
| Lower | Extra | Config `extraDirs` | Additional directories (power user) |
| Higher | Workspace | `<workspace>/skills/` | Installed and project-specific skills |

Higher precedence skills override lower ones with the same name.

Skills from the embedded catalog (bundled with GoClaw) can be installed to the workspace using the `skills` tool's `install` action.

## Skill Format

Each skill is a directory containing a `SKILL.md` file:

```
skills/
├── weather/
│   └── SKILL.md
├── discord/
│   └── SKILL.md
└── ...
```

### SKILL.md Structure

```markdown
---
name: My Skill
description: Short description of what this skill does
metadata:
  openclaw:
    emoji: "🔧"
    os: ["darwin", "linux"]
    requires:
      bins: ["mytool"]
      env: ["MY_API_KEY"]
    install:
      - kind: brew
        formula: mytool
---

# My Skill

Instructions for the agent on how to use this skill...
```

### Metadata Fields

- `emoji` - Display emoji for the skill
- `os` - Supported operating systems (`darwin`, `linux`, `windows`)
- `requires.bins` - Required binaries (all must exist)
- `requires.anyBins` - Required binaries (at least one must exist)
- `requires.env` - Required environment variables
- `requires.config` - Required config keys
- `install` - Installation options

### Install Kinds

| Kind | Description | Behavior |
|------|-------------|----------|
| `brew` | Homebrew formula | Auto-install |
| `go` | Go module (`go install`) | Auto-install |
| `uv` | Python uv tool | Auto-install |
| `download` | Direct download URL | Manual (shows URL) |
| `npm`/`node` | npm package | Manual (shows command) |
| `pnpm` | pnpm package | Manual (shows command) |
| `yarn` | yarn package | Manual (shows command) |

- **Auto-install**: GoClaw runs the install command automatically
- **Manual**: Returns the install command; agent runs it via `exec` if needed

## Configuration

Add to `goclaw.json`:

```json
{
  "skills": {
    "enabled": true,
    "workspaceDir": "",
    "extraDirs": [],
    "watch": true,
    "watchDebounceMs": 500,
    "install": {
      "allowEmbedded": true,
      "allowClawHub": false,
      "allowLocal": true
    }
  }
}
```

### Options

| Option | Default | Description |
|--------|---------|-------------|
| `enabled` | `true` | Enable/disable skills system |
| `workspaceDir` | `<workspace>/skills/` | Override workspace skills path |
| `extraDirs` | `[]` | Additional skill directories |
| `watch` | `true` | Watch for file changes |
| `watchDebounceMs` | `500` | Debounce interval for changes |
| `install` | See below | Installation source configuration |
| `entries` | `{}` | Whitelist flagged skills (see below) |

### Whitelisting Flagged Skills

When the security auditor flags a skill, it's disabled by default. If you trust the skill, whitelist it:

```json
{
  "skills": {
    "entries": {
      "flagged-but-trusted": {
        "enabled": true
      }
    }
  }
}
```

This is the only use case for per-skill config. To remove an unwanted skill, simply uninstall it rather than disabling via config.

## Security Auditor

GoClaw scans skill content for security concerns:

### Detected Patterns

- `.env`, `.credentials`, `.secrets` file references
- `curl | bash`, `wget | sh` patterns
- External webhook URLs (`webhook.site`, etc.)
- References to `~/.ssh`, `~/.aws`
- Long base64-encoded content

### Behavior

When suspicious patterns are found:

1. **Skill is disabled by default**
2. **Warning logged** with details
3. **Shown in `/status`** output
4. **Must be explicitly enabled** in config

Example warning:
```
Security Warning: Skill "suspicious-skill" has been flagged and disabled.
Found 2 security concern(s):
  - Line 45 [warn]: References .env file (match: ~/.env)
  - Line 67 [critical]: External webhook URL (match: webhook.site)

To enable: add to goclaw.json: {"skills":{"entries":{"suspicious-skill":{"enabled":true}}}}
```

## Eligibility

A skill is eligible if:

1. **OS matches** - `runtime.GOOS` is in the skill's `os` list (or list is empty)
2. **Binaries exist** - All `requires.bins` are in PATH
3. **Any binary exists** - At least one `requires.anyBins` is in PATH
4. **Env vars set** - All `requires.env` are defined
5. **Not disabled** - Not set to `enabled: false` in config
6. **Passes audit** - No security warnings (or explicitly enabled)

## Commands

### /status

Shows skill statistics and any flagged skills:

```
Skills: 54 total, 12 eligible
Flagged Skills: 1
  - suspicious-skill (disabled): env_file, webhook_site
```

### /skills

Lists all skills with their status. See the Skills Tool section below.

## Skills Tool

The `skills` tool provides structured access to the skills registry for the agent. This is the preferred way to query skills programmatically rather than manually reading SKILL.md files.

### Actions

#### list

List all skills with optional filtering.

**Input:**
```json
{
  "action": "list",
  "filter": "eligible",
  "verbose": false
}
```

**Filters:** `all`, `eligible`, `ineligible`, `flagged`, `whitelisted`

**Output:**
```json
{
  "summary": {
    "total": 55,
    "eligible": 11,
    "ineligible": 43,
    "flagged": 1,
    "whitelisted": 0
  },
  "filter": "eligible",
  "count": 11,
  "skills": [
    {
      "name": "weather",
      "emoji": "🌤️",
      "description": "Get current weather and forecasts",
      "status": "eligible",
      "source": "bundled"
    },
    {
      "name": "session-logs",
      "status": "ineligible",
      "reasons": ["binary: rg"],
      "missing": {"bins": ["rg"]},
      "source": "bundled"
    },
    {
      "name": "canvas",
      "status": "flagged",
      "reasons": ["Security flag: outside_workspace (warn)"],
      "source": "bundled"
    }
  ]
}
```

Notes:
- `summary` provides totals across all skills regardless of filter
- `reasons` included for ineligible/flagged skills
- `missing` shows which specific bins/env are not found
- With `verbose: true`, also includes `path` and `requires` fields

#### info

Get detailed information about a specific skill.

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
  "status": "ineligible",
  "path": "/home/user/.openclaw/workspace/goclaw/skills/himalaya/SKILL.md",
  "source": "bundled",
  "requires": {
    "bins": ["himalaya"]
  },
  "missing": ["binary: himalaya"],
  "install": [
    {
      "id": "brew",
      "kind": "brew",
      "label": "Install via Homebrew",
      "command": "brew install himalaya"
    }
  ]
}
```

#### check

Check why a skill is ineligible and get fix suggestions.

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
  "reasons": ["binary: ffmpeg"],
  "fixes": ["brew install ffmpeg", "apt install ffmpeg"]
}
```

**Output (flagged):**
```json
{
  "name": "suspicious-skill",
  "eligible": false,
  "reasons": ["Security flag: env_file (warn)"],
  "fixes": ["{\"skills\":{\"entries\":{\"suspicious-skill\":{\"enabled\":true}}}}"]
}
```

### Status Values

- `eligible` - Ready to use
- `ineligible` - Missing requirements (binaries, env vars, wrong OS)
- `flagged` - Disabled by security auditor
- `whitelisted` - Manually enabled despite audit flags

#### search

Search available skills from all sources (embedded, clawhub, local).

**Input:**
```json
{
  "action": "search",
  "query": "email"
}
```

**Output:**
```json
{
  "results": [
    {
      "name": "himalaya",
      "description": "CLI to manage emails via IMAP/SMTP",
      "source": "embedded",
      "installed": false
    }
  ]
}
```

#### install

Install a skill from a source.

**Input:**
```json
{
  "action": "install",
  "skill": "himalaya",
  "source": "embedded"
}
```

**Sources:** `embedded` (bundled with GoClaw), `clawhub` (remote registry - not yet implemented), `local` (workspace path)

**Output:**
```json
{
  "success": true,
  "skill": "himalaya",
  "path": "/home/user/.goclaw/skills/himalaya/SKILL.md"
}
```

**Note:** Installation is controlled by `skills.install` config:

```json
{
  "skills": {
    "install": {
      "allowEmbedded": true,
      "allowClawHub": false,
      "allowLocal": true
    }
  }
}
```

#### sources

List available skill sources/repositories.

**Input:**
```json
{
  "action": "sources"
}
```

**Output:**
```json
{
  "sources": [
    {"name": "embedded", "enabled": true, "count": 55},
    {"name": "clawhub", "enabled": false, "url": "https://clawhub.com", "note": "not yet implemented"},
    {"name": "local", "enabled": true, "path": "~/.goclaw/workspace/skills"}
  ]
}
```

#### reload

Rescan skill directories and refresh the registry.

**Input:**
```json
{
  "action": "reload"
}
```

Use after manually adding skills to directories.

## Embedded Catalog (Developer)

GoClaw ships with an embedded catalog of skills from OpenClaw. For GoClaw developers:

```bash
# Sync skills from OpenClaw repository
make skills-update

# Check for differences without updating
make skills-check
```

End users install skills from the embedded catalog using the `skills` tool's `install` action.

## Troubleshooting

### Skill not appearing

1. Check the skill directory has a `SKILL.md` file
2. Check eligibility requirements (`bins`, `env`, `os`)
3. Check if skill was flagged by auditor (`/status`)
4. Check logs for parsing errors

### Skill disabled by auditor

1. Review the security warnings
2. If the skill is safe, explicitly enable it:
   ```json
   {
     "skills": {
       "entries": {
         "skill-name": { "enabled": true }
       }
     }
   }
   ```

### Parsing errors

Some skills may have non-standard YAML frontmatter. GoClaw handles:
- Missing frontmatter (uses directory name)
- Unquoted colons in values
- Both YAML map and JSON string metadata

If a skill still fails to parse, check the logs for details.
