# Subagent Specification

## Overview

This document describes how GoClaw should think about subagents going forward.

The important update is that GoClaw already has a working delegated-agent pattern in production:

- isolated cron task execution
- explicit result routing (`store_only`, `deliver`, `handoff_main`)
- handoff from an isolated worker result into the main agent chain
- detached/background execution that is not tied to the triggering request lifecycle

Subagents should be designed as a generalization of that pattern, not as a separate architecture invented from scratch.

## Status

**Current reality**

- Cron is the first production implementation of isolated delegated work.
- `handoff_main` is the first production implementation of a second-stage main-agent decision on top of isolated task output.
- The delivery model is now explicit enough to reuse for future subagents.

**Not implemented yet**

- a user-facing subagent tool
- subagent registries
- progress streaming
- token/cost accounting per delegated run
- cancellation and lifecycle controls
- parallel subagent orchestration

## Why Cron Matters

Cron is no longer just a scheduler. It is the first real implementation of this execution pattern:

1. start an isolated agent run
2. let it do focused work in a fresh session
3. collect the final result
4. choose what happens to that result

That is the exact core needed for subagents.

## Current Mechanisms

### 1. Isolated execution

Cron runs isolated tasks in fresh sessions:

- session key pattern: `cron:<jobId>`
- fresh context
- independent task prompt
- own model-purpose routing

This is our current worker primitive.

### 2. Explicit result routing

Cron jobs already express what happens after the isolated run:

| Mode | Meaning |
|------|---------|
| `store_only` | Run task, persist if configured, do not send result anywhere |
| `deliver` | Run task, then deliver the final assistant result to channels |
| `handoff_main` | Run task, then hand its result to the main agent for a second-stage decision |

This is the first clean result-policy model in GoClaw.

### 3. Main-agent handoff

`handoff_main` now means:

- the isolated task produces a result
- that result has not been delivered yet
- the main agent chain receives the result
- the main agent decides whether to:
  - send a user-facing message
  - take follow-up tool actions
  - update files or memory
  - or intentionally return `SILENT_OK`

This is the first implementation of agent-to-agent delegation inside GoClaw.

## Mental Model

Subagents should be treated as generalized delegated runs:

```text
main agent
  -> spawn isolated worker
  -> worker performs task
  -> worker completes with result
  -> routing policy decides outcome
       - keep internal
       - deliver directly
       - hand to main agent
       - later: return to requester / coordinator
```

## Relationship to Future Subagents

### Cron vs Subagent

| Aspect | Cron | Future Subagent |
|--------|------|-----------------|
| Trigger | Timer / manual run | Main agent tool call |
| Context | Fresh session | Usually fresh session, sometimes forked context |
| Purpose | Scheduled automation | Delegated task execution |
| Result policy | Already implemented | Should reuse same idea |
| Requester | System / scheduler | Main agent / parent subagent / coordinator |

### Shared primitive

Both cron and future subagents should eventually rely on one shared isolated-run mechanism.

Conceptually:

```go
type DelegatedRun struct {
    ID              string
    Source          string // cron, subagent, worker, branch
    SessionKey      string
    Prompt          string
    Purpose         string
    FreshContext    bool
    TimeoutSeconds  int
    ResultMode      string
    Persist         *bool
    RequesterKey    string
    Label           string
}
```

Cron is simply one producer of `DelegatedRun`s.

Subagents would be another.

## Result Policies for Subagents

Subagents should not invent an unrelated result model.

The cron model should be the base:

- `store_only`
- `deliver`
- `handoff_main`

Future subagent work may add more specialized policies, such as:

- `return_to_requester`
  - inject or stream the result back to the spawning session
- `handoff_coordinator`
  - give result to a designated coordinator agent
- `stream_progress`
  - expose live updates while work is still running

But these should extend the same conceptual model, not replace it.

## Session Semantics

### Current delegated session shape

- cron sessions: `cron:<jobId>`

### Future subagent session shape

Recommended pattern:

- `agent:<agentId>:subagent:<uuid>`

Possible later variants:

- `agent:<agentId>:worker:<uuid>`
- `agent:<agentId>:branch:<uuid>`

The key is that delegated runs must have:

- stable identity
- isolated transcript/history
- ownership metadata
- cleanup policy

## What Subagents Need Beyond Cron

Cron gave us the core runner, but subagents need more plumbing.

### 1. Run registry

We need to track:

- run ID
- parent/requester
- current state (`queued`, `running`, `completed`, `failed`, `timeout`, `canceled`)
- start and finish times
- model used
- result mode

### 2. Progress and events

Unlike cron, subagents will need richer visibility:

- start event
- tool-use progress
- partial progress messages
- completion/failure event

This should reuse the assistant/system/event surface split where possible.

### 3. Token and cost accounting

Subagent runs should track:

- input tokens
- output tokens
- cache read/write tokens
- model/provider
- estimated cost

This will matter for:

- budgeting
- debugging
- future swarm coordination

### 4. Cancellation

Subagents should support:

- user cancel
- parent cancel
- timeout cancel

Cron mostly does not need rich interactive cancel semantics.
Subagents will.

### 5. Cleanup policy

Subagent sessions may need options such as:

- keep transcript
- delete on success
- delete on completion
- summarize then delete

## Swarm Direction

If GoClaw later grows into parallel agent swarms, the likely shape is:

1. main agent or coordinator spawns multiple delegated runs
2. each run executes in isolation
3. results report back with explicit metadata
4. a coordinator or main agent synthesizes the outputs

That means cron is effectively the simplest swarm member:

- one delegated run
- one result
- one routing policy

Future subagent swarms simply add:

- fan-out
- progress tracking
- aggregation
- coordinator logic

## Suggested Future Architecture

### Phase 1: Shared delegated runner

Extract a reusable internal delegated-run primitive that cron and subagents both use.

### Phase 2: Spawn tool

Add a subagent-spawn tool that:

- launches an isolated delegated run
- returns a run ID immediately
- records requester metadata

### Phase 3: Progress reporting

Expose background run state through:

- bus events
- session events
- possibly HTTP/TUI views later

### Phase 4: Coordinator patterns

Allow:

- one-to-one delegation
- one-to-many fan-out
- result aggregation

## Constraints

### No recursive chaos by default

Subagents should not recursively spawn unlimited subagents.

If nesting is allowed later, it should be:

- explicit
- depth-limited
- rate-limited

### Result-policy clarity

Every delegated run must answer:

1. what task is running?
2. where does the final result go?
3. should the result be persisted?

That is the main lesson from the cron refactor and should not be lost in future subagent work.

## Open Design Questions

- Should subagent handoff default to the main `agent` purpose or support dedicated per-subagent purposes?
- Should some subagents fork session history instead of always starting fresh?
- Should requester return paths use system-surface announcements or explicit assistant replies?
- How should progress be represented for channels like Telegram versus TUI/HTTP?
- How much run history should be persisted long-term?

## Recommendation

Treat cron as the reference implementation for delegated isolated work.

Do not design subagents as a separate special case.

Instead:

- reuse isolated execution
- reuse explicit result policies
- extend with requester tracking, progress events, cost tracking, cancellation, and coordination

## See Also

- `specs/PARALLEL_AGENTS.md` - older concurrency and worker ideas
- `specs/SUPPORT_AGENT.md` - role/task isolation in multi-session flows
- `specs/memory-extraction-coordination.md` - another example of background coordination pressure
