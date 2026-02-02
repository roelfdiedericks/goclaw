# Context Injection & Subagents Spec

## Overview

The agent's personality, knowledge, and behavior come from **workspace context files** that are injected into the system prompt. This is what makes Ratpup... Ratpup.

## Workspace Context Files

Located in `~/.openclaw/workspace/` (or custom workspace dir):

| File | Purpose | Main Agent | Subagent |
|------|---------|------------|----------|
| `AGENTS.md` | Core instructions — how to behave, tool usage patterns, workflows | ✅ | ✅ |
| `SOUL.md` | Personality, tone, vibe — "who you are" | ✅ | ❌ |
| `TOOLS.md` | Tool-specific notes — camera names, SSH hosts, API endpoints | ✅ | ✅ |
| `IDENTITY.md` | Agent identity — name, creation date, relationships | ✅ | ❌ |
| `USER.md` | User information — name, preferences, schedule, context | ✅ | ❌ |
| `HEARTBEAT.md` | Periodic tasks to check during heartbeat polls | ✅ | ❌ |
| `BOOTSTRAP.md` | First-run instructions (deleted after first session) | ✅ | ❌ |
| `MEMORY.md` | Long-term curated memories | ✅ | ❌ |

### File Descriptions

**AGENTS.md** — The operating manual. How to use tools, when to ask permission, memory patterns, group chat behavior. Shared with subagents because they need to know how to behave.

**SOUL.md** — The personality. Tone, vibe, communication style. "Be genuinely helpful, not performatively helpful." This is what makes the agent feel like a person, not a chatbot. Main agent only — subagents are workers, not friends.

**TOOLS.md** — Local environment notes. "Camera 'driveway' is the front camera." "SSH to home-server at 192.168.1.100." Shared with subagents because they may need to use tools.

**IDENTITY.md** — Who the agent is. Name, "born" date, relationships. "I'm Ratpup, RoDent's cyber-son." Main agent only — subagents don't need an identity.

**USER.md** — Who the user is. Name, preferences, schedule, work context. "RoDent is a night owl, no meetings before 11am." Main agent only — subagents shouldn't know personal details.

**HEARTBEAT.md** — Checklist for heartbeat polls. "Check email, check calendar." Main agent only — subagents don't do heartbeats.

**BOOTSTRAP.md** — First-run setup instructions. "Read this, figure out who you are, then delete me." Deleted after first session.

**MEMORY.md** — Curated long-term memories. Important events, lessons learned, user preferences. Main agent only — subagents are ephemeral.

## System Prompt Structure

The system prompt is built from multiple sections:

```
[Hardcoded sections]
- You are a personal assistant...
- Tooling: Tool availability list
- Safety: Don't pursue self-preservation, etc.
- Skills: Available skills with SKILL.md locations

[Workspace context]
## Project Context
The following project context files have been loaded:
If SOUL.md is present, embody its persona...

## AGENTS.md
[content]

## SOUL.md
[content]

## USER.md
[content]
...

[Runtime info]
## Runtime
model=claude-opus-4-5 | channel=telegram | ...
```

### Injection Order

1. **Core identity** — "You are a personal assistant running inside GoClaw"
2. **Tooling** — Available tools with descriptions
3. **Skills** — Skill discovery instructions
4. **Memory recall** — How to use memory_search
5. **Safety** — Constitutional AI principles
6. **Workspace files** — SOUL.md, USER.md, etc. (filtered by session type)
7. **Runtime** — Model, channel, capabilities

## What is a Subagent?

A **subagent** is an isolated session spawned by the main agent to do background work.

### Why Subagents Exist

1. **Parallel work** — Main agent can spawn a subagent to research something while continuing the conversation
2. **Isolation** — Some tasks shouldn't pollute the main session history
3. **Different models** — Subagent can use a cheaper/faster model for simple tasks
4. **Background processing** — Long-running tasks that report back when done

### How Subagents Work

```
User: "Research the history of Johannesburg and write a summary"

Main Agent thinks: "This will take a while, let me spawn a subagent"

Main Agent calls: sessions_spawn(task="Research Johannesburg history, write 500 word summary")

[Subagent session created]
[Subagent runs, does research, writes summary]
[Subagent completes]

Main Agent receives: "Subagent finished: [summary text]"
Main Agent replies to user with the summary
```

### Subagent Session Keys

```
Main session:     agent:main:main
Subagent:         agent:main:spawn:abc123
Another subagent: agent:main:spawn:def456
```

### Why Subagents Get Reduced Context

**Privacy:**
- USER.md contains personal information (schedule, preferences, relationships)
- A subagent spawned to "check if this code has bugs" shouldn't know your wife's name
- Principle of least privilege

**Security:**
- MEMORY.md may contain sensitive historical context
- Subagents are ephemeral workers, not trusted with full context
- If a subagent task involves external APIs, less context = less leak risk

**Efficiency:**
- Subagents don't need personality (SOUL.md)
- They don't need identity (IDENTITY.md)
- Smaller context = faster, cheaper

**Clarity:**
- Subagents are workers with a specific task
- They shouldn't be confused by personality instructions
- Clear role: "do this task, report back"

### What Subagents DO Get

```go
// Subagent context filter
var subagentAllowlist = []string{
    "AGENTS.md",  // How to behave, tool patterns
    "TOOLS.md",   // Environment-specific tool notes
}
```

**AGENTS.md** — They still need to know:
- How to use tools properly
- When to ask for confirmation
- Output formatting conventions

**TOOLS.md** — They still need to know:
- Camera names if they need to check cameras
- SSH hosts if they need to connect somewhere
- API endpoints for web tools

## Implementation

### Loading Workspace Files

```go
// internal/context/workspace.go

type WorkspaceFile struct {
    Name    string  // "SOUL.md"
    Path    string  // "/home/user/.openclaw/workspace/SOUL.md"
    Content string  // file contents (empty if missing)
    Missing bool    // true if file doesn't exist
}

var workspaceFiles = []string{
    "AGENTS.md",
    "SOUL.md",
    "TOOLS.md",
    "IDENTITY.md",
    "USER.md",
    "HEARTBEAT.md",
    "BOOTSTRAP.md",
    "MEMORY.md",
}

func LoadWorkspaceFiles(dir string) ([]WorkspaceFile, error) {
    var files []WorkspaceFile
    for _, name := range workspaceFiles {
        path := filepath.Join(dir, name)
        content, err := os.ReadFile(path)
        if err != nil {
            files = append(files, WorkspaceFile{
                Name: name, Path: path, Missing: true,
            })
        } else {
            files = append(files, WorkspaceFile{
                Name: name, Path: path, Content: string(content), Missing: false,
            })
        }
    }
    return files, nil
}
```

### Filtering for Subagents

```go
// internal/context/filter.go

var subagentAllowlist = map[string]bool{
    "AGENTS.md": true,
    "TOOLS.md":  true,
}

func FilterForSubagent(files []WorkspaceFile) []WorkspaceFile {
    var filtered []WorkspaceFile
    for _, f := range files {
        if subagentAllowlist[f.Name] {
            filtered = append(filtered, f)
        }
    }
    return filtered
}

func FilterForSession(files []WorkspaceFile, isSubagent bool) []WorkspaceFile {
    if isSubagent {
        return FilterForSubagent(files)
    }
    return files
}
```

### Building System Prompt

```go
// internal/context/prompt.go

type PromptParams struct {
    WorkspaceDir  string
    IsSubagent    bool
    Tools         []Tool
    Model         string
    Channel       string
    UserTimezone  string
}

func BuildSystemPrompt(params PromptParams) (string, error) {
    var sections []string
    
    // Core identity
    sections = append(sections, "You are a personal assistant running inside GoClaw.")
    
    // Tooling section
    sections = append(sections, buildToolingSection(params.Tools))
    
    // Safety section (skip for subagents to save tokens)
    if !params.IsSubagent {
        sections = append(sections, buildSafetySection())
    }
    
    // Load and filter workspace files
    files, _ := LoadWorkspaceFiles(params.WorkspaceDir)
    files = FilterForSession(files, params.IsSubagent)
    
    // Project context section
    sections = append(sections, "# Project Context")
    sections = append(sections, "The following project context files have been loaded:")
    if !params.IsSubagent {
        sections = append(sections, "If SOUL.md is present, embody its persona and tone.")
    }
    
    for _, f := range files {
        if f.Missing {
            sections = append(sections, fmt.Sprintf("## %s\n[MISSING] Expected at: %s", f.Name, f.Path))
        } else {
            sections = append(sections, fmt.Sprintf("## %s\n%s", f.Name, f.Content))
        }
    }
    
    // Runtime section
    sections = append(sections, buildRuntimeSection(params))
    
    return strings.Join(sections, "\n\n"), nil
}
```

### Session Type Detection

```go
// internal/session/types.go

func IsSubagentSession(key string) bool {
    // Subagent keys contain ":spawn:" 
    // e.g., "agent:main:spawn:abc123"
    return strings.Contains(key, ":spawn:")
}

// Or check by label prefix
func IsSubagentByLabel(label string) bool {
    return strings.HasPrefix(label, "subagent:")
}
```

## File Structure

```
internal/
├── context/
│   ├── workspace.go     # Load workspace files
│   ├── filter.go        # Filter for subagents
│   ├── prompt.go        # Build system prompt
│   └── sections.go      # Individual prompt sections
```

## MVP Scope

- [x] Load workspace files from directory
- [x] Build system prompt with file injection
- [x] Filter files for subagent sessions
- [ ] Subagent spawning (sessions_spawn tool) — post-MVP
- [ ] Template files for new workspaces — post-MVP

For MVP, focus on main agent context. Subagent spawning can come later, but the filtering logic should be in place.

## Examples

### Main Agent System Prompt (abbreviated)

```
You are a personal assistant running inside GoClaw.

## Tooling
Tool availability: read, write, edit, exec, web_search, web_fetch, browser...

## Safety
You have no independent goals...

# Project Context
If SOUL.md is present, embody its persona and tone.

## AGENTS.md
[full AGENTS.md content]

## SOUL.md
# SOUL.md - Who You Are
Be genuinely helpful, not performatively helpful...

## USER.md
# USER.md - About My Human
Name: RoDent, night owl, loves Golang...

## MEMORY.md
# MEMORY.md - Long-Term Memory
Lessons learned, important events...

## Runtime
model=claude-opus-4-5 | channel=telegram
```

### Subagent System Prompt (abbreviated)

```
You are a worker agent spawned to complete a specific task.

## Tooling
Tool availability: read, write, edit, exec, web_search, web_fetch...

# Project Context

## AGENTS.md
[full AGENTS.md content]

## TOOLS.md
[full TOOLS.md content]

## Runtime
model=claude-sonnet-4 | session=agent:main:spawn:abc123

## Task
Research the history of Johannesburg and write a 500 word summary.
```

Note: No SOUL.md, USER.md, MEMORY.md, IDENTITY.md. Just instructions and tool notes.
