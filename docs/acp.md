---
title: "ACP Sessions"
description: "Use GoClaw as an ACP client for Cursor sessions, prompts, and interactive workflows"
section: "Advanced"
weight: 25
---

# ACP Sessions

ACP lets GoClaw attach to an external agent session and route your chat turns through that session instead of the normal local LLM tool loop.

In the current MVP, GoClaw supports a local stdio ACP session with the Cursor driver. This is useful when you want to steer a live Cursor agent session, inspect its state, and handle Cursor interactive workflows from GoClaw's channels.

## Current Scope

GoClaw currently supports:

- the `cursor` ACP driver
- a local stdio transport
- session-scoped attachment through `/acp` commands
- agent-facing ACP control and inspection tools
- interactive Cursor extension flows surfaced in HTTP, Telegram, and TUI

ACP attachment is tied to the GoClaw session key, not to one specific UI tab or one specific chat transport. For owner sessions, that usually means the attached ACP session follows the shared `primary` session.

## Common Workflow

### 1. Attach a session

Attach a new Cursor ACP session from the current GoClaw session:

```bash
/acp attach
```

Attach with an explicit working directory or startup mode:

```bash
/acp attach cursor --cwd /path/to/project --mode agent
```

Re-attach to an existing ACP session ID:

```bash
/acp attach cursor --session SESSION_ID
```

### 2. Inspect or change mode

Check the current ACP attachment:

```bash
/acp status
```

Change the ACP mode when the driver supports it:

```bash
/acp mode plan
```

Typical modes used by Cursor are `agent`, `plan`, and `ask`.

### 3. Steer the attached session

Send a prompt into the attached ACP session:

```bash
/acp steer Review the current refactor and suggest the next safe step.
```

By default, `steer` detaches after the prompt completes so you can return to normal GoClaw chat naturally.

If you want to keep the ACP session attached for more back-and-forth turns, use:

```bash
/acp steer --stay-attached Ask the attached Cursor session to create a short plan.
```

### 4. Finish cleanly

Use these depending on what you want:

- `/acp detach` to stop routing this GoClaw session through ACP, while leaving the external ACP session available to re-attach later
- `/acp close` to close the ACP session entirely
- `/acp cancel` to cancel the currently running ACP prompt without closing the session

## Interactive Behavior

Cursor ACP sessions can emit interactive extension requests such as questions, plan approval flows, todo updates, delegated-task notices, and generated-asset notices.

GoClaw surfaces those differently by channel:

- **HTTP/Web UI** shows interactive cards for questions and plan approval, plus rendered status cards for todo, task, and generated-image events
- **Telegram** uses inline buttons for single-choice and approval flows, and native polls for single-question multi-select prompts
- **TUI** shows notice-only summaries so you can see what happened, but interactive responses must be handled elsewhere

If an interactive ACP prompt is waiting and you continue chatting instead of clicking the UI, GoClaw cancels the pending interactive request and treats your new message as the next turn. This avoids getting stuck behind a blocked prompt.

## Limitations

Current ACP support is intentionally narrow:

- only the Cursor driver is supported today
- only local stdio transport is supported today
- ACP-attached sessions do not yet support normal media or other content-block turns
- TUI currently mirrors interactive ACP events as notices rather than a full response UI

If you want to send images, audio, or other content-block input to GoClaw, detach from ACP first.

## User Commands vs Agent Tools

There are two ACP control surfaces:

- `/acp` slash commands are for you as the operator
- `acp_control` and `acp_inspect` are agent-facing tools used internally by the GoClaw agent

Use the slash commands for normal manual control. The agent tools are documented separately in [ACP Tools](tools/acp.md).

## See Also

- [Channel Commands](commands.md) — `/acp` command reference
- [ACP Tools](tools/acp.md) — Agent-facing ACP tool reference
- [Channels](channels.md) — How ACP events appear in each text channel
- [Architecture](architecture.md) — High-level ACP routing inside GoClaw
