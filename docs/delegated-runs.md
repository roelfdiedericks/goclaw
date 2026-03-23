---
title: "Delegated Runs"
description: "Architecture and operational model for delegated runs, subagents, fanout, and result routing"
section: "About"
weight: 9
---

# Delegated Runs

Delegated runs are the shared execution model used by both:

- cron-isolated agent work
- owner-triggered subagent tools

This gives GoClaw one lifecycle/state system for background and nested agent work, instead of separate code paths.

## Why This Exists

Delegated runs provide:

- isolated execution contexts per run (`sessionKey`, timeout, purpose)
- consistent lifecycle tracking (`queued`, `running`, `completed`, `failed`, `timeout`, `canceled`)
- shared policy enforcement for depth, parent-child limits, and global lane concurrency
- predictable result routing back to requester/session/channel
- one control plane for HTTP/TUI/Telegram visibility

## Core Components

- `internal/delegatedrun`
  - core contracts (`RunSpec`, `RunRecord`, `RunResult`, `RunState`)
  - runner implementation (`DefaultRunner`) with bounded concurrency lane support
  - registry interface with memory + SQLite backing
  - typed emitters and bus projection (`delegated.run.*`)
- `internal/cron/service.go`
  - orchestrates delegated run startup/wait/cancel
  - centralizes spawn policy checks and lineage/depth validation
  - wires delegated execution into cron when `gateway.delegatedRuns.enabled=true`
- Tools
  - `subagent_spawn`, `subagent_status`, `subagent_cancel`, `subagent_fanout`
- HTTP control plane
  - `/runners`, `/api/runners`, `/api/runners/:runId`, `/api/runners/events`, cancel endpoint

## Lifecycle and State Machine

Typical delegated path:

1. `StartDelegatedRun(...)` validates policy and defaults.
2. Run is recorded as `queued`.
3. Runner lane admits run -> transitions to `running`.
4. Execution completes as one terminal state:
   - `completed`
   - `failed`
   - `timeout`
   - `canceled`
5. Registry/event log and bus events are updated for observers.

## Result Routing Semantics

Delegated runs snapshot result policy at spawn time and honor it on completion:

- `store_only`
- `deliver`
- `handoff_main`
- `return_to_requester`

For `return_to_requester`, GoClaw supports:

- session reinjection as synthetic `tool_use`/`tool_result`
- dispatch ordering (`queue_first`/`direct_first`)
- fallback mode (`none`/`queue_fallback`/`direct_fallback`)
- persisted idempotency keys (`completionDispatchKey`) and sequences
- dispatch-phase event logging for observability

## Policy and Safety Controls

Delegated policy controls include:

- `maxSpawnDepth`
- `maxActiveChildrenPerParent`
- `maxConcurrentRuns` (runner lane capacity, queued admission model)

Lineage behavior:

- explicit `parentRunId` is supported
- when omitted, subagent tooling propagates parent lineage from current session run context only if that run exists in delegated-run registry; otherwise it spawns as top-level

## Fanout Coordinator (`subagent_fanout`)

Fanout is implemented as a coordinator over delegated child runs:

- bounded `parallelism`
- deterministic aggregation ordered by input prompt index
- optional model-mediated synthesis (`synthesize=true`)
- synthesis guardrails:
  - bounded synthesis input size
  - truncation metadata (`truncated`, `includedItems`, `totalItems`)
  - independent synthesis timeout (`synthesisTimeoutSeconds`)

## Observability and Operations

Operator surfaces:

- HTTP runners dashboard with live SSE
- owner Telegram run lifecycle summaries
- TUI delegated run summary panel

Registry/event durability:

- memory registry for fast in-process state
- SQLite event/history support for restart-safe run history

## Configuration

Primary switches:

- `gateway.delegatedRuns.enabled`
- delegated run limits under `gateway.delegatedRuns.*`
- `tools.subagent.enabled`

Recommended baseline:

- `gateway.delegatedRuns.enabled=true`
- `tools.subagent.enabled=true`

Timeout policy knobs:

- `gateway.delegatedRuns.defaultTimeoutSeconds` sets a default when caller tools/jobs omit `timeoutSeconds`
- `gateway.delegatedRuns.maxTimeoutSeconds` caps excessively large delegated timeouts for safety

## Access Control

Delegated subagent tools are owner-only (`subagent_spawn`, `subagent_status`, `subagent_cancel`, `subagent_fanout`).

Enforcement is layered:

- registration gate: `tools.subagent.enabled`
- delegated engine gate: `gateway.delegatedRuns.enabled`
- runtime authorization gate: each subagent tool requires `sessionCtx.User.IsOwner()`

Subagent tools are only registered when **both** toggles are enabled.

So non-owner users cannot execute delegated subagent tools even if role tool lists include those names.

## See Also

- [Architecture](architecture.md)
- [Roles & Access Control](roles.md)
- [Tools](tools.md)
- [Tools: Cron](tools/cron.md)
- [Channels](channels.md)
