---
title: "Session Management"
description: "Context management and compaction to stay within LLM token limits"
section: "Advanced"
weight: 10
---

# Session Management

GoClaw manages conversation context to stay within LLM token limits while preserving important information.

## Overview

```
┌─────────────────────────────────────────────────────────────┐
│                    Context Window (200k)                     │
├─────────────────────────────────────────────────────────────┤
│ System Prompt │ Compaction Summary │ Recent Messages        │
│    (~10k)     │     (~2k)          │    (remaining)         │
└─────────────────────────────────────────────────────────────┘
```

As conversation grows, GoClaw:
1. **Monitors** context usage (token count vs max)
2. **Checkpoints** (optional) — Takes rolling snapshots at thresholds
3. **Compacts** (required) — Truncates old messages when nearly full

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

1. **Generate summary** of messages being removed
2. **Truncate** old messages (keep configurable percentage)
3. **Write** compaction record to database
4. **Inject** summary into future prompts

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
        "minMessages": 20
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
- Full history is always available for auditing/retry
- Background retry can regenerate summaries from SQLite

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
    needs_summary_retry BOOLEAN
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
        "minMessages": 20
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
