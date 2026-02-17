# Tool Call Sequence Handling

**Status:** Current Behavior Documented  
**Date:** 2026-02-14  
**Author:** RoDent + Ratpup

## Overview

This document describes how GoClaw handles tool calls from LLM providers, including current limitations and future improvement opportunities.

## Current Architecture

### Response Struct (Single Tool)

GoClaw's `Response` struct supports **one tool call per response**:

```go
type Response struct {
    Text       string
    ToolUseID  string          // Single tool call
    ToolName   string
    ToolInput  json.RawMessage
    StopReason string
    // ...
}
```

### Gateway Loop

The gateway processes tool calls sequentially:

```
1. Call StreamMessage() on provider
2. If response has tool_use:
   - Add tool_use message to session
   - Execute the tool
   - Add tool_result message to session
   - Loop back to step 1
3. If no tool_use:
   - Add assistant message to session
   - Return final response
```

## Provider Discrepancies

### OpenAI Provider

Takes the **first** tool call when multiple are returned:

```go
// internal/llm/openai.go ~line 1180
if len(toolCalls) > 0 && toolCalls[0].ID != "" {
    tc := toolCalls[0]  // FIRST tool only
    response.ToolUseID = tc.ID
    response.ToolName = tc.Function.Name
    // ...
}
```

### Anthropic Provider

Takes the **last** tool call (overwrites in loop):

```go
// internal/llm/anthropic.go ~line 514
for _, block := range message.Content {
    switch variant := block.AsAny().(type) {
    case anthropic.ToolUseBlock:
        response.ToolUseID = variant.ID  // Overwrites, keeps LAST
        response.ToolName = variant.Name
        // ...
    }
}
```

### xAI Provider (Planned)

Will take the **first client-side** tool call, consistent with OpenAI pattern.

## Problem: Parallel Tool Calls

Modern LLMs (Claude, GPT-4, Grok) support returning multiple tool calls in a single response for parallel execution.

### What Happens Now

If an LLM returns multiple tool calls (A, B, C):

```
Turn 1:
  LLM: "Call tool A, B, and C"
  Provider: Returns only A (or C for Anthropic)
  Gateway: Executes A, records tool_use(A) + tool_result(A)

Turn 2:
  LLM sees: [user_msg, tool_use(A), tool_result(A)]
  LLM might: 
    - Forget B and C existed
    - Re-request B and C
    - Get confused about state
```

### Why This Matters

1. **Lost context**: Tool calls B and C are never recorded in the session
2. **Inconsistent behavior**: Different providers handle this differently
3. **Potential confusion**: LLM may not understand why some tools weren't executed

### Why It's Acceptable For Now

1. **Our tools are action-oriented**: `hass`, `exec`, `browser`, `read` - typically called one at a time
2. **Think-act-observe pattern**: Agents usually do one action, observe result, then decide next
3. **Re-request works**: LLM often re-requests missing tools on next turn
4. **Server-side tools (xAI)**: `web_search`, `x_search` are handled internally by xAI, not returned to us

## Future Improvements

### Option 1: Batch Execution (Medium Effort)

Execute all tool calls in sequence, aggregate results:

```go
type Response struct {
    Text       string
    ToolCalls  []ToolCall  // Multiple tools
    StopReason string
}

// Gateway batches execution
for _, tc := range response.ToolCalls {
    result := executeTool(tc)
    sess.AddToolUse(tc.ID, tc.Name, tc.Input)
    sess.AddToolResult(tc.ID, result)
}
```

**Pros**: Complete execution, accurate session history  
**Cons**: Changes Response struct, all providers need updates

### Option 2: Parallel Execution (Higher Effort)

Execute multiple tools concurrently:

```go
results := make(chan ToolResult, len(toolCalls))
for _, tc := range toolCalls {
    go func(tc ToolCall) {
        results <- executeTool(tc)
    }(tc)
}
```

**Pros**: Faster for independent tools  
**Cons**: Complexity, error handling, tool ordering

### Option 3: Standardize Provider Behavior (Low Effort)

Ensure all providers return the **first** tool call consistently:

```go
// All providers:
if len(toolCalls) > 0 {
    return toolCalls[0]  // Always first
}
```

**Pros**: Predictable behavior  
**Cons**: Still loses parallel calls

## Recommendation

1. **Short term**: Standardize on "first tool" pattern for all providers (Option 3)
2. **Medium term**: If parallel tool calls become common with our usage patterns, implement Option 1
3. **Monitor**: Log when multiple tool calls are returned but only one is processed

## Logging Improvement

Add logging to detect when this happens:

```go
if len(toolCalls) > 1 {
    L_warn("multiple tool calls returned, processing first only",
        "total", len(toolCalls),
        "processing", toolCalls[0].Name,
        "dropped", toolCallNames(toolCalls[1:]))
}
```

## References

- Gateway tool handling: `internal/gateway/gateway.go` ~lines 1670-1755
- Response struct: `internal/llm/anthropic.go` ~lines 50-65
- OpenAI tool handling: `internal/llm/openai.go` ~lines 1080-1190
- Anthropic tool handling: `internal/llm/anthropic.go` ~lines 510-520
