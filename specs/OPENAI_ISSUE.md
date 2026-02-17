# OpenAI Provider Issues

**Date:** 2026-02-13
**Status:** Investigation
**Affected:** DeepSeek V3.2, Kimi K2.5 (via openai.go provider)

## Symptoms

When switching from Anthropic to OpenAI-compatible providers (DeepSeek, Kimi):
1. Agent starts editing memory files unprompted
2. Sends `NO_REPLY` repeatedly without context
3. Generally behaves "broken" — not responding to direct questions

## Findings

### StopReason Handling Mismatch

**Anthropic returns:**
- `"tool_use"` — when model wants to call a tool
- `"end_turn"` — when model is done responding

**OpenAI-compatible returns:**
- `"stop"` — normal completion
- `"tool_calls"` — when model wants to call tools
- `"length"` — max tokens hit

The openai.go provider manually sets `StopReason = "tool_use"` when tool calls detected:

```go
// openai.go ~line 489
if len(toolCalls) > 0 && toolCalls[0].ID != "" {
    tc := toolCalls[0]
    response.ToolUseID = tc.ID
    response.ToolName = tc.Function.Name
    response.ToolInput = json.RawMessage(tc.Function.Arguments)
    response.StopReason = "tool_use"  // Forced to match Anthropic convention
}
```

### Gateway Tool Detection

Gateway uses `HasToolUse()` which only checks `ToolName`:

```go
func (r *Response) HasToolUse() bool {
    return r.ToolName != ""
}
```

This should work, but if tool calls are malformed/empty:
- `toolCalls[0].Function.Name` could be empty string
- `HasToolUse()` returns false
- Gateway treats it as text-only response

### Potential Empty Response Path

If both `response.Text == ""` AND `HasToolUse() == false`:
- Gateway may return empty/break early
- Could trigger silent reply behavior or default handling

## Hypotheses

1. **Tool call parsing differences** — Kimi/DeepSeek might structure tool calls slightly differently than expected
2. **Streaming chunk assembly** — Tool call deltas might not accumulate correctly
3. **finish_reason timing** — Provider might send `finish_reason` before tool call content is complete
4. **Arguments JSON** — Might be malformed or streamed differently

## Debugging Steps

1. Add verbose logging to openai.go streaming loop:
   ```go
   log.Printf("[openai] chunk: finish_reason=%v, delta.content=%q, delta.tool_calls=%+v", 
       choice.FinishReason, choice.Delta.Content, choice.Delta.ToolCalls)
   ```

2. Log final response state before return:
   ```go
   log.Printf("[openai] final: text=%q, toolName=%q, toolInput=%s, stopReason=%s",
       response.Text, response.ToolName, response.ToolInput, response.StopReason)
   ```

3. Compare raw responses between working (Anthropic) and broken (Kimi/DeepSeek)

## Files to Check

- `goclaw/internal/llm/openai.go` — streaming logic, tool call accumulation
- `goclaw/internal/llm/anthropic.go` — reference implementation
- `goclaw/internal/gateway/gateway.go` — response handling, tool loop

## Related

- Kimi K2.5 was previously working via openai.go provider
- OpenRouter uses same OpenAI-compatible API
- Issue may affect all non-Anthropic providers
