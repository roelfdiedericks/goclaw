# Cron Specification

## Overview

Cron is GoClaw's built-in scheduler for running tasks at specific times or intervals. Jobs are executed **immediately** when scheduled - no waiting for heartbeats.

**Key use cases:**
- "Run this every morning at 7am"
- "Remind me in 20 minutes"
- "Check for updates every 4 hours"
- "Send a weekly summary on Mondays"

**Key design decisions:**
- **Precise timing** - jobs run exactly when scheduled, not "sometime after"
- **No heartbeat dependency** - cron is standalone, calls `HandleAgentRequest` directly
- **Runs inside gateway** - goroutine, no HTTP needed
- **OpenClaw compatible** - reads `~/.openclaw/cron/jobs.json`

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        Gateway                               │
│  ┌─────────────┐                    ┌───────────────────┐   │
│  │ Cron        │───── timer ───────►│ Job Executor      │   │
│  │ Service     │                    │                   │   │
│  └─────────────┘                    └─────────┬─────────┘   │
│         │                                     │             │
│         ▼                                     ▼             │
│  ┌─────────────┐                    ┌───────────────────┐   │
│  │ Job Store   │                    │ HandleAgentRequest│   │
│  │ (JSON file) │                    │ (direct call)     │   │
│  └─────────────┘                    └───────────────────┘   │
│                                                             │
│  Channels: Telegram, TUI, etc. (also call HandleAgentRequest)│
└─────────────────────────────────────────────────────────────┘
```

Cron is just another "channel" that can invoke the agent - like Telegram or TUI, but triggered by time rather than user messages.

## Execution Model

**All jobs run immediately when their schedule fires:**

| Session Target | Session Key | Context | History |
|----------------|-------------|---------|---------|
| `isolated` | `cron:<jobId>` | Fresh (no prior messages) | Saved for inspection |
| `main` | `primary` | Full conversation history | Saved normally |

### FreshContext Flag

For isolated jobs, we don't inject prior session messages into the LLM context, but we still save the run to the session for later inspection.

```go
type AgentRequest struct {
    Source       string
    ChatID       string
    UserMsg      string
    FreshContext bool  // If true, don't load prior messages into context
}
```

### Job Execution

Cron follows the same pattern as Telegram and TUI - create an events channel, run the agent, process events:

```go
func (c *CronService) executeJob(job *CronJob) {
    startTime := time.Now()
    
    // Determine session key and context mode
    sessionKey := session.PrimarySession
    freshContext := false
    if job.SessionTarget == "isolated" {
        sessionKey = "cron:" + job.ID
        freshContext = true
    }
    
    // Build prompt
    prompt := job.Payload.Text
    if job.Payload.Message != "" {
        prompt = job.Payload.Message
    }
    formattedPrompt := fmt.Sprintf("[cron:%s %s] %s", job.ID, job.Name, prompt)
    
    // Build request
    req := gateway.AgentRequest{
        Source:       "cron",
        ChatID:       job.ID,
        UserMsg:      formattedPrompt,
        User:         c.gateway.Users().Owner(),  // Cron runs as owner
        FreshContext: freshContext,
    }
    
    // Run agent - same pattern as Telegram/TUI
    events := make(chan gateway.AgentEvent, 100)
    go func() {
        c.gateway.RunAgent(ctx, req, events)
    }()
    
    // Process events, capture final response
    var finalText string
    var runErr error
    for evt := range events {
        switch e := evt.(type) {
        case gateway.EventAgentEnd:
            finalText = e.FinalText
        case gateway.EventAgentError:
            runErr = fmt.Errorf(e.Error)
        // Could log tool events, progress, etc.
        }
    }
    
    // Update job state
    duration := time.Since(startTime)
    status := "ok"
    var errStr string
    if runErr != nil {
        status = "error"
        errStr = runErr.Error()
    }
    c.store.UpdateJobState(job.ID, CronJobState{
        LastRunAtMs:    timeToMs(startTime),
        LastStatus:     status,
        LastError:      errStr,
        LastDurationMs: duration.Milliseconds(),
        NextRunAtMs:    c.computeNextRunMs(job.Schedule, time.Now()),
    })
    
    // Deliver to channels if requested
    if job.Payload.Deliver && finalText != "" && runErr == nil {
        for _, ch := range c.gateway.Channels() {
            ch.Send(ctx, finalText)
        }
    }
    
    // Handle one-shot jobs
    if job.DeleteAfterRun && status == "ok" {
        c.store.DeleteJob(job.ID)
    } else if job.Schedule.Kind == "at" && status == "ok" {
        c.store.DisableJob(job.ID)
    }
}

### Why Main-Session Jobs Are Useful

Main-session jobs have access to conversation context:
- **Memory checkpoint** - knows what conversations to checkpoint
- **Digest/summary** - can reference recent discussions
- **Follow-up reminders** - context about what was discussed

### Legacy: `wakeMode`

OpenClaw uses `wakeMode` to control when jobs run:
- `next-heartbeat` - wait for next heartbeat (imprecise timing)
- `now` - trigger immediate execution

**GoClaw ignores `wakeMode`** - all jobs run immediately when scheduled. We read the field for compatibility but don't differentiate behavior. If you schedule something for 9am, it runs at 9am.

## Core Types

### Job ID

Each job has a UUID `id` field, generated when the job is created:

```json
{
  "id": "0ee9083a-5712-42d5-9a0b-162747c61851",
  "name": "Morning Brief",
  ...
}
```

The job ID is used for:
1. **Cron tool operations** - agent uses `jobId` to update/remove/run jobs
2. **Session key** - isolated jobs store their run history under `cron:<jobId>`

### CronJob (OpenClaw compatible)

```go
type CronJob struct {
    ID            string       `json:"id"`              // UUID, generated on create
    AgentID       string       `json:"agentId,omitempty"`
    Name          string       `json:"name"`
    Enabled       bool         `json:"enabled"`
    CreatedAtMs   int64        `json:"createdAtMs"`
    UpdatedAtMs   int64        `json:"updatedAtMs"`
    Schedule      CronSchedule `json:"schedule"`
    SessionTarget string       `json:"sessionTarget"`      // "main" or "isolated"
    WakeMode      string       `json:"wakeMode,omitempty"` // Legacy, ignored by GoClaw
    Payload       CronPayload  `json:"payload"`
    DeleteAfterRun bool        `json:"deleteAfterRun,omitempty"`
    State         CronJobState `json:"state"`
}

type CronJobState struct {
    NextRunAtMs    *int64 `json:"nextRunAtMs,omitempty"`
    LastRunAtMs    *int64 `json:"lastRunAtMs,omitempty"`
    LastStatus     string `json:"lastStatus,omitempty"`    // "ok", "error"
    LastError      string `json:"lastError,omitempty"`
    LastDurationMs int64  `json:"lastDurationMs,omitempty"`
}
```

### Schedules

```go
type CronSchedule struct {
    Kind    string `json:"kind"`              // "at", "every", "cron"
    AtMs    int64  `json:"atMs,omitempty"`    // for "at": unix ms timestamp
    EveryMs int64  `json:"everyMs,omitempty"` // for "every": interval in ms
    Expr    string `json:"expr,omitempty"`    // for "cron": cron expression
    Tz      string `json:"tz,omitempty"`      // for "cron": IANA timezone
}
```

| Kind | Description | Example |
|------|-------------|---------|
| `at` | One-shot at absolute time | `{"kind": "at", "atMs": 1738262400000}` |
| `every` | Recurring interval | `{"kind": "every", "everyMs": 3600000}` (hourly) |
| `cron` | Cron expression | `{"kind": "cron", "expr": "0 7 * * *", "tz": "Africa/Johannesburg"}` |

**Cron expression format:** Standard 5-field only:
```
minute  hour  day-of-month  month  day-of-week
  0      11        *          *         *
```

**Not supported:**
- 6-field (seconds) - overkill for our use cases
- Extended aliases (`@daily`, `@hourly`) - not needed
- Vixie-cron edge cases

**Recommended library:** [github.com/robfig/cron/v3](https://github.com/robfig/cron)

```go
parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
sched, err := parser.Parse("0 11 * * *")
next := sched.Next(time.Now())
```

### Payloads

```go
type CronPayload struct {
    Kind    string `json:"kind"`  // "systemEvent" or "agentTurn"
    
    // For systemEvent (typically main session)
    Text    string `json:"text,omitempty"`
    
    // For agentTurn (typically isolated session)
    Message          string `json:"message,omitempty"`
    Model            string `json:"model,omitempty"`
    Thinking         string `json:"thinking,omitempty"`
    TimeoutSeconds   int    `json:"timeoutSeconds,omitempty"`
    Deliver          bool   `json:"deliver,omitempty"`
    Channel          string `json:"channel,omitempty"`
    To               string `json:"to,omitempty"`
    BestEffortDeliver bool  `json:"bestEffortDeliver,omitempty"`
}
```

**Note:** While OpenClaw enforces `main` + `systemEvent` and `isolated` + `agentTurn` pairings, GoClaw is flexible - you can use either payload kind with either session target. We extract the prompt from `Text` or `Message` as appropriate.

## Storage

### Job Store

Jobs are stored in `~/.openclaw/cron/jobs.json` (OpenClaw compatible):

```json
{
  "version": 1,
  "jobs": [
    {
      "id": "uuid",
      "name": "Morning Brief",
      "enabled": true,
      "schedule": { "kind": "cron", "expr": "0 11 * * *", "tz": "Africa/Johannesburg" },
      "sessionTarget": "main",
      "payload": { "kind": "systemEvent", "text": "..." },
      "state": { "nextRunAtMs": 1770109200000 }
    }
  ]
}
```

**Why JSON not SQLite?**
- OpenClaw compatibility - reads/writes the same file
- Shared state when both GoClaw and OpenClaw are running
- Simple, human-readable, editable

### Run History

Run history is stored in `~/.openclaw/cron/runs/<jobId>.jsonl` (OpenClaw compatible):

```jsonl
{"ts":1770052827919,"status":"ok","durationMs":56491,"summary":"Weather checked. 3 calendar events found..."}
{"ts":1770060189033,"status":"error","durationMs":1200,"error":"timeout waiting for browser"}
```

**Entry fields:**
- `ts` - Unix timestamp (ms) when run started
- `status` - "ok" or "error"
- `durationMs` - How long the run took
- `summary` - Agent output, truncated to 2000 chars
- `error` - Error message if status is "error"

**Auto-pruning (same as OpenClaw):**
- Prune when file exceeds 2MB
- Keep last 2000 lines
- Self-managing, no external rotation needed

```go
func pruneRunLog(filePath string) error {
    const maxBytes = 2_000_000  // 2MB
    const keepLines = 2000
    
    info, err := os.Stat(filePath)
    if err != nil || info.Size() <= maxBytes {
        return nil  // No pruning needed
    }
    
    // Read, keep last N lines, rewrite
    // ...
}
```

## Scheduler Implementation

### Timer Approach (Recommended)

Instead of polling every N seconds, compute the next wake time and set a single timer:

```go
type CronService struct {
    store     *CronStore
    gateway   *Gateway
    timer     *time.Timer
    mu        sync.RWMutex
    running   map[string]bool  // jobs currently executing
    stopCh    chan struct{}
}

func (s *CronService) Start(ctx context.Context) {
    s.armTimer()
    
    go func() {
        for {
            select {
            case <-s.timer.C:
                s.runDueJobs()
                s.armTimer()  // Recompute next wake
            case <-s.stopCh:
                return
            case <-ctx.Done():
                return
            }
        }
    }()
}

func (s *CronService) armTimer() {
    jobs := s.store.GetEnabledJobs()
    
    var earliest *time.Time
    for _, job := range jobs {
        if next := s.computeNextRun(job); next != nil {
            if earliest == nil || next.Before(*earliest) {
                earliest = next
            }
        }
    }
    
    if earliest == nil {
        // No jobs scheduled - wake in 1 hour to check
        s.timer.Reset(time.Hour)
        return
    }
    
    delay := time.Until(*earliest)
    if delay < 0 {
        delay = 0
    }
    s.timer.Reset(delay)
}
```

### Job Execution

```go
func (s *CronService) runDueJobs() {
    now := time.Now()
    jobs := s.store.GetDueJobs(now)
    
    for _, job := range jobs {
        if s.isRunning(job.ID) {
            continue  // Skip if already running
        }
        go s.executeJob(job)
    }
}

func (s *CronService) executeJob(job *CronJob) {
    s.setRunning(job.ID, true)
    defer s.setRunning(job.ID, false)
    
    startTime := time.Now()
    
    // Determine session
    sessionKey := session.PrimarySession
    if job.SessionTarget == "isolated" {
        sessionKey = "cron:" + job.ID
    }
    
    // Build prompt
    prompt := job.Payload.Text
    if job.Payload.Message != "" {
        prompt = job.Payload.Message
    }
    formattedPrompt := fmt.Sprintf("[cron:%s %s] %s", job.ID, job.Name, prompt)
    
    // Execute via gateway
    err := s.gateway.HandleAgentRequest(context.Background(), AgentRequest{
        Source:  "cron",
        ChatID:  job.ID,
        UserMsg: formattedPrompt,
        // TODO: model/thinking overrides for isolated jobs
    })
    
    // Update state
    duration := time.Since(startTime)
    status := "ok"
    var errStr string
    if err != nil {
        status = "error"
        errStr = err.Error()
    }
    
    s.store.UpdateJobState(job.ID, CronJobState{
        LastRunAtMs:    timeToMs(startTime),
        LastStatus:     status,
        LastError:      errStr,
        LastDurationMs: duration.Milliseconds(),
        NextRunAtMs:    s.computeNextRunMs(job.Schedule, time.Now()),
    })
    
    // Handle one-shot jobs
    if job.DeleteAfterRun && status == "ok" {
        s.store.DeleteJob(job.ID)
    } else if job.Schedule.Kind == "at" && status == "ok" {
        // Disable completed one-shot jobs (don't delete for history)
        s.store.DisableJob(job.ID)
    }
}
```

### Computing Next Run

```go
func (s *CronService) computeNextRun(job *CronJob) *time.Time {
    now := time.Now()
    
    switch job.Schedule.Kind {
    case "at":
        at := time.UnixMilli(job.Schedule.AtMs)
        if at.After(now) {
            return &at
        }
        return nil  // Already passed
        
    case "every":
        interval := time.Duration(job.Schedule.EveryMs) * time.Millisecond
        // Compute next from last run or creation time
        var anchor time.Time
        if job.State.LastRunAtMs != nil {
            anchor = time.UnixMilli(*job.State.LastRunAtMs)
        } else {
            anchor = time.UnixMilli(job.CreatedAtMs)
        }
        next := anchor.Add(interval)
        for next.Before(now) {
            next = next.Add(interval)
        }
        return &next
        
    case "cron":
        parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
        sched, err := parser.Parse(job.Schedule.Expr)
        if err != nil {
            return nil
        }
        loc := time.Local
        if job.Schedule.Tz != "" {
            if l, err := time.LoadLocation(job.Schedule.Tz); err == nil {
                loc = l
            }
        }
        next := sched.Next(now.In(loc))
        return &next
    }
    return nil
}
```

## Cron Tool

The agent interacts with cron via a tool:

```json
{
  "name": "cron",
  "description": "Manage scheduled tasks",
  "input_schema": {
    "type": "object",
    "properties": {
      "action": {
        "type": "string",
        "enum": ["status", "list", "add", "update", "remove", "run", "runs"]
      },
      "jobId": { "type": "string" },
      "job": { "type": "object" },
      "patch": { "type": "object" },
      "includeDisabled": { "type": "boolean" }
    },
    "required": ["action"]
  }
}
```

### Actions

| Action | Description | Required |
|--------|-------------|----------|
| `status` | Scheduler status, job count, next run | - |
| `list` | All jobs (use `includeDisabled` for disabled) | - |
| `add` | Create job | `job` |
| `update` | Patch existing job | `jobId`, `patch` |
| `remove` | Delete job | `jobId` |
| `run` | Trigger job immediately | `jobId` |
| `runs` | Get run history | `jobId` |

## CLI Commands

```bash
# List jobs
goclaw cron list
goclaw cron list --all  # include disabled

# Add one-shot reminder
goclaw cron add \
  --name "Reminder" \
  --at "20m" \
  --session main \
  --text "Check the deployment"

# Add recurring job
goclaw cron add \
  --name "Daily summary" \
  --cron "0 9 * * *" \
  --tz "Africa/Johannesburg" \
  --session isolated \
  --message "Summarize overnight updates" \
  --deliver \
  --channel telegram

# Edit job
goclaw cron edit <jobId> --enabled=false

# Manual run
goclaw cron run <jobId>

# View history
goclaw cron runs <jobId> --limit 10

# Remove job
goclaw cron remove <jobId>

# Status
goclaw cron status
```

## Configuration

```json
{
  "cron": {
    "enabled": true,
    "jobsFile": "~/.openclaw/cron/jobs.json",
    "maxConcurrentJobs": 3,
    "runHistoryLimit": 100
  }
}
```

## File Structure

```
internal/cron/
├── service.go      # CronService (scheduler, timer, execution)
├── store.go        # JSON file operations
├── types.go        # CronJob, CronSchedule, CronPayload
├── schedule.go     # computeNextRun()
└── tool.go         # Cron tool implementation

cmd/goclaw/
├── cron.go         # CLI commands
```

## Implementation Phases

### Phase 1: Core Infrastructure
- [ ] CronJob types (OpenClaw compatible JSON structure)
- [ ] JSON store (read/write `~/.openclaw/cron/jobs.json`)
- [ ] `FreshContext` flag on `AgentRequest` (skip prior messages in context)
- [ ] `Channels()` method on Gateway to expose channel map for delivery

### Phase 2: Scheduler
- [ ] CronService with timer loop (goroutine in gateway)
- [ ] Schedule parsing (at, every, 5-field cron expressions)
- [ ] `computeNextRun()` implementation
- [ ] Job execution using events channel pattern (same as Telegram/TUI)
- [ ] Job state updates (lastStatus, lastError, nextRunAt)

### Phase 3: Tool & CLI
- [ ] Cron tool (status, list, add, update, remove, run, runs)
- [ ] CLI commands (`goclaw cron list/add/edit/remove/run/runs`)
- [ ] Human-friendly duration parsing ("20m", "2h")

### Phase 4: Run History & Delivery
- [ ] Run history logging (`~/.openclaw/cron/runs/<jobId>.jsonl`)
- [ ] Summary truncation (2000 chars)
- [ ] Log auto-pruning (2MB / 2000 lines)
- [ ] Channel delivery when `payload.deliver: true`
- [ ] One-shot job handling (disable after run)

## Resolved Design Decisions

1. **Execution pattern**: Cron uses the same events channel pattern as Telegram/TUI. No special variant needed.

2. **Delivery**: If `payload.deliver: true`, send response to **all channels** after agent completes. We don't implement channel-specific targeting for v1 - agent can use `message` tool if it needs to target specific channels.

3. **Model overrides**: Skip for v1. Use standard configured model.

4. **Concurrent execution**: Each job runs in its own goroutine. Jobs scheduled for the same time all run.

5. **Session isolation**: Use `FreshContext: true` flag. Agent starts with no prior messages, but run is saved to `cron:<jobId>` session for inspection.

6. **Error handling**: Fail immediately. Update `state.lastStatus = "error"` and `state.lastError`. Agent can check via cron tool.

7. **Run history storage**: JSONL files in `~/.openclaw/cron/runs/<jobId>.jsonl` (OpenClaw compatible). Includes truncated summary (2000 chars). Auto-prunes when file exceeds 2MB, keeps last 2000 lines.

8. **Cron expressions**: Standard 5-field only (minute, hour, dom, month, dow). No seconds field, no extended aliases.

9. **Inspecting isolated runs**: 
   - Run history includes `summary` field with truncated output
   - Full session stored under `cron:<jobId>` session key
   - Cron tool `runs` action returns history with summaries
   - For full conversation, could add future `session` action or use session tool directly

## See Also

- [Session Management](./SESSION_PERSISTENCE.md)
- [Gateway Architecture](../docs/architecture.md)
