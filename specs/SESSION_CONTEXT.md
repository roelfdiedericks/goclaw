# SESSION_CONTEXT.md — How OpenClaw Manages Context

## Overview

OpenClaw sends **the entire session history** to Anthropic on each request, from the last compaction point. There's no sliding window or "last N messages" — it's all-or-nothing until context overflows.

---

## What Gets Sent Each Turn

```
┌─────────────────────────────────────────────────────────┐
│ System Prompt                                           │
│ - Core identity, safety, tooling sections               │
│ - Workspace files (AGENTS.md, SOUL.md, etc.)            │
│ - Tool definitions and summaries                        │
│ - Runtime info, time, user identity                     │
│ - ~10-30k tokens depending on workspace                 │
├─────────────────────────────────────────────────────────┤
│ [Compaction Summary - if previous compaction occurred]  │
│ - LLM-generated summary of truncated history            │
│ - Injected as first user message                        │
├─────────────────────────────────────────────────────────┤
│ Full Message History (since last compaction)            │
│ - User messages (text, images)                          │
│ - Assistant messages (text, tool calls)                 │
│ - Tool results                                          │
│ - ALL of them, in order                                 │
├─────────────────────────────────────────────────────────┤
│ Current User Message                                    │
└─────────────────────────────────────────────────────────┘
```

---

## Message Types in History

### User Message
```json
{
  "role": "user",
  "content": [
    { "type": "text", "text": "What's the weather?" }
  ]
}
```

### Assistant Message
```json
{
  "role": "assistant", 
  "content": [
    { "type": "text", "text": "Let me check..." },
    { "type": "toolCall", "id": "toolu_xxx", "name": "exec", "arguments": {...} }
  ]
}
```
**Note:** Thinking blocks are stored but typically excluded from context sent to LLM.

### Tool Result
```json
{
  "role": "toolResult",
  "toolCallId": "toolu_xxx",
  "content": [{ "type": "text", "text": "Weather: 24°C sunny" }]
}
```

---

## Context Flow

```
User sends message
       │
       ▼
┌──────────────────┐
│ Load session     │ ← Read JSONL, get all messages since last compaction
│ from JSONL       │
└────────┬─────────┘
         │
         ▼
┌──────────────────┐
│ Sanitize history │ ← Fix role ordering, validate turns
└────────┬─────────┘
         │
         ▼
┌──────────────────┐
│ Limit turns      │ ← Optional: getDmHistoryLimitFromSessionKey()
│ (if configured)  │   Config: channels.<provider>.historyTurns
└────────┬─────────┘
         │
         ▼
┌──────────────────┐
│ Build system     │ ← Rebuilt fresh each turn with current workspace files
│ prompt           │
└────────┬─────────┘
         │
         ▼
┌──────────────────┐
│ Send to LLM      │ ← system + messages + current user message
│                  │
└────────┬─────────┘
         │
         ▼
    Context overflow?
         │
    ┌────┴────┐
    No       Yes
    │         │
    ▼         ▼
 Continue   Trigger compaction
```

---

## Compaction Process

When context exceeds model limits (~96% full):

### 1. Detect Overflow
- LLM returns context overflow error
- OpenClaw catches `isContextOverflowError()`

### 2. Attempt Auto-Compaction
```typescript
const compactResult = await compactEmbeddedPiSessionDirect({
  sessionFile,
  workspaceDir,
  config,
  // ...
});
```

### 3. Summarization Strategy

OpenClaw uses **chunked summarization** from `compaction.ts`:

```typescript
// Split messages into chunks by token share
const chunks = splitMessagesByTokenShare(messages, parts);

// Summarize each chunk
for (const chunk of chunks) {
  summary = await generateSummary(chunk, model, ...);
}

// If multiple chunks, merge summaries
if (partialSummaries.length > 1) {
  // LLM merges partial summaries into one
}
```

**Key functions:**
- `estimateMessagesTokens()` — Count tokens in message list
- `splitMessagesByTokenShare()` — Divide messages into roughly equal token chunks
- `summarizeInStages()` — Multi-stage summarization with fallbacks
- `summarizeWithFallback()` — Handles oversized messages gracefully

### 4. Prune History
```typescript
const { messages, droppedMessages, droppedTokens } = pruneHistoryForContextShare({
  messages,
  maxContextTokens,
  maxHistoryShare: 0.5,  // Keep at most 50% of context for history
});
```

### 5. Write Compaction Record
```json
{
  "type": "compaction",
  "id": "0b72cf11",
  "timestamp": "2026-02-01T16:10:50.414Z",
  "summary": "Summary of truncated history...",
  "firstKeptEntryId": "2ca436a6",
  "tokensBefore": 186673
}
```

### 6. Retry Request
With compacted history, retry the user's message.

---

## Token Estimation

OpenClaw uses `estimateTokens()` from pi-coding-agent:

```typescript
export function estimateMessagesTokens(messages: AgentMessage[]): number {
  return messages.reduce((sum, message) => sum + estimateTokens(message), 0);
}
```

This is an **estimate**, not exact. OpenClaw adds safety margins:
- `SAFETY_MARGIN = 1.2` (20% buffer for estimation inaccuracy)
- Compaction triggers well before actual limit

---

## History Limiting (Optional)

Some channels can limit history turns via config:

```json
{
  "channels": {
    "telegram": {
      "historyTurns": 50
    }
  }
}
```

```typescript
const limited = limitHistoryTurns(
  validated,
  getDmHistoryLimitFromSessionKey(sessionKey, config)
);
```

This is **turn limiting**, not token limiting — useful for DMs where you don't need months of history.

---

## What GoClaw Needs to Implement

### MVP (Current Gap)

1. **Send full history** — Currently GoClaw may not be sending all messages
2. **Token tracking** — Count tokens before sending, know when approaching limit
3. **System prompt rebuild** — Fresh system prompt each turn with current workspace

### Phase 2

4. **Compaction detection** — Catch context overflow errors
5. **Auto-compaction** — Summarize and prune when overflow detected
6. **Compaction records** — Write compaction markers to session

### Phase 3

7. **Memory flush triggers** — Prompt agent at 50%/75%/90% thresholds
8. **Rolling summaries** — Incremental summarization (from SESSION_PERSISTENCE.md spec)

---

## Key Differences: OpenClaw vs GoClaw Current State

| Aspect | OpenClaw | GoClaw (Current) |
|--------|----------|------------------|
| History sent | Full (since compaction) | Unknown/partial? |
| System prompt | Rebuilt each turn | Rebuilt each turn ✓ |
| Token counting | estimateTokens() | Not implemented |
| Compaction | Auto on overflow | Not implemented |
| History limiting | Optional per-channel | Not implemented |

---

## Files to Study

OpenClaw source (in `goclaw/ref/openclaw/src/`):

- `agents/compaction.ts` — Core compaction logic, token estimation
- `agents/pi-embedded-runner/run/attempt.ts` — Main agent loop, message handling
- `agents/pi-embedded-runner/compact.ts` — Session compaction orchestration
- `agents/pi-embedded-runner/history.ts` — limitHistoryTurns, getDmHistoryLimit
- `agents/pi-extensions/compaction-safeguard.ts` — Safeguard mode extension

---

## GoClaw Implementation Details

### Compaction Model Selection

GoClaw supports using a separate Ollama model for compaction/checkpoint summarization to reduce main model token usage.

**Configuration:**
```json
{
  "session": {
    "compaction": {
      "ollama": {
        "url": "http://localhost:11434",
        "model": "qwen2.5:7b"
      }
    }
  }
}
```

**Behavior:**
- If `session.compaction.ollama.url` is set, GoClaw uses Ollama for:
  - Compaction summary generation
  - Checkpoint summary generation
- If not configured, falls back to main Anthropic model (default)
- Recommended models: `qwen2.5:7b`, `llama3.2`, `mistral`

**Benefits:**
- Reduces main model token costs (compaction summaries can be 4k+ tokens)
- Local model = no API costs for summarization
- Faster for large context summarization

### System Prompt Caching

GoClaw caches workspace files (SOUL.md, IDENTITY.md, etc.) to avoid disk I/O on every request.

**Architecture:**
```
┌──────────────────────────────────────────────────────────┐
│                    Prompt Cache                          │
├──────────────────────────────────────────────────────────┤
│  fsnotify Watcher     Hash Poller (fallback)             │
│  - Watches files      - Checks every 60s (configurable)  │
│  - Immediate          - Catches edge cases               │
│    invalidation       - Network filesystems, Docker      │
├──────────────────────────────────────────────────────────┤
│               Cached Workspace Files                     │
│  AGENTS.md, SOUL.md, TOOLS.md, IDENTITY.md, ...         │
└──────────────────────────────────────────────────────────┘
```

**Watched Files:**
- `AGENTS.md`, `SOUL.md`, `TOOLS.md`, `IDENTITY.md`, `USER.md`
- `HEARTBEAT.md`, `BOOTSTRAP.md`, `MEMORY.md`
- `memory/` directory (for daily memory files)

**Configuration:**
```json
{
  "promptCache": {
    "pollInterval": 60
  }
}
```

- `pollInterval`: Hash poll interval in seconds (default: 60, 0 = disable polling)

**Cache Invalidation:**
1. **Primary (fsnotify):** File change triggers immediate invalidation
2. **Fallback (hash polling):** Background goroutine checks file hashes periodically
   - Catches cases fsnotify might miss (network filesystems, some editors, Docker volumes)

**Cache Coherency:**
- When any identity file changes, the cache invalidates
- Next request loads fresh files from disk
- Anthropic's `cache_control: ephemeral` caches the system prompt on their side
- Local cache ensures file changes are detected and Anthropic receives updated prompt

---

## Summary

OpenClaw's context strategy:
1. **Send everything** since last compaction
2. **Rebuild system prompt** fresh each turn
3. **Estimate tokens** to track context usage
4. **Auto-compact** when overflow detected
5. **Summarize in chunks** for better quality
6. **Prune to 50%** of context window post-compaction

GoClaw implements the same pattern with additional features:
- **Optional Ollama model** for compaction/checkpoints (reduces main model costs)
- **Workspace file caching** with fsnotify + hash polling (improves performance)
- **Cache coherency** for identity files (ensures prompt updates when files change)
