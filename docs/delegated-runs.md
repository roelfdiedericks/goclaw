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
- `return_to_requester`

For `return_to_requester`, GoClaw supports:

- session reinjection as synthetic `tool_use`/`tool_result`
- dispatch ordering (`queue_first`/`direct_first`)
- fallback mode (`none`/`queue_fallback`/`direct_fallback`)
- persisted idempotency keys (`completionDispatchKey`) and sequences
- dispatch-phase event logging for observability

Agent-facing defaults:

- `subagent_spawn` is async-first:
  - returns `runId` immediately
  - later completion callback is on by default
- `subagent_fanout` is immediate-result-first:
  - returns worker results in the current turn
  - later completion callback is off by default

Direct routing safety policy:

- if focused requester channel is unreachable, direct delivery is not retried
- fallback path is used deterministically (typically queue/session reinjection)
- completion remains discoverable in session/transcript history

Completion message contract:

- direct channel delivery uses human-readable completion text only
- internal requester reinjection stores structured payload with:
  - `schema=delegated_completion.v1`
  - `kind=task_completion`
  - `meta` (run/source/session/toolError/injectMode)
  - `replyInstruction` (internal orchestration hint)
  - `untrustedChildOutput` (bounded/truncated text payload)
- `replyInstruction` remains in structured payload but is not rendered in direct channel text

Requester binding model:

- each run persists requester binding metadata (focus state, reason, timestamps)
- operator controls are available via `subagent_status` actions (`focus`, `unfocus`)
- timer guards are disabled by default (manual focus/unfocus first)

Deferred completion wake behavior:

- when descendants are still active, completion dispatch defers immediately and stores `continuationWakeAt`
- shared wake scheduler (run-ID keyed) re-enters completion orchestration on wake
- repeated deferrals update wake metadata; latest scheduled wake replaces older pending timer for that run

## Policy and Safety Controls

Delegated policy controls include:

- `maxSpawnDepth`
- `maxActiveChildrenPerParent`
- `maxConcurrentRuns` (runner lane capacity, queued admission model)

Lineage behavior:

- explicit `parentRunId` is supported
- when omitted, subagent tooling propagates parent lineage from current session run context only if that run exists in delegated-run registry; otherwise it spawns as top-level

Purpose behavior:

- `purpose` is a freeform run metadata tag (dashboard/filter/logging)
- delegated subagent tooling uses the delegated/subagent model chain internally by default

## Fanout Coordinator (`subagent_fanout`)

Fanout is implemented as a coordinator over delegated child runs:

- bounded `parallelism`
- deterministic aggregation ordered by input prompt index
- default contract: return detailed child outputs to the caller in the current turn
- optional extra summary pass (`includeSummary=true`)
- extra summary guardrails:
  - bounded synthesis input size
  - complete summaries only: if the summary would only cover some worker outputs, GoClaw skips it and reports that in `extraSummaryStatus`
  - independent summary timeout (`summaryTimeoutSeconds`)
- session-derived inline budgeting:
  - uses current session context headroom plus the compaction reserve floor
  - returns as many complete child outputs inline as fit
  - when not all outputs fit, returns explicit overflow handles (`runId` values + inspect path) for the omitted children instead of silent preview-only truncation
- completion handoff no longer requires an extra summarizer model run:
  - completion summary text is built directly from deterministic fanout outcomes
  - if an optional extra summary exists, that text is incorporated into the final completion summary
  - async completion dispatch still uses the shared delegated completion pipeline

Delegated control actions:

- `subagent_status action="steer"` injects guidance into a delegated child session and triggers one agent turn
- `subagent_status action="send"` injects a ghostwritten assistant message into the delegated child session without triggering the model

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
- `llm.subagent.models` (dedicated subagent/delegated model chain)
- `llm.agent.models` (fallback chain when `llm.subagent.models` is empty)

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

