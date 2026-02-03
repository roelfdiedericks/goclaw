# Subagent Tool Specification

## Overview

The `sessions_spawn` tool allows the main agent to spawn isolated sub-agent runs for delegated tasks. Subagents run in fresh sessions without conversation history, complete their task, and announce results back to the requester.

**Use cases:**
- Delegate research/analysis to a cheaper model
- Parallel background tasks
- Long-running work that shouldn't block main conversation
- Tasks that don't need conversation context

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                      Main Agent                              │
│                                                              │
│  "Research the history of XFS filesystem"                   │
│           │                                                  │
│           ▼                                                  │
│  ┌─────────────────┐                                        │
│  │ sessions_spawn  │                                        │
│  │ tool            │                                        │
│  └────────┬────────┘                                        │
│           │                                                  │
└───────────┼──────────────────────────────────────────────────┘
            │
            ▼
┌─────────────────────────────────────────────────────────────┐
│                    Subagent Runner                           │
│                                                              │
│  Session: agent:main:subagent:<uuid>                        │
│  Context: Fresh (no history)                                 │
│  Model: Can be different/cheaper                            │
│  Lane: Runs parallel to main                                │
│                                                              │
│  ┌─────────────────────────────────────────────────────┐    │
│  │ System prompt:                                       │    │
│  │ "You are a subagent spawned by the main agent.      │    │
│  │  Task: Research the history of XFS filesystem.       │    │
│  │  Report your findings concisely."                    │    │
│  └─────────────────────────────────────────────────────┘    │
│                                                              │
└───────────────────────────┬─────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│                   Result Announcement                        │
│                                                              │
│  → Main session receives: "[Subagent: research] Complete.   │
│     XFS was developed by SGI in 1993..."                    │
│                                                              │
│  → Optional: Deliver to Telegram/channels                   │
│  → Optional: Delete subagent session after                  │
└─────────────────────────────────────────────────────────────┘
```

## Relationship to Cron

Subagents share plumbing with isolated cron jobs:

| Aspect | Cron (isolated) | Subagent |
|--------|-----------------|----------|
| Session | `cron:<jobId>` | `agent:main:subagent:<uuid>` |
| Context | Fresh | Fresh |
| Trigger | Timer | Main agent tool call |
| Return | Deliver to channels | Announce to requester session |
| Cleanup | Disable after one-shot | Delete or keep |

**Implementation strategy:** Reuse the isolated runner from cron, add metadata for subagent-specific behavior (requester tracking, result announcement).

## Tool Definition

```json
{
  "name": "sessions_spawn",
  "description": "Spawn a background sub-agent run in an isolated session. Results are announced back when complete.",
  "input_schema": {
    "type": "object",
    "properties": {
      "task": {
        "type": "string",
        "description": "The task for the subagent to complete"
      },
      "label": {
        "type": "string",
        "description": "Optional label for tracking (e.g., 'research', 'analysis')"
      },
      "model": {
        "type": "string",
        "description": "Optional model override (e.g., 'claude-sonnet-4-20250514' for cheaper)"
      },
      "thinking": {
        "type": "string",
        "description": "Optional thinking level ('off', 'low', 'medium', 'high')"
      },
      "timeoutSeconds": {
        "type": "integer",
        "description": "Max runtime in seconds (0 = no limit)"
      },
      "cleanup": {
        "type": "string",
        "enum": ["delete", "keep"],
        "description": "Whether to delete subagent session after completion"
      }
    },
    "required": ["task"]
  }
}
```

## Session Keys

Subagent sessions follow the pattern:
```
agent:<agentId>:subagent:<uuid>
```

Example:
```
agent:main:subagent:a1b2c3d4-e5f6-7890-abcd-ef1234567890
```

## Subagent System Prompt

Subagents receive a special system prompt:

```
You are a subagent spawned by the main agent.

**Requester:** agent:main:main
**Session:** agent:main:subagent:abc123
**Label:** research

**Your task:**
Research the history of XFS filesystem and summarize key milestones.

**Instructions:**
- Focus only on the assigned task
- Be concise but thorough
- You cannot spawn additional subagents
- Your response will be announced back to the requester
```

## Execution Flow

### 1. Main Agent Spawns

```go
// Main agent calls sessions_spawn
result := tool.Execute("sessions_spawn", map[string]any{
    "task": "Research XFS history",
    "label": "research",
    "model": "claude-sonnet-4-20250514",
    "timeoutSeconds": 120,
    "cleanup": "delete",
})
// Returns immediately with:
// {"status": "accepted", "runId": "abc123", "sessionKey": "agent:main:subagent:..."}
```

### 2. Subagent Runs (Background)

```go
type SubagentRun struct {
    RunID            string
    SessionKey       string        // agent:main:subagent:<uuid>
    RequesterKey     string        // agent:main:main
    Task             string
    Label            string
    Model            string        // Optional override
    Thinking         string        // Optional override
    TimeoutSeconds   int
    Cleanup          string        // "delete" or "keep"
    StartedAt        time.Time
}

func (g *Gateway) runSubagent(run *SubagentRun) {
    // Build subagent system prompt
    systemPrompt := buildSubagentPrompt(run)
    
    // Run agent with fresh context
    result, err := g.RunAgent(ctx, AgentRequest{
        SessionKey:    run.SessionKey,
        Message:       run.Task,
        SystemPrompt:  systemPrompt,
        Model:         run.Model,
        Thinking:      run.Thinking,
        FreshContext:  true,  // No history
    })
    
    // Announce result back
    g.announceSubagentResult(run, result, err)
    
    // Cleanup if requested
    if run.Cleanup == "delete" {
        g.deleteSession(run.SessionKey)
    }
}
```

### 3. Result Announcement

Results are injected into the requester's session:

```go
func (g *Gateway) announceSubagentResult(run *SubagentRun, result string, err error) {
    var announcement string
    if err != nil {
        announcement = fmt.Sprintf("[Subagent: %s] Failed: %s", run.Label, err)
    } else {
        announcement = fmt.Sprintf("[Subagent: %s] Complete.\n\n%s", run.Label, result)
    }
    
    // Inject as system event into requester session
    g.injectSystemEvent(run.RequesterKey, announcement)
}
```

## Constraints

### Subagents Cannot Spawn Subagents

```go
func (t *SessionsSpawnTool) Execute(ctx context.Context, params map[string]any) (string, error) {
    sessionKey := getSessionKey(ctx)
    
    if isSubagentSession(sessionKey) {
        return jsonResult(map[string]any{
            "status": "forbidden",
            "error": "sessions_spawn is not allowed from sub-agent sessions",
        })
    }
    // ...
}

func isSubagentSession(key string) bool {
    return strings.Contains(key, ":subagent:")
}
```

### Agent Allowlist (Optional)

Config can restrict which agents are allowed:

```json
{
  "agents": {
    "defaults": {
      "subagents": {
        "allowAgents": ["main", "research"],
        "model": "claude-sonnet-4-20250514"
      }
    }
  }
}
```

## Registry

Track running subagents for monitoring and cleanup:

```go
type SubagentRegistry struct {
    mu   sync.RWMutex
    runs map[string]*SubagentRun  // runId -> run
}

func (r *SubagentRegistry) Register(run *SubagentRun)
func (r *SubagentRegistry) Complete(runId string, result string, err error)
func (r *SubagentRegistry) List() []*SubagentRun
func (r *SubagentRegistry) Get(runId string) *SubagentRun
```

## Storage

### Run History (Optional)

Log subagent runs for debugging:

```sql
CREATE TABLE subagent_runs (
    id TEXT PRIMARY KEY,
    session_key TEXT NOT NULL,
    requester_key TEXT NOT NULL,
    task TEXT NOT NULL,
    label TEXT,
    model TEXT,
    status TEXT,        -- "running", "completed", "failed", "timeout"
    result TEXT,
    error TEXT,
    started_at INTEGER NOT NULL,
    finished_at INTEGER,
    duration_ms INTEGER
);
```

## Integration with Cron

The isolated runner can be shared:

```go
// Unified isolated run
type IsolatedRun struct {
    ID            string
    SessionKey    string        // "cron:<id>" or "agent:main:subagent:<uuid>"
    Task          string
    Source        string        // "cron" or "subagent"
    RequesterKey  string        // For subagent: who to announce to
    Model         string
    Thinking      string
    Timeout       time.Duration
    Deliver       bool          // Cron: deliver to channels
    Cleanup       string        // "delete" | "keep" | "disable"
}

func (g *Gateway) RunIsolated(run *IsolatedRun) {
    // Same executor for both cron and subagent
    result, err := g.runWithFreshContext(run)
    
    switch run.Source {
    case "cron":
        if run.Deliver {
            g.deliverToChannels(result)
        }
        // Update cron job state
        
    case "subagent":
        g.announceSubagentResult(run, result, err)
        if run.Cleanup == "delete" {
            g.deleteSession(run.SessionKey)
        }
    }
}
```

## CLI (Optional)

```bash
# List running subagents
goclaw subagent list

# View subagent run details
goclaw subagent show <runId>

# Cancel running subagent
goclaw subagent cancel <runId>

# View history
goclaw subagent history --limit 10
```

## Implementation Phases

### Phase 1: Basic Spawn
- [ ] `sessions_spawn` tool definition
- [ ] Subagent session creation
- [ ] Fresh context execution
- [ ] Basic result announcement

### Phase 2: Registry & Tracking
- [ ] SubagentRegistry for active runs
- [ ] Timeout handling
- [ ] Run history logging

### Phase 3: Integration
- [ ] Share isolated runner with cron
- [ ] Model/thinking overrides
- [ ] Cleanup options

### Phase 4: Polish
- [ ] CLI commands
- [ ] Agent allowlist config
- [ ] Rate limiting (max concurrent subagents)

## Example Usage

**Main agent spawning a research task:**

```
User: "What's the history of XFS? Use a subagent to research it."

Agent: I'll spawn a subagent to research XFS history.

[Tool: sessions_spawn]
{
  "task": "Research the history of the XFS filesystem. Cover: origin, key developers, major milestones, adoption.",
  "label": "xfs-research",
  "model": "claude-sonnet-4-20250514",
  "timeoutSeconds": 120,
  "cleanup": "delete"
}

[Tool result]
{"status": "accepted", "runId": "abc123", "sessionKey": "agent:main:subagent:..."}

Agent: I've spawned a research subagent. I'll let you know when it completes.

... (subagent runs in background) ...

[System event]
[Subagent: xfs-research] Complete.

XFS was developed by Silicon Graphics (SGI) in 1993 for their IRIX operating system...

Agent: The research is complete! Here's what the subagent found:
[summarizes/presents results]
```

## See Also

- [CRON.md](./CRON.md) — Cron system (shares isolated runner)
- [SESSION_PERSISTENCE.md](./SESSION_PERSISTENCE.md) — Session management
