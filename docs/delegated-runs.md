---
title: "Delegated Runs"
description: "Use subagents and fanout to delegate work, monitor runs, and manage background task results"
section: "About"
weight: 9
---

# Delegated Runs

GoClaw lets you delegate work to **subagents**: isolated worker agents that can do a task for you in the background or in parallel.

If you have used `subagent_spawn` or `subagent_fanout`, you are already using delegated runs. Internally GoClaw tracks them all in one system, but the user-facing idea is simple:

- `subagent_spawn` starts one worker
- `subagent_fanout` starts several workers in parallel
- `subagent_status` lets you inspect or steer them
- `subagent_cancel` stops them

## What Subagents Are

Subagents are useful when a task should be split away from the main conversation:

- long-running research
- background follow-up work
- parallel reading or investigation across several files/sources
- work you may want to inspect or cancel later

Each subagent gets its own isolated run, its own run ID, and its own timeout/limits.

## Spawn vs Fanout

Use `subagent_spawn` when:

- you want one worker
- you want to keep going now and hear back later
- you want a run ID immediately

Default behavior:

- returns a `runId` immediately
- sends a later completion callback by default

Use `subagent_fanout` when:

- you want several workers in parallel
- you want their results back in the current turn
- you want to compare or interpret the worker outputs yourself

Default behavior:

- returns worker results in the current turn
- does **not** send a later completion callback unless you ask for it

## How Results Come Back

`subagent_spawn` and `subagent_fanout` return results differently on purpose.

### `subagent_spawn`

`subagent_spawn` is for work that should continue separately.

Typical flow:

1. Start a worker
2. Get a `runId` immediately
3. Receive a later completion callback when it finishes

### `subagent_fanout`

`subagent_fanout` is for parallel work that you want back now.

Typical flow:

1. Start several workers in parallel
2. Wait for them to finish
3. Get their results in the same turn

By default, `subagent_fanout` tries to return the worker outputs inline.

If everything fits:

- the worker results come back directly in the tool result

If not everything fits:

- GoClaw returns as many complete results as fit
- the rest come back as explicit `runId` handles in `overflow`
- use `subagent_status action=info` with those `runId` values to inspect the missing results

## What `ok=false` Means In Fanout

For `subagent_fanout`, a tool result can return partial work and still tell you that the overall fanout did **not** fully succeed.

If `ok=false`, treat that as a real failure or partial failure.

Read:

- `status`
- `message`
- `stats`

This means one or more worker runs:

- failed
- timed out
- were canceled
- or failed to start

You may still get useful child outputs back, but the fanout should not be treated as fully successful.

## Optional Extra Summary

`subagent_fanout` supports `includeSummary=true` for one extra machine-generated summary across the worker outputs.

This summary is secondary. The main result is still the worker outputs themselves.

GoClaw only returns `extraSummary` when:

- the worker outcomes were healthy
- and the summary covered all worker outputs

If not, GoClaw skips it and explains why in `extraSummaryStatus`.

This makes the rule simple for agents:

- if `extraSummary` exists, it is complete enough to use
- if it was skipped, use the main worker results instead

## Monitor And Control Subagents

You can monitor and control subagents in a few ways.

### Web UI

The runners page at `:1337/runners` shows:

- active and recent runs
- parent/child relationships
- structured run detail
- live runner events

This is useful when you want to see what workers are doing, inspect a run, or cancel active work.

### `subagent_status`

Use `subagent_status` to:

- list workers
- inspect one worker by `runId`
- fetch a stored result with `action=info`
- read event logs with `action=log`
- steer a running worker
- send a message into a running worker session

### `subagent_cancel`

Use `subagent_cancel` to stop work:

- `self` stops one run
- `subtree` also stops its child runs

## Timeouts And Limits

Subagents are not unlimited. GoClaw applies safety limits to keep delegated work from running away.

Examples include:

- timeout limits
- maximum parallel delegated runs
- parent/child depth and fanout limits

If a worker exceeds its time limit, it can time out. If a fanout hits limits or unhealthy child outcomes, the returned status reflects that.

## Configuration

The main switches are:

- `gateway.delegatedRuns.enabled`
- `tools.subagent.enabled`

Useful limit controls live under:

- `gateway.delegatedRuns.*`

Useful timeout controls include:

- `gateway.delegatedRuns.defaultTimeoutSeconds`
- `gateway.delegatedRuns.maxTimeoutSeconds`

If you want subagents available at all, both delegated runs and subagent tools need to be enabled.

## Access Control

Subagent tools are owner-only:

- `subagent_spawn`
- `subagent_status`
- `subagent_cancel`
- `subagent_fanout`

If these tools do not appear, check that:

- `gateway.delegatedRuns.enabled=true`
- `tools.subagent.enabled=true`

## When To Use This

Use subagents when:

- the task is large enough to split off
- you want parallel workers
- you want a run you can inspect or cancel later

Stay in the main agent turn when:

- the work is small
- you do not need a separate run
- parallelism would not help

## Next Steps

- See [Tools](tools.md) for the tool index
- See [Tools: Cron](tools/cron.md) for scheduled background work
- See [Channels](channels.md) for where delegated results can show up
- See [Roles & Access Control](roles.md) for owner-only tool access

## See Also

- [Architecture](architecture.md)
- [Roles & Access Control](roles.md)
- [Tools](tools.md)
- [Tools: Cron](tools/cron.md)
- [Channels](channels.md)

