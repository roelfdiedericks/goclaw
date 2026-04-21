---
title: "Session Management"
description: "Context management and compaction to stay within LLM token limits"
section: "Advanced"
weight: 10
---

# Session Management

GoClaw manages conversation context to stay within LLM token limits while preserving important information.

Compaction no longer means "old context is gone." With Lossless Context Management (LCM), compacted history is stored as searchable summaries with links back to the raw messages they covered.

## Overview

```
┌─────────────────────────────────────────────────────────────┐
│                    Context Window (200k)                     │
├─────────────────────────────────────────────────────────────┤
│ System Prompt │ LCM Summaries      │ Fresh Tail Messages    │
│    (~10k)     │   (recall cues)    │    (remaining)         │
└─────────────────────────────────────────────────────────────┘
```

As conversation grows, GoClaw:
1. **Monitors** context usage (token count vs max)
2. **Checkpoints** (optional) — Takes rolling snapshots at thresholds
3. **Compacts** (required) — Truncates old messages when nearly full
4. **Preserves recall** (default) — Keeps compacted history drillable through summary recall tools, with a budget-fit frontier injected into prompts by default

---

## Checkpoints

Checkpoints are **rolling snapshots** of conversation state. They do NOT delete messages — they just record the current state.

### What's in a Checkpoint?

Each checkpoint captures:

- **Summary** — LLM-generated summary of the conversation so far
- **Token count** — How many tokens were used when the checkpoint was created
- **Topics** — Topics discussed in the conversation
- **Key decisions** — Decisions made during the conversation
- **Open questions** — Outstanding questions that haven't been resolved

### When Are Checkpoints Created?

Checkpoints are generated based on configuration:

```json
{
  "session": {
    "summarization": {
      "checkpoint": {
        "enabled": true,
        "thresholds": [25, 50, 75],
        "turnThreshold": 15,
        "minTokensForGen": 10000
      }
    }
  }
}
```

| Trigger | Description |
|---------|-------------|
| `thresholds` | Generate at 25%, 50%, 75% of context |
| `turnThreshold` | Generate every N user messages |
| `minTokensForGen` | Don't checkpoint if below this token count |

### Checkpoint Generation

Each checkpoint triggers an **async LLM call** using the summarization purpose chain:

```
[25% context] → Summarization LLM → Checkpoint saved
[50% context] → Summarization LLM → Checkpoint saved  
[75% context] → Summarization LLM → Checkpoint saved
```

### Why Use Checkpoints?

1. **Recovery points** — If something goes wrong, we have summaries
2. **Compaction optimization** — Can skip LLM call at compaction time
3. **Structured data** — Topics, decisions, questions are useful metadata
4. **Async/non-blocking** — Don't slow down the main agent loop

### Disabling Checkpoints

If you want to minimize LLM calls:

```json
{
  "session": {
    "summarization": {
      "checkpoint": {
        "enabled": false
      }
    }
  }
}
```

---

## Compaction

Compaction **truncates old messages** when context approaches the limit. This is required to continue the conversation.

When LCM is enabled (default), compaction also writes searchable summary nodes so the agent can recover compacted details later.

### When Does Compaction Trigger?

Compaction triggers based on token count OR message count:

```json
{
  "session": {
    "summarization": {
      "compaction": {
        "reserveTokens": 4000,
        "maxMessages": 500
      }
    }
  }
}
```

| Trigger | Description |
|---------|-------------|
| Token-based | `totalTokens >= maxTokens - reserveTokens` |
| Message-based | `messageCount >= maxMessages` (if > 0) |

With `reserveTokens: 30000` and `maxTokens: 200000`:
```
Compaction at: 200000 - 30000 = 170000 tokens (~85%)
```

### What Happens During Compaction?

1. **Choose a fresh tail** of newest messages to keep in raw form
2. **Generate a leaf summary** of the messages being removed
3. **Write** the compaction record plus LCM metadata to SQLite
4. **Inject** XML summary blocks plus the fresh tail into future prompts

### Lossless Context Management

With LCM enabled, each compaction creates a **leaf summary** linked to the raw messages it covered. As more leaf summaries accumulate, GoClaw can also create **condensed summaries** that group older summaries into higher-level recall cues.

This gives the agent a compact prompt view without losing drill-down paths:

- **`grep_summaries`** — Search compacted summaries by topic or phrase
- **`describe`** — Inspect one summary node cheaply
- **`expand`** — Drill into child summaries and raw source messages

By default, the prompt sees a **budget-fit frontier** of compacted history before the live message tail. GoClaw prefers higher-level condensed summaries when they cover older leaves, then spends the remaining budget on uncovered recent leaves. When the budget is tight, the newest summary blocks win (they are still rendered oldest-first inside the prompt), so long-running sessions see recent context instead of only the oldest summaries. Older decisions stay reachable through the recall tools below without being injected into every prompt.

### Compaction Configuration

```json
{
  "session": {
    "summarization": {
      "compaction": {
        "reserveTokens": 4000,
        "maxMessages": 500,
        "preferCheckpoint": true,
        "keepPercent": 50,
        "minMessages": 20,
        "freshTailCount": 10,
        "freshTailMaxTokens": 4000,
        "leafMinFanout": 4,
        "condensedMinFanout": 4,
        "incrementalMaxDepth": 2,
        "leafTargetTokens": 800,
        "condensedTargetTokens": 1200,
        "lcm": {
          "preset": "balanced",
          "enabled": true,
          "summaryInjectionMode": "frontier",
          "maxInjectedSummaryTokens": 4000,
          "summaryMaxOverageFactor": 3
        }
      }
    }
  }
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `reserveTokens` | 4000 | Tokens to reserve before triggering |
| `maxMessages` | 500 | Max messages before compaction (0 = disabled) |
| `preferCheckpoint` | true | Use existing checkpoint for summary if available |
| `keepPercent` | 50 | Percent of messages to keep after compaction |
| `minMessages` | 20 | Minimum messages to always keep |
| `freshTailCount` | `10` | When greater than `0`, keep this many newest messages instead of using `keepPercent`. Set to `0` to fall back to `keepPercent`. |
| `freshTailMaxTokens` | `4000` | Optional extra cap for the kept fresh tail; the newest message is always protected |
| `leafMinFanout` | 4 | Number of uncompacted leaf summaries required before creating a condensed summary |
| `condensedMinFanout` | 4 | Number of condensed summaries at one depth required before creating the next depth |
| `incrementalMaxDepth` | 2 | Maximum summary depth built automatically in the retry loop |
| `leafTargetTokens` | 800 | Target size for leaf summaries |
| `condensedTargetTokens` | 1200 | Target size for condensed summaries |
| `lcm.preset` | `balanced` | Named preset (`balanced`, `aggressive`, `long_term_memory`, `recall_heavy`, `custom`). Named presets are authoritative and overwrite the outer compaction and LCM fields on load. |
| `lcm.enabled` | true | Enable Lossless Context Management features, recall actions, and XML summary context |
| `lcm.summaryInjectionMode` | `frontier` | `frontier` injects a non-overlapping, budget-fit summary frontier; `all` injects every stored summary block |
| `lcm.maxInjectedSummaryTokens` | 4000 | Approximate token budget for the injected XML summary overlay when using `frontier` mode |
| `lcm.summaryMaxOverageFactor` | 3 | Hard cap multiplier for generated leaf and condensed summaries before they are truncated and stored |

### Summarization Styles

The setup editors expose a preset-first LCM UX. Presets set _both_ LCM
injection fields and the outer compaction/retention/condensation fields,
so switching preset changes the full compaction behavior, not just the
prompt overlay.

| Preset | Injection mode | Max injected summary tokens | Fresh tail count | Fresh tail max tokens | Leaf min fanout | Condensed min fanout | Incremental max depth | Leaf target tokens | Condensed target tokens |
|--------|----------------|-----------------------------|------------------|-----------------------|-----------------|----------------------|-----------------------|--------------------|-------------------------|
| Balanced (default) | frontier | 4000 | 10 | 4000 | 4 | 4 | 2 | 800 | 1200 |
| Aggressive | frontier | 2000 | 6 | 2000 | 3 | 3 | 3 | 600 | 900 |
| Long-term memory | frontier | 8000 | 20 | 8000 | 6 | 6 | 3 | 1000 | 1500 |
| Recall-heavy | all | 12000 | 20 | 8000 | 6 | 6 | 3 | 1000 | 1500 |
| Custom | — | — | — | — | — | — | — | — | — |

- **Balanced** — Recommended default. Frontier injection with a moderate prompt budget.
- **Aggressive** — Tighter frontier budget and smaller retention tail; deeper condensation.
- **Long-term Memory** — Larger tail and deeper condensation to retain more historical detail while still using frontier injection.
- **Recall-heavy** — Injects every stored summary block for maximum recall and debugging, with the largest retention settings.
- **Custom** — Unlocks direct editing of every field. GoClaw preserves user-edited values on load if they diverge from every preset.

Presets are authoritative: picking a named preset overwrites any manually
edited fields for the corresponding section. The runtime config still
stores the effective values under `session.summarization.compaction.*`
and `session.summarization.compaction.lcm.*`.

### Summary Injection Modes

GoClaw supports two prompt-assembly modes for LCM summaries:

- **`frontier`** — Default and recommended. Build a non-overlapping frontier of summary nodes, estimate the rendered XML footprint, and stop once the configured summary budget is full.
- **`all`** — Inject every stored summary row in order. This is useful for debugging or very large-context models, but it will grow prompt usage much faster.

### Summary Overage Cap

Leaf, condensed, and retry-generated summaries are all subject to the same hard cap:

```
max stored summary size = targetTokens * summaryMaxOverageFactor
```

If a generated summary grows past that limit, GoClaw truncates it deterministically, keeps a clear marker in the stored text, and preserves the `Expand for details about:` trailer when possible.

### Fresh Tail Retention

If `freshTailCount` is `0`, GoClaw behaves like the older compaction model: keep `keepPercent` of the conversation, with `minMessages` as a floor.

If `freshTailCount` is greater than `0`, it takes precedence over `keepPercent`. This is useful when you want a fixed raw tail size regardless of how large the earlier conversation became.

`freshTailMaxTokens` adds an optional token budget on top of that fixed count. If the requested tail would exceed the cap, GoClaw trims it earlier while still keeping the newest message.

### Summary Generation (with fallback)

GoClaw uses the LLM registry's summarization purpose chain with automatic fallback:

```
1. Check for recent checkpoint (fast path)
   └─ If checkpoint covers ≥50% of context → Use its summary
   
2. Try summarization providers (in order)
   └─ Primary model → Success → Done
   └─ Fallback models → Success → Done
   
3. Emergency truncation (if all fail)
   └─ Write stub summary
   └─ Keep more messages (higher keepPercent)
   └─ Mark for background retry
```

### Fallback Configuration

```json
{
  "session": {
    "summarization": {
      "failureThreshold": 3,
      "resetMinutes": 30,
      "retryIntervalSeconds": 60
    }
  }
}
```

| Setting | Default | Description |
|---------|---------|-------------|
| `failureThreshold` | 3 | After N consecutive failures, try next provider |
| `resetMinutes` | 30 | Reset failure count after N minutes |
| `retryIntervalSeconds` | 60 | Background retry interval for pending summaries |

### Background Retry

If compaction had to use emergency truncation (no summary), a background goroutine retries:

1. Checks every `retryIntervalSeconds` for pending retries
2. Loads original messages from SQLite
3. Tries summarization providers with fallback
4. Updates compaction record with better summary

The same retry loop also handles **summary condensation** when enough leaf or condensed summaries are available to roll up into a higher-level recall node.

### Using Recall After Compaction

When compacted details matter, the agent should treat summaries as recall cues rather than final proof. The intended flow is:

1. `transcript action="grep_summaries"` to find relevant summary IDs
2. `transcript action="describe"` to inspect the right summary cheaply
3. `transcript action="expand"` to recover raw messages or child summaries before answering with exact details

See [Transcript Search](transcript-search.md) for the tool-level examples.

### `/session` Diagnostics

The `/session` command now reports both normal compaction health and LCM state, including:

- Whether LCM is enabled
- Leaf summary count
- Condensed summary count and depth breakdown
- **Condense backlog**: how many un-parented leaves (and un-parented condensed nodes at each depth) are waiting to be rolled up. `(empty)` means the DAG is fully caught up; a non-zero leaf count means the background loop still has summaries to build.
- **Next tick**: what the next condensation tick will do — either `condense N → depth-D` when a fanout-sized batch is eligible, or `idle (backlog below fanout)` when there is not enough material yet.
- Pending summary retries
- FTS row count for compacted summaries

This is the quickest way to confirm that compaction is happening and that compacted summaries are searchable. After a long period with LCM disabled (or after enabling it for the first time), expect a non-zero leaf backlog that drains by `leafMinFanout` candidates per tick until the DAG is fully built.

Agents have a mirror of this surface via the `transcript` tool's `stats` action. In addition to the DAG counters shown by `/session`, the agent-facing payload includes the full preset catalog (with descriptions and every field value), a field glossary, and drift-signal heuristics — so the agent can read the live state and suggest preset tuning to you without guessing. `/session` remains the user-facing entry point; `transcript.stats` is the agent-facing one.

---

## OpenClaw Session Inheritance

GoClaw can inherit conversation history from OpenClaw sessions, enabling side-by-side operation.

### Configuration

```json
{
  "session": {
    "inherit": true,
    "inheritPath": "~/.openclaw/agents/main/sessions",
    "inheritFrom": "main"
  }
}
```

| Field | Description |
|-------|-------------|
| `inherit` | Enable OpenClaw session inheritance |
| `inheritPath` | Path to OpenClaw sessions directory |
| `inheritFrom` | Session key to inherit from |

### Session Watcher

When inheritance is enabled, a **SessionWatcher** monitors the OpenClaw session file for changes:

- Uses fsnotify for real-time change detection
- Reads new records as they're written
- Injects them into the GoClaw session
- Enables two-way conversation flow

This allows:
- Running GoClaw and OpenClaw simultaneously
- Seeing messages from both in a unified timeline
- Seamless migration between systems

---

## Context Pressure Warnings

GoClaw shows context usage to the agent and warns when approaching limits.

### Context status (ephemeral)

The agent sees the same `## Context Status` block as before, but it is injected as an ephemeral **system** message just before the latest user turn (not inside the long-lived system prompt), so the stable prompt prefix stays cache-friendly.

At higher usage levels, warnings are added:

| Usage | Warning |
|-------|---------|
| 50%+ | "You may want to note important decisions to memory files." |
| 75%+ | "Consider writing key decisions to memory/YYYY-MM-DD.md." |
| 90%+ | "CRITICAL: Write important context to memory files NOW before compaction." |

### Memory Flush Prompts

You can also inject user messages at specific thresholds to prompt the agent more directly:

```json
{
  "session": {
    "memoryFlush": {
      "enabled": true,
      "thresholds": [
        {
          "percent": 90,
          "prompt": "Context at 90%. Save important decisions to memory/YYYY-MM-DD.md before compaction.",
          "injectAs": "user",
          "oncePerCycle": true
        }
      ]
    }
  }
}
```

| Field | Description |
|-------|-------------|
| `enabled` | Enable memory flush prompting |
| `thresholds` | Array of threshold configurations |
| `percent` | Context usage percent to trigger |
| `prompt` | Message to inject |
| `injectAs` | `"user"` or `"system"` |
| `oncePerCycle` | Only trigger once per compaction cycle |

Use `YYYY-MM-DD` in the prompt — it's automatically replaced with today's date.

---

## Storage

### In-Memory vs Database

| Location | Contents | After Compaction |
|----------|----------|------------------|
| In-memory | Recent messages only | Truncated |
| SQLite | Full message history | Preserved |

This means:
- Agent only sees recent messages (context window)
- Full history is always available for auditing, retry, and expansion
- Compaction rows store searchable LCM summaries and DAG metadata
- Background retry can regenerate summaries and build condensed summaries from SQLite

### Database Location

Sessions are stored in `~/.goclaw/sessions.db`:

```sql
-- Messages table
CREATE TABLE messages (
    id TEXT PRIMARY KEY,
    session_key TEXT,
    timestamp DATETIME,
    role TEXT,           -- user, assistant, tool_use, tool_result
    content TEXT,
    tool_name TEXT,
    tool_input BLOB,
    tool_result TEXT,
    tool_is_error BOOLEAN
);

-- Compactions table
CREATE TABLE compactions (
    id TEXT PRIMARY KEY,
    session_key TEXT,
    timestamp DATETIME,
    summary TEXT,
    first_kept_entry_id TEXT,
    tokens_before INTEGER,
    needs_summary_retry BOOLEAN,
    kind TEXT,
    depth INTEGER,
    source_message_ids TEXT,
    child_compaction_ids TEXT,
    earliest_message_at DATETIME,
    latest_message_at DATETIME,
    source_token_count INTEGER
);

-- Checkpoints table
CREATE TABLE checkpoints (
    id TEXT PRIMARY KEY,
    session_key TEXT,
    timestamp DATETIME,
    summary TEXT,
    tokens_at_checkpoint INTEGER,
    topics TEXT,
    key_decisions TEXT,
    open_questions TEXT
);
```

---

## Full Configuration Example

```json
{
  "session": {
    "store": "sqlite",
    "storePath": "~/.goclaw/sessions.db",
    
    "inherit": false,
    "inheritPath": "",
    "inheritFrom": "",
    
    "summarization": {
      "ollama": {
        "url": "http://localhost:11434",
        "model": "qwen2.5:7b",
        "timeoutSeconds": 600,
        "contextTokens": 131072
      },
      "fallbackModel": "claude-3-haiku-20240307",
      "failureThreshold": 3,
      "resetMinutes": 30,
      "retryIntervalSeconds": 60,
      
      "checkpoint": {
        "enabled": true,
        "thresholds": [25, 50, 75],
        "turnThreshold": 15,
        "minTokensForGen": 10000
      },
      
      "compaction": {
        "reserveTokens": 4000,
        "maxMessages": 500,
        "preferCheckpoint": true,
        "keepPercent": 50,
        "minMessages": 20,
        "freshTailCount": 10,
        "freshTailMaxTokens": 4000,
        "leafMinFanout": 4,
        "condensedMinFanout": 4,
        "incrementalMaxDepth": 2,
        "leafTargetTokens": 800,
        "condensedTargetTokens": 1200,
        "lcm": {
          "preset": "balanced",
          "enabled": true
        }
      }
    },
    
    "memoryFlush": {
      "enabled": true,
      "thresholds": [
        {"percent": 90, "prompt": "Context at 90%. Save key context to memory/YYYY-MM-DD.md.", "injectAs": "user", "oncePerCycle": true}
      ]
    }
  }
}
```

---

## Flow Diagram

```
User Message
     │
     ▼
┌─────────────────┐
│ Add to Session  │
└────────┬────────┘
         │
         ▼
┌─────────────────┐     Yes    ┌─────────────────┐
│ Need Compaction?├───────────►│ Run Compaction  │
└────────┬────────┘            └────────┬────────┘
         │ No                           │
         ▼                              ▼
┌─────────────────┐            ┌─────────────────┐
│ Call LLM        │            │ Truncate + Save │
└────────┬────────┘            └────────┬────────┘
         │                              │
         ▼                              │
┌─────────────────┐                     │
│ Check Checkpoint│◄────────────────────┘
│ Trigger?        │
└────────┬────────┘
         │ Yes (async)
         ▼
┌─────────────────┐
│ Generate        │
│ Checkpoint      │
└─────────────────┘
```

---

## See Also

- [Configuration Reference](configuration.md) — All config options
- [Architecture](architecture.md) — System overview
- [Troubleshooting](troubleshooting.md) — Common issues
- [Agent Memory](agent-memory.md) — Memory system overview
- [Transcript Search](transcript-search.md) — Recall actions for compacted history
