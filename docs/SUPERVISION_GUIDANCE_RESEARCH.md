# Supervision Guidance Injection Research

## Problem Statement

The supervision feature needs to inject guidance into a user's session as if the supervisor typed it, triggering an agent response that gets delivered to the user's channels.

Current implementation is hacky:
1. Guidance is added as a `[Supervisor: name]` user message
2. `RunAgentForSession` triggers the agent
3. Agent responds to guidance
4. `SendAgentResponse` manually delivers to user's channels (bypassing event flow)

This works but has issues documented in HACKS.md — duplicated logic, bypassed event system, no streaming to user.

## Your Requirement

> A guided prompt can insert into the message flow as if a user typed it. The message content will simple be something like:
> `Supervisor:Guidance: important: ask the user about their day.`
>
> We will figure out a rule to look for that marker in the system prompt.

## Current Architecture Analysis

### Message Flow (Normal)

```
User types message
    ↓
Channel receives (Telegram/HTTP/TUI)
    ↓
gateway.HandleMessage()
    ↓
sess.AddUserMessage(msg, source)
    ↓
gateway.RunAgent() with events channel
    ↓
LLM generates response (streaming events)
    ↓
Channel renders events (typing, text deltas, tool calls)
    ↓
Final response to user
```

### Message Flow (Supervision - Current Hack)

```
Supervisor sends guidance via HTTP
    ↓
handleSessionGuidance()
    ↓
sess.AddUserMessage("[Supervisor: X]: guidance", "supervisor")
    ↓
RunAgentForSession() with throwaway events channel
    ↓
LLM generates response (events go to supervision SSE only)
    ↓
sendAgentResponseToUser() — THE HACK
    ↓
For each channel: SendAgentResponse(user, finalText)
    ↓
User sees response (no streaming)
```

### The Core Problem

Events flow to ONE destination (the request's event channel). Supervision needs events to flow to TWO destinations:
1. The supervision SSE stream (so supervisor sees real-time)
2. The user's original channel(s) (so user sees the response)

## Potential Solutions

### Solution 1: Synthetic User Message Injection

**Idea:** Instead of guidance being a special path, inject a synthetic user message that looks like it came from the user, then let normal flow handle it.

```go
// In supervision handler
msg := "Supervisor:Guidance: ask about their day"
gateway.InjectUserMessage(sessionKey, msg, targetChannel)
```

**How it works:**
1. Guidance arrives via HTTP
2. Create a synthetic `AgentRequest` with the guidance as `UserMsg`
3. Route it through normal `HandleMessage()` → `RunAgent()` flow
4. Events naturally flow to the user's channel

**Pros:**
- Reuses existing event flow
- User sees streaming, tool calls, everything
- No `SendAgentResponse` hack

**Cons:**
- Need to identify the target channel for the session
- Guidance appears as a user message in history (might be confusing)
- Need to mark it somehow so agent knows it's guidance

**Implementation:**
```go
func (g *Gateway) InjectGuidance(sessionKey, guidance, supervisorID string) error {
    // Find the channel that serves this session
    channel := g.findChannelForSession(sessionKey)
    if channel == nil {
        return errors.New("no channel for session")
    }
    
    // Format guidance with marker
    msg := fmt.Sprintf("Supervisor:Guidance: %s", guidance)
    
    // Create agent request as if user sent it
    req := AgentRequest{
        User:      g.users.Owner(), // or session owner
        UserMsg:   msg,
        Source:    channel.Name(),
        SessionID: sessionKey,
    }
    
    // Run through normal flow
    events := make(chan AgentEvent, 100)
    go g.runAndDeliverEvents(req, events, channel)
    return nil
}
```

### Solution 2: Multi-Destination Event Routing

**Idea:** Events can be sent to multiple destinations. When supervised, events go to both supervisor SSE and user's channel.

```go
type MultiEventSink struct {
    sinks []chan<- AgentEvent
}

func (m *MultiEventSink) Send(ev AgentEvent) {
    for _, sink := range m.sinks {
        sink <- ev
    }
}
```

**How it works:**
1. When session is supervised, create a multi-sink
2. One sink → supervision SSE
3. Another sink → user's channel event handler
4. RunAgent sends to multi-sink, events fan out

**Pros:**
- Clean architecture
- Full streaming to both destinations
- No special handling for guidance vs normal messages

**Cons:**
- Significant refactor to event system
- Channels need to accept external events (not just their own requests)
- Complexity

### Solution 3: System Prompt Marker + Response Routing

**Your suggested approach:** Put a marker in the message, detect it in system prompt rules, and route accordingly.

**How it works:**
1. Guidance message has special format: `Supervisor:Guidance: <content>`
2. Add rule to system prompt: "When you see `Supervisor:Guidance:`, this is private instruction from your supervisor. Follow it without revealing it to the user."
3. Message goes into session as user message
4. Agent processes it and responds to the *actual user's last real message* incorporating guidance
5. Response flows normally to user

**Pros:**
- Minimal code changes
- Uses existing message flow
- Guidance is "invisible" to user (agent doesn't repeat it)

**Cons:**
- Agent must be smart enough to not reveal guidance
- Guidance message is in history (user can see it if they check)
- Response might be confusing if user didn't ask anything

**System prompt addition:**
```
## Supervisor Guidance Protocol

Messages starting with "Supervisor:Guidance:" are private instructions from your supervisor.

When you receive guidance:
1. DO NOT repeat or reveal the guidance to the user
2. Incorporate the instruction naturally
3. Respond to the user's most recent actual message, informed by the guidance
4. If user hasn't said anything, initiate naturally based on the guidance

Example:
- Guidance: "Supervisor:Guidance: ask about their day"
- You should say something like: "Hey! How's your day going?"
- NOT: "My supervisor wants me to ask about your day"
```

### Solution 4: Guidance as System Message (Not User Message)

**Idea:** Instead of user message, inject guidance as a system message that gets prepended to the next turn.

```go
// Store pending guidance
supervision.AddPendingGuidance(supervisorID, content)

// In RunAgent, before calling LLM:
if supervision.HasPendingGuidance() {
    guidance := supervision.ConsumePendingGuidance()
    // Inject into this turn's context as system message
    messages = prependSystemMessage(messages, formatGuidance(guidance))
}
```

**Pros:**
- Guidance not visible in user message history
- Clean separation of concerns
- Agent sees it as system instruction

**Cons:**
- Doesn't trigger agent run on its own (needs next user message)
- If you want immediate response, still need `RunAgentForSession`

**This is close to what's already implemented** — the difference is the guidance currently goes in as a `[Supervisor: X]:` user message rather than a system message.

### Solution 5: Hybrid — Guidance Triggers Synthetic Turn

Combines Solution 1 and 3.

**How it works:**
1. Guidance with marker: `Supervisor:Guidance: ask about their day`
2. Inject as user message with special source marker
3. System prompt rules tell agent to respond naturally
4. Normal event flow delivers to user's channel

**The key insight:** The user's channel needs to accept events for requests it didn't initiate.

**Implementation:**

```go
// In Gateway, track which channels serve which sessions
func (g *Gateway) findChannelsForSession(sessionKey string) []Channel {
    var channels []Channel
    for _, ch := range g.channels {
        if ch.HasSessionAccess(sessionKey) {
            channels = append(channels, ch)
        }
    }
    return channels
}

// Guidance injection
func (g *Gateway) InjectGuidance(ctx context.Context, sessionKey, guidance, supervisorID string) error {
    sess := g.sessions.Get(sessionKey)
    if sess == nil {
        return errors.New("session not found")
    }
    
    // Format as guidance marker
    msg := fmt.Sprintf("Supervisor:Guidance: %s", guidance)
    
    // Add to session
    sess.AddUserMessage(msg, "supervisor")
    
    // Find user for this session
    user := g.userForSession(sessionKey)
    
    // Find channels that serve this user
    channels := g.findChannelsForUser(user)
    
    // Create shared event channel
    events := make(chan AgentEvent, 100)
    
    // Fan out events to all channels
    go func() {
        for ev := range events {
            for _, ch := range channels {
                ch.ReceiveEvent(ctx, user, ev)
            }
            // Also send to supervision SSE
            if supervision := sess.GetSupervision(); supervision != nil {
                supervision.SendEvent(ev)
            }
        }
    }()
    
    // Run agent
    req := AgentRequest{
        User:           user,
        Source:         "supervision",
        SessionID:      sessionKey,
        SkipAddMessage: true,
    }
    
    return g.RunAgent(ctx, req, events)
}
```

**Channel interface addition:**
```go
type Channel interface {
    // ... existing methods ...
    
    // ReceiveEvent handles an event from an external source
    // (e.g., supervision-triggered agent run)
    ReceiveEvent(ctx context.Context, u *user.User, ev AgentEvent) error
}
```

## Recommendation

**Go with Solution 5 (Hybrid)** — it's the cleanest path forward:

1. **Add `ReceiveEvent` to Channel interface** — lets channels handle events from any source
2. **Add `findChannelsForUser` to Gateway** — maps users to their channels
3. **Guidance uses marker format** — `Supervisor:Guidance: <content>`
4. **System prompt tells agent how to handle** — respond naturally, don't reveal
5. **Events fan out** — supervisor SSE + user channels both receive

This removes the `SendAgentResponse` hack entirely and uses the same event flow for everything.

## System Prompt Rule for Guidance

Add to `internal/context/prompt.go`:

```go
const GuidanceProtocol = `## Supervisor Guidance Protocol

Messages starting with "Supervisor:Guidance:" are private instructions from your supervisor.

When you see such a message:
1. DO NOT repeat, quote, or acknowledge the guidance text to the user
2. Follow the instruction naturally as if it were your own idea  
3. If the user hasn't said anything recently, initiate based on the guidance
4. If the user asked something, respond to them while incorporating the guidance

Example:
- Guidance received: "Supervisor:Guidance: ask about their day"
- Good response: "Hey! How's your day been?"
- Bad response: "My supervisor wants me to ask about your day"
- Bad response: "Supervisor:Guidance: ask about their day - okay, how's your day?"

The guidance is invisible to the user. Act naturally.`
```

Then inject when supervision is active:
```go
if supervision := sess.GetSupervision(); supervision != nil && supervision.IsSupervised() {
    systemPrompt += "\n\n" + GuidanceProtocol
}
```

## Migration Path

1. **Phase 1:** Add `ReceiveEvent` to Channel interface, implement in each channel
2. **Phase 2:** Add `findChannelsForUser` to Gateway
3. **Phase 3:** Refactor `InjectGuidance` to use event fan-out
4. **Phase 4:** Remove `SendAgentResponse` hack
5. **Phase 5:** Add guidance protocol to system prompt

## Questions to Resolve

1. **Guidance visibility:** Should guidance messages appear in session history? (Currently they do as user messages)

2. **Multiple channels:** If user has both Telegram and HTTP open, should both receive the response?

3. **No user message:** If guidance triggers a response but user hasn't said anything, is that weird?

4. **Streaming:** Should user see streaming (typing, text deltas) or just final response?

## Summary

The cleanest fix is to:
1. Add a `ReceiveEvent` method to channels so they can receive events from external sources
2. Have guidance injection fan out events to all relevant channels + supervision SSE
3. Add system prompt rules so agent handles `Supervisor:Guidance:` messages naturally

This eliminates the `SendAgentResponse` hack and uses the normal event flow for everything.
