# SESSION_PERSISTENCE.md — Session Storage & Context Management

## Overview

GoClaw reads and writes sessions in OpenClaw's JSONL format, enabling **shared session history** between runtimes. Additionally, GoClaw introduces **rolling checkpoints** and **multi-threshold memory flush** to prevent context loss during compaction.

**Goals:**
1. Session inheritance — GoClaw can load OpenClaw sessions seamlessly
2. Rolling checkpoints — Generate summaries *during* conversation, not just at compaction
3. Proactive memory flush — Prompt agent at 50%, 75%, 90% instead of panic at 96%

---

## Design Decisions

| Topic | Decision |
|-------|----------|
| Checkpoint model | Configurable cheaper model (e.g., Haiku); fallback to main model if unavailable |
| Checkpoint trigger | 25%, 50%, 75% tokens OR every 15 messages (whichever first) |
| Checkpoint content | Structured: summary + topics + decisions + open questions |
| Checkpoint timing | Async background (non-blocking, after turn completes) |
| OpenClaw compat | Checkpoint records are GoClaw-only (OpenClaw ignores unknown types) |
| Session sharing | GoClaw reads `agent:main:main`, writes to `goclaw:main:main` |
| Memory flush injection | System prompt for 50%/75%; distinct user message at 90% |
| 90% flush tracking | Track `flushActioned` based on write tool usage, but don't force |
| Token estimation | Anthropic API responses for actuals + tiktoken-go for pre-turn estimates |
| Record IDs | Include timestamp to avoid collision: `{timestamp}_{hex}` |

---

## Storage Layout

```
~/.openclaw/agents/main/sessions/
├── sessions.json                           # Index: session key → file
├── 7a0e870d-ac60-4042-85f2-99a6a0ca49fd.jsonl  # Session file
└── ...
```

### sessions.json (Index)

```json
{
  "agent:main:main": {
    "sessionId": "7a0e870d-ac60-4042-85f2-99a6a0ca49fd",
    "updatedAt": 1769962601713,
    "sessionFile": "/home/openclaw/.openclaw/agents/main/sessions/7a0e870d-....jsonl",
    "compactionCount": 2,
    "totalTokens": 156000
  }
}
```

---

## JSONL Record Types

| Type | Description | OpenClaw Compat |
|------|-------------|-----------------|
| `session` | Session header (first line) | Yes |
| `message` | User/assistant/tool messages | Yes |
| `compaction` | History truncation marker | Yes |
| `checkpoint` | Rolling summary (GoClaw feature) | **No** (ignored) |
| `model_change` | Model switch event | Yes |
| `thinking_level_change` | Thinking mode change | Yes |
| `custom` | Extension events | Yes |

### Record ID Format

To avoid 8-char hex collision, include timestamp:

```go
func generateRecordID() string {
    return fmt.Sprintf("%d_%s", time.Now().UnixMilli(), randomHex(4))
}
// Example: "1706803200000_a1b2"
```

---

## Rolling Checkpoints (GoClaw Feature)

### Concept

Generate structured summaries *during* the conversation while full context is available, not at compaction time when context may already be degraded.

### Checkpoint Record

```json
{
  "type": "checkpoint",
  "id": "1706803200000_c1a2",
  "parentId": "previousRecordId",
  "timestamp": "2026-02-01T12:00:00Z",
  "checkpoint": {
    "summary": "User is working on GoClaw, a Go rewrite of OpenClaw...",
    "tokensAtCheckpoint": 50000,
    "messageCountAtCheckpoint": 45,
    "topics": ["GoClaw development", "session persistence", "compaction"],
    "openQuestions": ["How to handle concurrent writes?"],
    "keyDecisions": ["Use JSONL format for OpenClaw compat"]
  }
}
```

### Triggers

| Trigger | Default | Notes |
|---------|---------|-------|
| Token threshold | 25%, 50%, 75% | Percentage of context window |
| Turn count | Every 15 user messages | Whichever triggers first |
| Topic change | Deferred to post-MVP | Optional embedding-based detection |

### Generation

- **Model**: Configurable (default: `anthropic/claude-3-haiku`)
- **Fallback**: Use main conversation model if checkpoint model unavailable
- **Timing**: Async background — does not block next turn
- **Content**: Structured extraction via LLM prompt

```go
type CheckpointConfig struct {
    Enabled         bool
    Model           string  // e.g., "anthropic/claude-3-haiku"
    FallbackToMain  bool    // Default: true
    TokenThresholds []int   // Percentages: [25, 50, 75]
    TurnThreshold   int     // Default: 15 user messages
    MinTokensForGen int     // Skip if context < N tokens
}
```

### Usage

1. **After Compaction**: Use recent checkpoint instead of generating new summary
2. **Session Inheritance**: Checkpoints provide rich context when loading
3. **Fast Compaction**: If recent checkpoint exists, no LLM call needed

---

## Multi-Threshold Memory Flush

### Thresholds & Injection Method

| Percent | Type | Injection | Prompt |
|---------|------|-----------|--------|
| 50% | Soft | System prompt | "Context at 50%. Consider noting key decisions to memory." |
| 75% | Medium | System prompt | "Context at 75%. Write important context to memory/YYYY-MM-DD.md now." |
| 90% | Urgent | **User message** | See below |

### 90% Urgent Message

At 90%, inject as a user message (agent must respond):

```
[SYSTEM: pre-compaction memory flush]
Context at 90%. Compaction imminent.
Store durable memories now (use memory/YYYY-MM-DD.md; create memory/ if needed).
If nothing to store, reply with NO_REPLY.
```

### Flush Action Tracking

Track whether agent wrote to memory after 90% flush:

```go
// After agent response to 90% flush
flushActioned := false
for _, toolCall := range response.ToolCalls {
    if toolCall.Name == "write" || toolCall.Name == "edit" {
        if strings.HasPrefix(toolCall.Input.Path, "memory/") {
            flushActioned = true
            break
        }
    }
}
sess.Metadata.FlushActioned = flushActioned
```

**Note**: We track for observability but don't force action. If agent ignores, compaction happens anyway.

---

## Token Tracking

Two-pronged approach for accuracy:

### Pre-Turn Estimates (for threshold detection)

Use tiktoken-go library (`github.com/pkoukk/tiktoken-go`):

```go
import "github.com/pkoukk/tiktoken-go"

func estimateTokens(text string) int {
    enc, _ := tiktoken.GetEncoding("cl100k_base")  // Claude's encoding
    return len(enc.Encode(text, nil, nil))
}

// Before sending to LLM
estimatedTokens := sess.TotalTokens + estimateTokens(newUserMessage)
if estimatedTokens >= sess.MaxTokens * 0.9 {
    // Inject 90% warning before this turn
}
```

### Post-Turn Actuals (from API response)

```go
// After LLM response
sess.TotalTokens = response.Usage.InputTokens + response.Usage.OutputTokens
```

### Context Visibility

Inject into system prompt:

```
[Context: 156k/200k tokens (78%)]
```

---

## Compaction

### Trigger

```go
func (s *Session) ShouldCompact() bool {
    return s.TotalTokens >= s.MaxTokens - s.ReserveTokens
}
```

### With Checkpoints (Fast Path)

```go
func (g *Gateway) compact(sess *Session) error {
    // 1. Find most recent checkpoint
    checkpoint := sess.MostRecentCheckpoint()
    
    // 2. If checkpoint recent enough, use it (no LLM call!)
    if checkpoint != nil && checkpoint.TokensAtCheckpoint > sess.TotalTokens*0.5 {
        return g.compactWithCheckpoint(sess, checkpoint)
    }
    
    // 3. Fallback: generate summary from current context
    summary, err := g.llm.GenerateSummary(sess.Messages)
    if err != nil {
        // Last resort: use old checkpoint
        if checkpoint != nil {
            return g.compactWithCheckpoint(sess, checkpoint)
        }
        return err
    }
    
    return g.writeCompactionRecord(sess, summary)
}
```

### Compaction Record

```json
{
  "type": "compaction",
  "id": "1706803200000_0b72",
  "parentId": "06856efc",
  "timestamp": "2026-02-01T16:10:50.414Z",
  "summary": "Working on GoClaw session persistence...",
  "firstKeptEntryId": "2ca436a6",
  "tokensBefore": 186673,
  "fromCheckpoint": true
}
```

---

## Session File Strategy

| Runtime | Reads From | Writes To |
|---------|------------|-----------|
| OpenClaw | `agent:main:main` | `agent:main:main` |
| GoClaw | `agent:main:main` (inherit) | `goclaw:main:main` |

No concurrent write conflicts — each runtime has its own session file.

---

## Configuration

```json
{
  "session": {
    "storage": "jsonl",
    "path": "~/.openclaw/agents/main/sessions",
    "inherit": true,
    "defaultKey": "agent:main:main",
    
    "checkpoint": {
      "enabled": true,
      "model": "anthropic/claude-3-haiku",
      "fallbackToMain": true,
      "tokenThresholdPercents": [25, 50, 75],
      "turnThreshold": 15,
      "minTokensForGen": 10000
    },
    
    "memoryFlush": {
      "enabled": true,
      "showInSystemPrompt": true,
      "thresholds": [
        {
          "percent": 50,
          "prompt": "Context at 50%. Consider noting key decisions to memory.",
          "injectAs": "system"
        },
        {
          "percent": 75,
          "prompt": "Context at 75%. Write important context to memory/YYYY-MM-DD.md now.",
          "injectAs": "system"
        },
        {
          "percent": 90,
          "prompt": "[SYSTEM: pre-compaction memory flush]\nContext at 90%. Compaction imminent.\nStore durable memories now (use memory/YYYY-MM-DD.md).\nIf nothing to store, reply with NO_REPLY.",
          "injectAs": "user"
        }
      ]
    },
    
    "compaction": {
      "reserveTokens": 4000,
      "preferCheckpoint": true
    }
  }
}
```

---

## File Structure

```
internal/session/
├── manager.go      # Session management (exists)
├── session.go      # Session struct (exists)
├── types.go        # Record type definitions (new)
├── jsonl.go        # JSONL reader/writer (new)
├── context.go      # Build LLM context from records (new)
├── checkpoint.go   # Rolling checkpoint generation (new)
├── compaction.go   # Compaction logic (new)
└── tokens.go       # Token estimation (new)
```

---

## Implementation Phases

### Phase 1: JSONL Persistence
- Record type definitions
- JSONL reader/writer
- sessions.json index management
- Build LLM context from records
- Session inheritance from OpenClaw

### Phase 2: Token Tracking
- tiktoken-go integration for pre-turn estimates
- Track actuals from API responses
- Context % in system prompt

### Phase 3: Memory Flush
- Threshold configuration
- System prompt injection (50%/75%)
- User message injection (90%)
- Flush action tracking

### Phase 4: Rolling Checkpoints
- Checkpoint triggers (token % + turn count)
- Async generation with cheaper model
- Structured summary extraction
- Checkpoint record persistence

### Phase 5: Compaction
- Compaction trigger logic
- Checkpoint-based fast path
- LLM fallback
- Threshold reset

---

## Summary vs Memory Distinction

| Aspect | Rolling Summary (Checkpoint) | Memory Files |
|--------|------------------------------|--------------|
| Purpose | Conversation state | Durable knowledge |
| Lifetime | Session-bound | Permanent |
| Content | Topics, decisions, questions | Facts, lessons, context |
| Trigger | Automatic (thresholds) | Agent-initiated (write tool) |
| Location | JSONL session file | `memory/YYYY-MM-DD.md` |

Both matter:
- **Checkpoint**: "We're debugging the token counter bug"
- **Memory**: "GoClaw uses JSONL format (see specs/SESSION_PERSISTENCE.md)"

---

## Related: Memory Search Tool

The memory flush workflow tells agents to write context to `memory/` files. But after compaction, agents need a way to **find** those memories. This requires a separate `memory_search` tool (not part of this spec):

**Why it matters:**
- Agent writes important context at 75%/90% thresholds
- Compaction happens, agent forgets conversation details
- Agent needs to search memory files to recover context
- Without search, memories become write-only (useless)

**Requirements:**
- Semantic/vector search over `memory/` directory
- Keyword fallback for simple queries
- Integration with compaction recovery workflow

**OpenClaw reference:** `agents.defaults.memorySearch` config — check for compatibility.

**Spec needed:** `specs/MEMORY_SEARCH.md`

---

## Storage Backend Architecture

**Target:** SQLite as primary storage. JSONL is read-only for OpenClaw migration.

### Why SQLite over JSONL

| Factor | JSONL | SQLite |
|--------|-------|--------|
| Locking | Manual flock() | Implicit (WAL mode) |
| Concurrent access | Fragile | Handled correctly |
| Query speed | Parse entire file | Indexed lookups |
| Parsing overhead | Every read | None |
| Portability | Text files | Single file, embedded |

### Pluggable Interface

```go
type SessionStore interface {
    Load(key string) (*Session, error)
    Save(session *Session) error
    Append(key string, record Record) error
    List() ([]SessionInfo, error)
    Close() error
}

// Implementations:
// - jsonlStore   (read-only, OpenClaw migration)
// - sqliteStore  (primary, default)
```

### SQLite Schema

```sql
CREATE TABLE sessions (
    key TEXT PRIMARY KEY,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    metadata JSON
);

CREATE TABLE messages (
    id TEXT PRIMARY KEY,
    session_key TEXT NOT NULL,
    parent_id TEXT,
    type TEXT NOT NULL,
    timestamp INTEGER NOT NULL,
    data JSON NOT NULL,
    FOREIGN KEY (session_key) REFERENCES sessions(key)
);

CREATE INDEX idx_messages_session ON messages(session_key, timestamp);
CREATE INDEX idx_messages_parent ON messages(parent_id);
```

### Migration Flow

1. On first run, check for OpenClaw sessions at `~/.openclaw/agents/main/sessions/`
2. If found and `inherit: true`, read JSONL and import to SQLite
3. All new writes go to SQLite
4. JSONL files left untouched (OpenClaw can still use them)

### Config

```json
{
  "session": {
    "store": "sqlite",
    "path": "~/.openclaw/goclaw/sessions.db",
    "inherit": {
      "enabled": true,
      "source": "~/.openclaw/agents/main/sessions",
      "format": "jsonl"
    }
  }
}
```

---

## Memory Flush Delivery

How flush prompts are delivered to the agent:

### Delivery Methods

| Method | Description |
|--------|-------------|
| `system` | Inject into system prompt (visible every turn until context drops) |
| `user` | Inject as user message (feels like conversation interruption) |
| `marked` | Inject as user message with clear marker: `[SYSTEM: memory flush]\n{prompt}` |

**Default:** `marked` — clear it's operational, not from the human.

### Actioned Detection

After flush prompt, check if agent invoked `write` or `edit` tool during that turn:
- If yes → `actioned = true`
- If no → `actioned = false`

Logged in session metadata for analytics. No escalation in MVP — `oncePerCycle` prevents nagging.

---

## Status

| Feature | Status |
|---------|--------|
| Read OpenClaw JSONL | To implement |
| Parse message records | To implement |
| Handle compaction markers | To implement |
| Build LLM context | To implement |
| SQLite storage | To implement |
| Pluggable SessionStore interface | To implement |
| JSONL → SQLite migration | To implement |
| Session inheritance | To implement |
| Token tracking (tiktoken) | To implement |
| Context % visibility | To implement |
| Memory flush (50/75/90%) | To implement |
| Memory flush delivery (marked) | To implement |
| Flush actioned detection | To implement |
| Rolling checkpoints | Post-MVP |
| Compaction with checkpoints | Post-MVP |
