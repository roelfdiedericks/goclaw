# GoClaw Skills System

GoClaw implements a skills system compatible with OpenClaw. Skills are markdown files that extend the agent's capabilities by providing domain-specific knowledge and instructions.

## Overview

Skills are loaded from multiple directories in precedence order (lowest to highest):

| Priority | Source | Path | Purpose |
|----------|--------|------|---------|
| Lowest | Extra | Config `extraDirs` | Additional directories |
| Low | Bundled | `<goclaw>/skills/` | Ships with GoClaw |
| Medium | Managed | `~/.openclaw/skills/` | User-installed via clawhub |
| Highest | Workspace | `<workspace>/skills/` | Project-specific |

Higher precedence skills override lower ones with the same name.

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

| Kind | Description | Supported |
|------|-------------|-----------|
| `brew` | Homebrew formula | Yes |
| `go` | Go module | Yes |
| `uv` | Python uv tool | Yes |
| `download` | Direct download | Yes |
| `node` | npm/pnpm/yarn | **BLOCKED** |

Node.js installation is blocked for security reasons. Install npm packages manually.

## Configuration

Add to `goclaw.json`:

```json
{
  "skills": {
    "enabled": true,
    "bundledDir": "",
    "managedDir": "",
    "workspaceDir": "",
    "extraDirs": [],
    "watch": true,
    "watchDebounceMs": 500,
    "entries": {
      "skill-name": {
        "enabled": true,
        "apiKey": "...",
        "env": {
          "VAR": "value"
        },
        "config": {}
      }
    }
  }
}
```

### Options

| Option | Default | Description |
|--------|---------|-------------|
| `enabled` | `true` | Enable/disable skills system |
| `bundledDir` | `<exe>/skills/` | Override bundled skills path |
| `managedDir` | `~/.openclaw/skills/` | Override managed skills path |
| `workspaceDir` | `<workspace>/skills/` | Override workspace skills path |
| `extraDirs` | `[]` | Additional skill directories |
| `watch` | `true` | Watch for file changes |
| `watchDebounceMs` | `500` | Debounce interval for changes |
| `entries` | `{}` | Per-skill configuration |

### Per-Skill Config

Override settings for specific skills:

```json
{
  "skills": {
    "entries": {
      "discord": {
        "enabled": true,
        "apiKey": "your-discord-token"
      },
      "suspicious-skill": {
        "enabled": false
      }
    }
  }
}
```

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
  "count": 11,
  "filter": "eligible",
  "skills": [
    {
      "name": "weather",
      "emoji": "🌤️",
      "description": "Get current weather and forecasts",
      "status": "eligible",
      "source": "bundled"
    }
  ]
}
```

With `verbose: true`, includes `path` and `requires` fields.

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

### Future Extensions

Future versions may support:
- `search` - Search skill registries (clawdhub.com, etc.)
- `install` - Install skills from approved registries with audit

## Syncing Bundled Skills

GoClaw bundled skills are synced from OpenClaw. To update:

```bash
make skills-update
```

To check for differences without updating:

```bash
make skills-check
```

This uses git sparse checkout to fetch only the `skills/` directory from the OpenClaw repository.

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
