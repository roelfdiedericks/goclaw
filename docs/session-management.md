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
2. **Checkpoints** (optional) - Takes rolling snapshots at thresholds
3. **Compacts** (required) - Truncates old messages when nearly full

## Checkpoints

Checkpoints are **rolling snapshots** of conversation state. They do NOT delete messages - they just record the current state.

### What's in a Checkpoint?

```go
type CheckpointData struct {
    Summary           string   // LLM-generated summary
    TokensAtCheckpoint int     // Token count when created
    Topics            []string // Topics discussed
    KeyDecisions      []string // Decisions made
    OpenQuestions     []string // Outstanding questions
}
```

### When Are Checkpoints Created?

Checkpoints are generated based on configuration:

```json
{
  "session": {
    "checkpoint": {
      "enabled": true,
      "tokenThresholdPercents": [25, 50, 75],
      "turnThreshold": 10,
      "minTokensForGen": 5000
    }
  }
}
```

| Trigger | Description |
|---------|-------------|
| `tokenThresholdPercents` | Generate at 25%, 50%, 75% of context |
| `turnThreshold` | Generate every N user messages |
| `minTokensForGen` | Don't checkpoint if below this token count |

### Checkpoint Generation

Each checkpoint triggers an **async LLM call**:

```
[25% context] → Ollama call → Checkpoint saved
[50% context] → Ollama call → Checkpoint saved  
[75% context] → Ollama call → Checkpoint saved
```

Checkpoints use Ollama by default (cheap, local). If Ollama fails, falls back to main model.

### Why Use Checkpoints?

1. **Recovery points** - If something goes wrong, we have summaries
2. **Compaction optimization** - Can skip LLM call at compaction time
3. **Structured data** - Topics, decisions, questions are useful metadata
4. **Async/non-blocking** - Don't slow down the main agent loop

### Disabling Checkpoints

If you want to minimize LLM calls:

```json
{
  "session": {
    "checkpoint": {
      "enabled": false
    }
  }
}
```

---

## Compaction

Compaction **truncates old messages** when context approaches the limit. This is required to continue the conversation.

### When Does Compaction Trigger?

```json
{
  "session": {
    "compaction": {
      "reserveTokens": 30000
    }
  }
}
```

Compaction triggers when:
```
totalTokens >= maxTokens - reserveTokens
```

With `reserveTokens: 30000` and `maxTokens: 200000`:
```
Compaction at: 200000 - 30000 = 170000 tokens (~85%)
```

### What Happens During Compaction?

1. **Generate summary** of messages being removed
2. **Truncate** old messages (keep last 10-20%)
3. **Write** compaction record to database
4. **Inject** summary into future prompts

### Summary Generation (with fallback)

GoClaw tries multiple strategies to generate the compaction summary:

```
1. Check for recent checkpoint (fast path)
   └─ If checkpoint covers ≥50% of context → Use its summary
   
2. Try Ollama
   └─ Success → Done
   └─ Failure → Increment failure count
   
3. Try Anthropic (fallback)
   └─ Success → Done
   └─ Failure → Continue to emergency
   
4. Emergency truncation
   └─ Write stub summary
   └─ Keep 20% of messages (instead of 10%)
   └─ Mark for background retry
```

### The `preferCheckpoint` Option

```json
{
  "session": {
    "compaction": {
      "preferCheckpoint": true
    }
  }
}
```

| Value | Behavior |
|-------|----------|
| `true` | Check for recent checkpoint first, skip LLM if found |
| `false` | Always call LLM for fresh summary |

**Tradeoff:**
- `true` = Faster, cheaper (reuses existing summary)
- `false` = More accurate (summarizes exactly what's being removed)

### Fallback Configuration

```json
{
  "session": {
    "compaction": {
      "ollamaFailureThreshold": 3,
      "ollamaResetMinutes": 30
    }
  }
}
```

| Setting | Description |
|---------|-------------|
| `ollamaFailureThreshold` | After N consecutive Ollama failures, use Anthropic |
| `ollamaResetMinutes` | Try Ollama again after this many minutes |

### Background Retry

If compaction had to use emergency truncation (no summary), a background goroutine retries:

```json
{
  "session": {
    "compaction": {
      "retryIntervalSeconds": 60
    }
  }
}
```

The retry process:
1. Checks every 60 seconds for pending retries
2. Loads original messages from SQLite
3. Tries Ollama → Anthropic fallback
4. Updates compaction record with better summary

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

### Database Schema

Sessions are stored in `~/.openclaw/sessions.db`:

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
```

---

## Configuration Summary

```json
{
  "session": {
    "store": "sqlite",
    "storePath": "~/.openclaw/sessions.db",
    
    "checkpoint": {
      "enabled": true,
      "tokenThresholdPercents": [25, 50, 75],
      "turnThreshold": 10,
      "minTokensForGen": 5000
    },
    
    "compaction": {
      "reserveTokens": 30000,
      "preferCheckpoint": true,
      "retryIntervalSeconds": 60,
      "ollamaFailureThreshold": 3,
      "ollamaResetMinutes": 30,
      "ollama": {
        "url": "http://localhost:11434",
        "model": "qwen2.5:7b",
        "timeoutSeconds": 600,
        "contextTokens": 131072
      }
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

- [Configuration Reference](./configuration.md) - All config options
- [Architecture](./architecture.md) - System overview
- [Troubleshooting](./troubleshooting.md) - Common issues
