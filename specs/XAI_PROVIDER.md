# xAI Provider Specification

**Status:** In Progress  
**Date:** 2026-02-14  
**Author:** Ratpup + RoDent

## Overview

First-class xAI/Grok provider for GoClaw using **gRPC** for maximum performance. Native integration with xAI's server-side tools.

### Implementation Status

| Component | Status | Notes |
|-----------|--------|-------|
| xai-go gRPC client | ✅ Done | https://github.com/roelfdiedericks/xai-go |
| GoClaw provider wrapper | 🔲 Planned | `internal/llm/xai.go` |
| Server-side tool integration | 🔲 Planned | web_search, x_search variants |
| Image generation tool | 🔲 Planned | Regular tool, not slash command |
| Embeddings provider | 🔲 Planned | Alternative to current embeddings |

## Why First-Class?

1. **Performance:** Native xAI API is 50% faster than OpenAI-compatible (181 t/s vs 118 t/s)
2. **Server-side tools:** web_search, x_search, code_execution run on xAI infra
3. **Future-proofing:** xAI is deprecating Anthropic SDK compatibility, pushing gRPC
4. **Features:** Citations, deferred completions, batch API
5. **X/Twitter access:** Native x_search without scraping

## xai-go Client

**Repository:** https://github.com/roelfdiedericks/xai-go

Uses gRPC (native xAI protocol). Full API coverage, tested interactively.

### Streaming Interface

xai-go already provides a clean streaming interface:

```go
stream, err := client.StreamChat(ctx, req)
for {
    chunk, err := stream.Next()
    if err == io.EOF {
        break
    }
    fmt.Print(chunk.Delta)
}
```

**GoClaw integration:** The XAI provider wrapper adapts xai-go's `stream.Next()` to GoClaw's `onDelta` callback pattern. No interface changes needed — same adapter pattern as OpenAI/Anthropic providers.

---

## Decision: Server-Side Tools

Server-side tools (web_search, x_search, code_execution) are a must for X/Twitter access.

### Good News: They CAN Be Mixed!

From xAI Advanced Usage docs:

> "You can combine server-side agentic tools (like web search and code execution) with custom client-side tools to create powerful hybrid workflows."

**This is cleaner than expected.** We can send BOTH server-side tools AND our function tools in the same request.

### How Mixed Tools Work

```
1. Send request with:
   - server_tools: [web_search, x_search]  
   - tools: [hass, exec, browser, read, write, ...]

2. xAI streams response:
   - Model decides to search → xAI executes server-side, continues internally
   - Model decides to call hass → execution PAUSES, returns tool_call to us
   - We execute hass, send result back
   - Model continues

3. Response includes:
   - content: final synthesized text
   - citations: sources from any searches
   - server_side_tool_usage: {WEB_SEARCH: 2, X_SEARCH: 1}
   - tool_calls: ALL calls (server + client) for logging
```

### What We See vs Don't See

| Data | Visible? | Notes |
|------|----------|-------|
| Server-side tool invocations | ✅ Yes | In `chunk.tool_calls` during stream |
| Server-side tool **results** | ❌ No | Model uses internally, we get synthesized response |
| Client-side tool calls | ✅ Yes | Execution pauses, we handle them |
| Citations/sources | ✅ Yes | In `response.citations` |
| Billing counts | ✅ Yes | In `response.server_side_tool_usage` |

### Distinguishing Tool Types

**No namespace clash!** Server-side and client-side tools have different `type` fields in xAI responses:

| Type | Category | Notes |
|------|----------|-------|
| `function_call` | Client-side | Our tools (hass, exec, read, etc.) |
| `web_search_call` | Server-side | xAI executes |
| `x_search_call` | Server-side | xAI executes |
| `code_interpreter_call` | Server-side | xAI executes |
| `file_search_call` | Server-side | xAI executes |

Even if we have a local function named `web_search`, it would be `function_call` type, not `web_search_call`. Distinguish by **type**, not name.

```go
func isServerSideTool(toolType string) bool {
    switch toolType {
    case "web_search_call", "x_search_call", "code_interpreter_call", "file_search_call":
        return true
    }
    return false  // function_call = client-side
}
```

When streaming:
- Server-side tool call → display/log it, continue streaming (xAI handles execution)
- Client-side tool call → pause, execute our tool, send result, continue

### Display Format

Server-side tool invocations should be displayed like local ones for thinking/trace output:

```
[web_search: {"query":"OpenClaw AI news","num_results":10}]
[x_keyword_search: {"query":"OpenClaw AI","limit":10,"mode":"Latest"}]
[x_semantic_search: {"query":"news on AI and OpenClaw","limit":10}]
```

For internal logs that need distinction:
```
L_debug("xai: server-side tool", "tool", name, "args", args)
L_debug("xai: client-side tool", "tool", name, "args", args)  
```

### Thinking Mode: Status Callbacks

xAI streams both `pending` and `completed` status for each server-side tool:

```
[Tool Calls]
  - web_search (server, pending)
    Args: {"query":"OpenClaw AI news"}
  - web_search (server, completed)
    Args: {"query":"OpenClaw AI news"}
```

**Current approach:** Show both. Verbose but informative for thinking mode.

**Future improvements (not MVP):**
- Dedupe by tool call ID — show args once, update status in-place
- Streaming status transitions: `[web_search: "query"] → searching... → done (0.8s)`
- Collapse completed tools in TUI, expand on click
- Show timing: how long each server-side tool took

For now, verbosity is acceptable. The data is available when we want to polish the UX.

### The Hybrid Flow

```
User: "What's trending on X about AI and turn on my office lights"

→ Request to xAI with:
  - server_tools: [x_search]
  - tools: [hass]

← Stream chunk: tool_call{name: "x_search", args: {query: "AI trending"}}
   (xAI executes internally, we just log it)

← Stream chunk: tool_call{name: "hass", args: {action: "call", ...}}
   (Execution pauses! This is ours)

→ We execute: hass(action="call", service="light.turn_on", ...)
→ Send tool_result back to xAI

← Stream continues with final response + citations from X search
```

### Benefits

- ✅ **Native X/Twitter search** — no scraping, no auth headaches
- ✅ **Our tools still work** — hass, exec, browser, etc.
- ✅ **Visibility** — we see all tool calls for logging/debugging
- ✅ **Citations** — sources included in response
- ✅ **Single request** — no two-phase complexity

---

## Multi-Turn Context Preservation

**Concern:** Does the model lose context of what server-side tools did across turns?

**Answer:** No — xAI stores full internal state (reasoning, tool calls, tool results) for 30 days.

### How It Works

```go
// Turn 1
resp1 := client.CompleteChat(ctx, req1.StoreMessages(true))
// xAI stores: reasoning + tool calls + tool results + response
// Returns: resp1.ID = "resp_abc123"

// Turn 2 — reference previous response
resp2 := client.CompleteChat(ctx, req2.PreviousResponseID("resp_abc123"))
// xAI hydrates full context — Grok knows what it searched/found
```

### Session Lifetime

| Aspect | Detail |
|--------|--------|
| **Lifetime** | 30 days from creation |
| **After 30 days** | Auto-deleted, fall back to local transcript |
| **System prompt** | NOT inherited — must send each turn (same as current GoClaw behavior) |
| **Billing** | Billed for full context even though stored server-side |

### GoClaw Hybrid Architecture

**Decision:** Use `previous_response_id` — encapsulated entirely within `xai.go`.

Other providers (Anthropic, OpenAI, Ollama) don't need to know about this. The XAI provider manages its own context chain internally.

```
┌─────────────────────────────────────────────────────────────┐
│ GoClaw Session (SQLite) — ALL providers                     │
│ - Messages transcript                                       │
│ - System prompt components (identity, memory, skills)       │
│ - Compaction summaries                                      │
│ - Provider-specific metadata (e.g., xai_response_id)        │
└─────────────────────────────────────────────────────────────┘
                              │
            ┌─────────────────┼─────────────────┐
            ▼                 ▼                 ▼
     ┌──────────┐      ┌──────────┐      ┌──────────┐
     │ Anthropic│      │  OpenAI  │      │   xAI    │
     │          │      │          │      │          │
     │ Uses     │      │ Uses     │      │ Uses     │
     │ messages │      │ messages │      │ messages │
     │ from     │      │ from     │      │ +        │
     │ transcript│     │ transcript│     │ response_│
     │          │      │          │      │ id chain │
     └──────────┘      └──────────┘      └──────────┘
                                               │
                                               ▼
                              ┌─────────────────────────────┐
                              │ xAI Server (30 days)        │
                              │ - Reasoning/thinking        │
                              │ - Server-side tool calls    │
                              │ - Server-side tool results  │
                              └─────────────────────────────┘
```

### Response ID Storage

**Decision:** Persist `response_id` as a column on the sessions table.

Why persist (not just in-memory):
- xAI stores context for 30 days
- GoClaw restart shouldn't break context chain
- Resume exactly where we left off

Why column (not separate table or JSON metadata):
- NULL TEXT columns are cheap in SQLite
- Simple queries, no JOINs
- Bonus: `WHERE xai_response_id IS NOT NULL` shows which sessions used xAI

### Schema Change

```sql
ALTER TABLE sessions ADD COLUMN xai_response_id TEXT;
```

### Provider Implementation

```go
// In XAIProvider
type XAIProvider struct {
    client     *xai.Client
    responseID string  // Current chain head, loaded from session
}

// LoadResponseID called when session loads
func (p *XAIProvider) LoadResponseID(id string) {
    p.responseID = id
}

// GetResponseID called after each turn to persist
func (p *XAIProvider) GetResponseID() string {
    return p.responseID
}
```

Gateway handles persistence:
```go
// After successful xAI turn
if xaiProvider, ok := provider.(*XAIProvider); ok {
    sess.XAIResponseID = xaiProvider.GetResponseID()
    store.UpdateSession(sess)  // Persists to SQLite
}
```

### Fallback Scenarios

| Scenario | Behavior |
|----------|----------|
| **GoClaw restart** | Load response_id, try it — xAI tells us if invalid |
| **response_id expired/invalid** | 404 error → clear ID, retry with transcript |
| **Switch to Anthropic** | Use local transcript (no xAI context) |
| **Switch back to xAI** | Try stored ID, fall back if invalid |
| **New session** | No response_id, fresh start |

### Error Handling

**Don't check dates** — just try the stored response_id. xAI returns 404 if invalid/expired.

```go
func (p *XAIProvider) StreamMessage(ctx context.Context, messages []Message, 
    toolDefs []ToolDefinition, systemPrompt string, ...) (*Response, error) {
    
    req := p.buildRequest(messages, systemPrompt, toolDefs)
    
    // Try with stored response_id if we have one
    if p.responseID != "" {
        req.PreviousResponseID(p.responseID)
    }
    
    resp, err := p.client.StreamChat(ctx, req)
    
    // Handle expired/invalid response_id
    if isNotFoundError(err) && p.responseID != "" {
        L_warn("xai: response_id invalid, falling back to transcript", 
            "responseID", p.responseID)
        
        // Clear invalid ID and retry without it
        p.responseID = ""
        req = p.buildRequest(messages, systemPrompt, toolDefs)  // No previous_response_id
        resp, err = p.client.StreamChat(ctx, req)
    }
    
    if err != nil {
        return nil, err
    }
    
    // Update chain head for next turn
    p.responseID = resp.ID
    
    return p.processStream(resp, onDelta)
}

func isNotFoundError(err error) bool {
    var xaiErr *xai.Error
    return errors.As(err, &xaiErr) && xaiErr.Code == xai.ErrNotFound
}
```

**Simple flow:**
1. Try with stored response_id (if any)
2. 404? Clear it, retry with transcript
3. Success? Store new response_id
4. Continue

### Encrypted Content Option

**Decision:** Skip. We're already sending content to xAI — Zero Data Retention doesn't apply to us. `previous_response_id` is simpler and sufficient.

---

## Server-Side Tools — What We Get

xAI executes these on their infrastructure. We see invocations + status in the stream, but not raw results.

| Tool | Cost | Capability | Status |
|------|------|------------|--------|
| `web_search` | $5/1k | Search + browse web pages | ✅ Enable |
| `x_keyword_search` | $5/1k | X posts by keyword, mode: Latest/Top | ✅ Enable |
| `x_semantic_search` | $5/1k | X posts by semantic meaning | ✅ Enable |
| `code_execution` | $5/1k | Python sandbox | ✅ Enable (configurable) |
| `collections_search` | $2.50/1k | RAG over docs | ❌ Skip — we have memory_search |

**Note:** The spec originally said "x_search" but xAI actually provides two variants:
- `x_keyword_search` — exact keyword matching with mode (Latest/Top)
- `x_semantic_search` — meaning-based search

**Decision on code_execution:** Enable by default but make configurable. While our `exec` tool is more flexible, xAI's sandbox is useful for quick calculations without round-trip latency.

**Key value:** Native X/Twitter access without scraping/auth issues.

### Tool Status Visibility

Server-side tools show status progression in the stream:

```
[Tool Calls]
  - web_search (server, pending)
    Args: {"query":"AI openclaw latest news","num_results":10}
  - x_keyword_search (server, pending)
    Args: {"query":"AI openclaw","limit":10,"mode":"Latest"}
  - web_search (server, completed)
  - x_keyword_search (server, completed)
```

**Implementation:** Log these for transcript/debugging. Status transitions: `pending` → `completed`.

### Citation Handling

When server-side tools run, response includes citations. Volume can be substantial (40+ citations from a single query with multiple search tools).

```
[Citations]
  1. https://x.com/i/status/2022676984606646570
  2. https://www.youtube.com/watch?v=Q7r--i9lLck
  3. https://coder.com/blog/why-i-ditched-openclaw...
  ... (up to 40+ citations)
```

**Implementation options:**
1. **Footnotes** — Append numbered list to response text
2. **Collapsed section** — Show "[42 sources]" that expands on click (TUI/web)
3. **Metadata only** — Store in transcript, don't render inline
4. **Smart filtering** — Only show top N most relevant citations

**Decision:** TBD — depends on output channel (Telegram has message limits, TUI can scroll)

---

## New GoClaw Tools

### Image Generation Tool

**Decision:** Expose as a regular GoClaw tool (like `hass`, `read`, `exec`), not a slash command.

```yaml
name: xai_image
description: Generate images using xAI's Grok image models
parameters:
  prompt: string (required) - Description of the image to generate
  model: string (optional) - grok-2-image, grok-imagine-image, grok-imagine-image-pro
  aspect_ratio: string (optional) - 1x1, 16x9, 9x16, 4x3, 3x4
  resolution: string (optional) - 1K, 2K
```

**Available models:**
- `grok-2-image-1212` (aliases: `grok-2-image`, `grok-2-image-latest`) — default
- `grok-imagine-image` — standard quality
- `grok-imagine-image-pro` — highest quality

**Example usage:**
```
User: Generate a logo for GoClaw
Agent: [calls xai_image tool]
→ Returns: https://imgen.x.ai/xai-imgen/xai-tmp-imgen-xxx.jpeg
```

**Implementation:** Wrap `xai.GenerateImage()` from xai-go.

### Embeddings Provider

**Decision:** Expose xAI embeddings as an alternative provider for `memory_search`.

```go
// In config
{
  "memory_search": {
    "embedding_provider": "xai",  // or "openai", "ollama"
    ...
  }
}
```

**xai-go already supports:**
```go
resp, err := client.Embed(ctx, xai.NewEmbedRequest().
    AddText("some text to embed"))
// Returns: []float32 embedding vector
```

**Benefits:**
- Single API key for LLM + embeddings
- Potentially better semantic alignment with Grok responses
- Simplifies configuration

### Server-Side Tool Usage Tracking

For logging and cost tracking:
```go
type ServerToolUsage struct {
    WebSearch   int `json:"SERVER_SIDE_TOOL_WEB_SEARCH"`
    XSearch     int `json:"SERVER_SIDE_TOOL_X_SEARCH"`
    CodeExec    int `json:"SERVER_SIDE_TOOL_CODE_EXECUTION"`
    // etc.
}
```

Log this per request for billing visibility.

---

## Implementation Architecture

### Standalone Go Package ✅ IMPLEMENTED

**Repository:** https://github.com/roelfdiedericks/xai-go

```
github.com/roelfdiedericks/xai-go/
├── client.go           # Main client, connection management
├── chat.go             # Chat completions (blocking)
├── chat_request.go     # Request builder with fluent API
├── tools.go            # Tool definitions, server-side + function calling
├── models.go           # Model listing, capabilities
├── embed.go            # Embedding generation
├── image.go            # Image generation
├── documents.go        # Document/collection search
├── tokenize.go         # Token counting
├── sample.go           # Text sampling
├── auth.go             # API key info
├── secure.go           # Secure string handling
├── errors.go           # Error types, retry logic
├── proto/              # Generated protobuf code (gitignored, generated via buf)
├── xai-proto/          # Git submodule — xAI protobuf definitions
├── cmd/minimal-client/ # Interactive chat REPL for testing
├── tests/              # Unit tests
├── integration/        # Integration tests (require API key)
├── Makefile            # Build, test, proto generation
└── buf.gen.go.yaml     # Buf generation config
```

**Full API coverage:** All 19 xAI gRPC RPCs implemented.

**Installation:**
```bash
go get github.com/roelfdiedericks/xai-go
```

**Usage:**
```go
import xai "github.com/roelfdiedericks/xai-go"

client, err := xai.FromEnv()  // Uses XAI_APIKEY env var
defer client.Close()

req := xai.NewChatRequest().
    SystemMessage("You are a helpful assistant.").
    UserMessage("What's trending on X?").
    AddTool(xai.NewWebSearchTool()).
    AddTool(xai.NewXSearchTool())

resp, err := client.CompleteChat(ctx, req)
fmt.Println(resp.Content)
fmt.Println(resp.Citations)  // From server-side searches
```

### GoClaw Integration Layer

Thin wrapper in `internal/llm/xai.go`:

```go
import xai "github.com/roelfdiedericks/xai-go"

type XAIProvider struct {
    client *xai.Client
    config XAIConfig
    // ... standard provider fields
}
```

### Testing Strategy

Target **90%+ test coverage** like the Rust client (98 tests).

**Unit tests:**
- Request building / serialization
- Response parsing
- Tool type detection (server-side vs client-side)
- Citation parsing
- Error classification
- Retry logic

**Integration tests (optional, requires API key):**
- Basic chat completion
- Streaming
- Tool calling
- Web search + citations

**Mock server:**
- Consider gRPC mock server for tests
- Avoids hitting real API in CI

---

## GoClaw Integration Plan

### Step 1: Provider Structure

```go
// internal/llm/xai.go

package llm

import (
    "context"
    xai "github.com/roelfdiedericks/xai-go"
)

type XAIProvider struct {
    client      *xai.Client
    config      XAIConfig
    name        string
    model       string
    // ... standard provider fields (metrics, trace, etc.)
}

type XAIConfig struct {
    APIKey       string
    Model        string           // e.g., "grok-4-1-fast-reasoning"
    ServerTools  []string         // ["web_search", "x_keyword_search", "x_semantic_search"]
    MaxTurns     int              // Limit server-side tool iterations
    Timeout      time.Duration
}

func NewXAIProvider(cfg XAIConfig) (*XAIProvider, error) {
    client, err := xai.New(xai.Config{
        APIKey:       xai.NewSecureString(cfg.APIKey),
        Timeout:      cfg.Timeout,
        DefaultModel: cfg.Model,
    })
    if err != nil {
        return nil, err
    }
    
    return &XAIProvider{
        client: client,
        config: cfg,
        name:   "xai",
        model:  cfg.Model,
    }, nil
}
```

### Step 2: Streaming Implementation

```go
func (p *XAIProvider) StreamMessage(ctx context.Context, messages []Message, 
    toolDefs []ToolDefinition, systemPrompt string,
    onDelta func(Delta), opts StreamOptions) (*Response, error) {
    
    // Build xai-go request
    req := xai.NewChatRequest().
        WithModel(p.selectModel(opts))
    
    if systemPrompt != "" {
        req.SystemMessage(systemPrompt)
    }
    
    // Convert messages
    for _, m := range messages {
        switch m.Role {
        case "user":
            req.UserMessage(m.Content)
        case "assistant":
            req.AssistantMessage(m.Content)
        }
    }
    
    // Add server-side tools
    for _, tool := range p.config.ServerTools {
        switch tool {
        case "web_search":
            req.AddTool(xai.NewWebSearchTool())
        case "x_keyword_search", "x_semantic_search":
            req.AddTool(xai.NewXSearchTool())
        case "code_execution":
            req.AddTool(xai.NewCodeExecutionTool())
        }
    }
    
    // Add client-side tools (GoClaw tools)
    for _, td := range toolDefs {
        req.AddTool(p.toXAITool(td))
    }
    
    // Stream response
    stream, err := p.client.StreamChat(ctx, req)
    if err != nil {
        return nil, err
    }
    
    return p.processStream(stream, onDelta)
}
```

### Step 3: Mixed Tool Handling

```go
func (p *XAIProvider) processStream(stream *xai.ChatStream, onDelta func(Delta)) (*Response, error) {
    response := &Response{}
    
    for {
        chunk, err := stream.Next()
        if err == io.EOF {
            break
        }
        if err != nil {
            return nil, err
        }
        
        // Handle text content
        if chunk.Delta != "" {
            onDelta(Delta{Text: chunk.Delta})
            response.Text += chunk.Delta
        }
        
        // Handle tool calls
        for _, tc := range chunk.ToolCalls {
            if p.isServerSideTool(tc.Name) {
                // Server-side: log status, xAI handles execution
                L_debug("xai: server-side tool", 
                    "tool", tc.Name, 
                    "status", tc.Status,
                    "args", tc.Arguments)
            } else {
                // Client-side: pause and return for GoClaw to execute
                response.ToolCalls = append(response.ToolCalls, ToolCall{
                    ID:        tc.ID,
                    Name:      tc.Name,
                    Arguments: tc.Arguments,
                })
            }
        }
        
        // Capture citations
        response.Citations = append(response.Citations, chunk.Citations...)
    }
    
    return response, nil
}

func (p *XAIProvider) isServerSideTool(name string) bool {
    serverTools := map[string]bool{
        "web_search":       true,
        "x_keyword_search": true,
        "x_semantic_search": true,
        "code_execution":   true,
    }
    return serverTools[name]
}
```

---

## Configuration

```json
{
  "llm": {
    "providers": {
      "xai": {
        "type": "xai",
        "apiKey": "${XAI_APIKEY}",
        "model": "grok-4-1-fast-reasoning",
        "serverTools": ["web_search", "x_keyword_search", "x_semantic_search", "code_execution"],
        "maxTurns": 5,
        "timeout": "120s"
      }
    },
    "agent": {
      "models": ["xai/grok-4-1-fast-reasoning", "anthropic/claude-opus-4-5"]
    }
  }
}
```

**Server tools config:**
- `[]` — disabled, pure LLM mode (our tools only)
- `["web_search"]` — web search only
- `["web_search", "x_keyword_search", "x_semantic_search"]` — web + X search (recommended)
- `["web_search", "x_keyword_search", "x_semantic_search", "code_execution"]` — all server tools

**maxTurns:** Limits server-side tool iterations per request (default: 5). Higher = more thorough research, more cost.

**Provider selection:** User-configurable. xAI can be primary, fallback, or one of several options. GoClaw just implements the layer.

---

## Decisions Made

| Question | Decision |
|----------|----------|
| Standalone package vs embedded | ✅ Standalone: github.com/roelfdiedericks/xai-go |
| Proto vendoring | ✅ Git submodule + generate via buf (proto/ is gitignored) |
| Image generation | ✅ Regular tool (xai_image), not slash command |
| Embeddings | ✅ Expose as alternative provider for memory_search |
| Server tools | ✅ All enabled by default, configurable via serverTools array |
| Provider selection | ✅ User-configurable, we just implement the layer |

## Open Questions

### Decided

| # | Question | Decision |
|---|----------|----------|
| 1 | Citation rendering | **Defer** — xAI-specific, noisy, hard to display. Figure out per-channel during implementation. |
| 2 | Connection management | **Create once, reuse** — gRPC handles pooling/reconnection. Keepalive configurable. |
| 3 | Batch API | **Skip** — No use case for interactive GoClaw. |
| 4 | Failover detection | **Use IsRetryable()** — xai-go provides error classification for failover decisions. |
| 5 | Tool result caching | **Skip** — Not applicable. We don't see raw results, and context preservation handles it. |

### Connection Management (Detail)

```go
type XAIProvider struct {
    client *xai.Client
    mu     sync.Mutex
    config XAIConfig
}

func (p *XAIProvider) getClient() (*xai.Client, error) {
    p.mu.Lock()
    defer p.mu.Unlock()
    
    if p.client == nil {
        client, err := xai.New(xai.Config{
            APIKey:           xai.NewSecureString(p.config.APIKey),
            Timeout:          p.config.Timeout,
            KeepaliveTime:    p.config.KeepaliveTime,    // default: 30s
            KeepaliveTimeout: p.config.KeepaliveTimeout, // default: 15s
        })
        if err != nil {
            return nil, err
        }
        p.client = client
    }
    return p.client, nil
}
```

- gRPC handles connection pooling, reconnection, multiplexing internally
- Keepalive configurable via `KeepaliveTime` and `KeepaliveTimeout`
- Only add explicit reconnection logic if problems arise in practice

### GoClaw Config (with keepalive)

```json
{
  "xai": {
    "apiKey": "${XAI_APIKEY}",
    "model": "grok-4-1-fast-reasoning",
    "timeout": "120s",
    "keepaliveTime": "60s",
    "keepaliveTimeout": "15s",
    "serverTools": ["web_search", "x_keyword_search", "x_semantic_search"]
  }
}
```

### Failover Detection (Detail)

Use `IsRetryable()` for failover decisions:

```go
func shouldFailover(err error) bool {
    var xaiErr *xai.Error
    if errors.As(err, &xaiErr) {
        return xaiErr.IsRetryable()  // true for: RateLimit, Unavailable, Timeout, ServerError
    }
    return true  // unknown error, assume unavailable
}
```

| Code | Failover? | Meaning |
|------|-----------|---------|
| `ErrUnavailable` | Yes | Service down |
| `ErrTimeout` | Yes | Request timed out |
| `ErrServerError` | Yes | 5xx error |
| `ErrRateLimit` | Yes* | Rate limited (*or wait using RetryAfter) |
| `ErrResourceExhausted` | Yes | Quota exceeded |
| `ErrAuth` | No | Bad API key — fix config |
| `ErrInvalidRequest` | No | Bad request — fix request |


---

## Testing Checklist

- [ ] gRPC connection establishes
- [ ] Basic chat completion works
- [ ] Streaming chunks arrive correctly  
- [ ] Function calling (our tools) works
- [ ] Server-side tools work (when enabled)
- [ ] Citations parsed correctly
- [ ] Reasoning model auto-selection works
- [ ] Error handling (rate limits, auth, model errors)
- [ ] Failover to Anthropic works
- [ ] 100K+ token context works

---

## References

### Our Implementation
- **xai-go:** https://github.com/roelfdiedericks/xai-go — Go gRPC client (full API coverage)

### xAI Documentation
- xAI Docs: https://docs.x.ai
- gRPC API: https://docs.x.ai/developers/grpc-api-reference
- Protos: https://github.com/xai-org/xai-proto (72 stars, 43 forks)
- Pricing: https://docs.x.ai/developers/models
- Tools: https://docs.x.ai/developers/tools/overview
- Benchmarks: https://artificialanalysis.ai/models/grok-4-1-fast-reasoning/providers

### Rust gRPC Clients (Reference Implementations)

These can serve as reference when building the Go provider:

**fpinsight/xai-grpc-client** (recommended)
- https://github.com/fpinsight/xai-grpc-client
- https://crates.io/crates/xai-grpc-client
- **100% API coverage** — 19/19 RPCs implemented
- 98 unit tests, actively maintained (v0.4.3)
- Full streaming, all 7 tool types, citations, multimodal
- Clean async/await API on tokio + tonic
- Good error handling with retry logic

**0xC0DE666/xai-sdk**
- https://github.com/0xC0DE666/xai-sdk
- Type-safe gRPC clients for all xAI services
- Alternative implementation

### xAI Backend Confirmation

From xAI job postings and tech articles:
- **Backend:** Rust
- **Protocol:** gRPC (with REST mapping for compatibility)
- **Job requirement:** "Expert knowledge of gRPC (unary, response streaming, bi-directional streaming, REST mapping)"

The Grok app itself almost certainly uses gRPC — their entire infrastructure is built around it.

---

## Future Considerations

### Voice Agent API

Real-time voice conversations via WebSocket — could enable voice mode for GoClaw without local STT/TTS.

**Endpoint:** `wss://api.x.ai/v1/realtime`

**Features:**
- Audio in → Text + Audio out (bidirectional)
- Real-time streaming
- Ephemeral tokens for browser-safe auth
- Flat per-minute billing

**Use case:** Hands-free interaction, voice assistants, accessibility.

**Implementation notes:**
- WebSocket, not gRPC (different transport)
- Would need separate client/handler
- Could integrate with existing Telegram voice messages?

**Priority:** Future phase — get core gRPC provider working first, voice is a nice-to-have.

### Batch API

**50% discount** on token costs for async processing.

- Requests queued, processed within 24 hours
- Good for: cron jobs, bulk analysis, non-urgent background tasks
- Could integrate with our cron system for cost savings

**Priority:** Low — nice optimization but not essential.
