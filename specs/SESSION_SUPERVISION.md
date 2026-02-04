# Session Supervision Specification

## Overview

Enable owners/supervisors to monitor, guide, and intervene in agent sessions with other users. Graduated levels of involvement from passive observation to active participation.

## Use Cases

- **Support QA:** Review how agent handled customer interactions
- **Live Monitoring:** Watch active support sessions in real-time
- **Agent Guidance:** Whisper instructions to agent mid-conversation
- **Escalation:** Intervene when agent is struggling or situation is sensitive
- **Training:** Observe agent behavior to improve prompts/knowledge

## Supervision Levels

### Level 1: Session Browser (Historical)

Read-only access to completed/historical sessions.

**Features:**
- List all sessions (filterable by user, date, status)
- Click to view full transcript
- Search within sessions
- Export transcripts

**Access:** Owner only

**UI:** `/sessions` page with list + detail view

```
┌─────────────────────────────────────────────────────────────┐
│ Sessions                                    [Search] [Filter]│
├─────────────────┬───────────────────────────────────────────┤
│ • primary       │ Alice (user:alice)                        │
│   (you)         │ Started: 2026-02-03 14:30                 │
│                 │ Messages: 23                               │
│ • user:alice    │─────────────────────────────────────────  │
│   Feb 3, 14:30  │                                           │
│   23 messages   │ Alice: How do I reset my router?          │
│                 │                                           │
│ • user:bob      │ Ratpup: I can help with that. First,     │
│   Feb 3, 10:15  │ let me find the documentation...          │
│   8 messages    │                                           │
│                 │ ⚙️ docs_search("router reset")            │
│ • cron:twitter  │ ✓ Completed (234ms)                       │
│   Feb 3, 15:33  │                                           │
└─────────────────┴───────────────────────────────────────────┘
```

---

### Level 2: Live Monitor (Watch)

Real-time observation of active sessions.

**Features:**
- See messages as they appear (SSE subscription)
- See tool calls and results
- See typing indicators
- No interaction — pure observation

**Access:** Owner only

**UI:** Same as browser, but with live updates for active sessions

```
┌─────────────────────────────────────────────────────────────┐
│ Session: user:alice                              🔴 LIVE     │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│ Alice: I want to cancel my subscription                     │
│                                                             │
│ Ratpup: I understand. Before I process that...              │
│                                                             │
│ Alice: Your service is too expensive                        │
│                                                             │
│ [Ratpup is typing...]  ← live indicator                     │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

**Implementation:**
- Subscribe to another session's SSE stream
- Same events as the user sees
- Plus tool events if supervisor has thinking enabled

---

### Level 3: Shadow Mode (Guide)

Invisibly guide the agent during live sessions.

**Features:**
- Everything from Level 2 (live monitoring)
- Shadow chat panel — send messages only agent sees
- Agent receives guidance as system context
- User is unaware of supervisor presence

**Access:** Owner only

**UI:**

```
┌─────────────────────────────────────────────────────────────┐
│ Session: user:alice                    🔴 LIVE [Shadow Mode]│
├─────────────────────────────────────────────────────────────┤
│                                                             │
│ Alice: I want to cancel my subscription                     │
│                                                             │
│ Ratpup: I understand. Before I process that, may I ask     │
│         what prompted this decision?                        │
│                                                             │
│ Alice: Your service is too expensive                        │
│                                                             │
│ [Ratpup is typing...]                                       │
│                                                             │
├─────────────────────────────────────────────────────────────┤
│ 👤 SHADOW CHAT (invisible to user)                          │
│                                                             │
│ You: Check if she's eligible for retention discount         │
│ System: ✓ Guidance delivered                                │
│                                                             │
│ You: If she insists, offer 20% off for 6 months            │
│ System: ✓ Guidance delivered                                │
│                                                             │
│ [Type guidance...                              ] [Send 👁️]  │
└─────────────────────────────────────────────────────────────┘
```

**How guidance reaches the agent:**

Option A: Inject as system message
```json
{
  "role": "system",
  "content": "[Supervisor guidance]: Offer 20% retention discount if she insists on canceling."
}
```

Option B: Append to next user message context
```json
{
  "role": "user", 
  "content": "Your service is too expensive",
  "_supervisor_context": "Consider offering retention discount"
}
```

Option C: Real-time context injection
- Guidance appears in agent's context window immediately
- Agent can incorporate before responding
- Requires interrupt/regenerate flow

**Recommended:** Option A — clean, explicit, agent knows it's guidance.

---

### Level 4: Intervention Controls

Active controls to interrupt or redirect the agent.

**Features:**
- **Pause:** Stop agent from responding, review situation
- **Interrupt:** Cancel current generation mid-stream
- **Regenerate:** Discard response, try again (with optional guidance)
- **Queue guidance:** Agent will see this before next response

**UI additions to Shadow Mode:**

```
├─────────────────────────────────────────────────────────────┤
│ 👤 SHADOW CONTROLS                                          │
│                                                             │
│ [⏸️ Pause] [⏹️ Interrupt] [🔄 Regenerate] [📝 Queue Note]   │
│                                                             │
│ Agent status: Responding...                                 │
└─────────────────────────────────────────────────────────────┘
```

**Pause:** 
- Prevents agent from auto-responding to next user message
- Supervisor reviews, adds guidance, then [Resume]
- User sees typing indicator / "Agent is thinking..."

**Interrupt:**
- Stops current generation
- Partial response is discarded (or saved as draft?)
- Supervisor can add guidance, then [Continue] or [Regenerate]

---

### Level 5: Takeover Mode (Direct Intervention)

Supervisor speaks directly in the session.

**Two sub-modes:**

#### 5a: Transparent Takeover
- User is informed supervisor joined
- Messages show as from supervisor
- Clear handoff

```
│ [RoDent (Supervisor) has joined the conversation]           │
│                                                             │
│ RoDent: Hi Alice, I'm stepping in to help with your        │
│         cancellation request. I can offer you...            │
```

#### 5b: Ghostwriting (Controversial)
- Supervisor types, but appears as agent
- User thinks it's still the AI
- Ethically questionable, but sometimes necessary

```
│ Ratpup: I've spoken with my supervisor and we can offer    │
│         you 30% off for the next year.                      │
│                                                             │
│         ↑ Actually typed by RoDent, appears as Ratpup      │
```

**Recommendation:** Default to transparent. Ghostwriting requires explicit toggle with warning.

---

## Implementation

### Backend

**Session subscription:**
```go
// Subscribe to another session's events
func (g *Gateway) SubscribeToSession(ctx context.Context, sessionKey string) (<-chan Event, error) {
    // Verify caller is owner
    if !isOwner(ctx) {
        return nil, errors.New("unauthorized")
    }
    
    // Return channel that mirrors the target session's events
    return g.sessionBroker.Subscribe(sessionKey), nil
}
```

**Guidance injection:**
```go
// Inject supervisor guidance into session
func (g *Gateway) InjectGuidance(ctx context.Context, sessionKey string, guidance string) error {
    // Verify caller is owner
    if !isOwner(ctx) {
        return errors.New("unauthorized")
    }
    
    // Add to session's pending guidance
    session := g.sessions.Get(sessionKey)
    session.PendingGuidance = append(session.PendingGuidance, SupervisorGuidance{
        From:      getUser(ctx).Username,
        Content:   guidance,
        Timestamp: time.Now(),
    })
    
    return nil
}
```

**Context building with guidance:**
```go
func (g *Gateway) buildMessages(session *Session) []Message {
    messages := session.Messages
    
    // Inject any pending supervisor guidance
    if len(session.PendingGuidance) > 0 {
        for _, g := range session.PendingGuidance {
            messages = append(messages, Message{
                Role:    "system",
                Content: fmt.Sprintf("[Supervisor guidance from %s]: %s", g.From, g.Content),
            })
        }
        session.PendingGuidance = nil // Clear after injection
    }
    
    return messages
}
```

### Frontend

**Pages:**
- `/sessions` — Session browser (list + historical view)
- `/sessions/:id` — Session detail (historical or live)
- `/sessions/:id/shadow` — Shadow mode (live + guidance)

**SSE endpoints:**
- `GET /api/sessions/:id/events` — Subscribe to session events (owner only)
- `POST /api/sessions/:id/guidance` — Send guidance (owner only)
- `POST /api/sessions/:id/interrupt` — Interrupt generation (owner only)

### Permissions

| Action | Owner | User | Guest |
|--------|-------|------|-------|
| View own session | ✅ | ✅ | ❌ |
| View other sessions | ✅ | ❌ | ❌ |
| Live monitor | ✅ | ❌ | ❌ |
| Shadow mode | ✅ | ❌ | ❌ |
| Send guidance | ✅ | ❌ | ❌ |
| Interrupt/Pause | ✅ | ❌ | ❌ |
| Takeover | ✅ | ❌ | ❌ |

### Audit Logging

All supervision actions should be logged:

```json
{
  "timestamp": "2026-02-03T16:30:00Z",
  "supervisor": "rodent",
  "session": "user:alice",
  "action": "guidance",
  "content": "Offer 20% retention discount",
  "agent_response_id": "msg_abc123"
}
```

For accountability and training data.

---

## Ethical Considerations

### Transparency
- Users should know they might be monitored (ToS/privacy policy)
- Consider indicator when supervisor is watching? (Optional, configurable)

### Ghostwriting Warnings
If enabled, show warning:
```
⚠️ Ghostwriting Mode: Your messages will appear as if from the agent.
The user will not know a human is responding. Use responsibly.
```

### Recording Consent
- All sessions are recorded (transcripts)
- Users should be informed
- Comply with local regulations (GDPR, etc.)

---

## MVP Scope

**Phase 1: Session Browser**
- [ ] `/sessions` page with list
- [ ] Historical transcript view
- [ ] Basic search/filter

**Phase 2: Live Monitor**
- [ ] Real-time session subscription
- [ ] Live message updates
- [ ] Active session indicators

**Phase 3: Shadow Mode**
- [ ] Shadow chat panel
- [ ] Guidance injection
- [ ] Basic intervention controls (pause/interrupt)

**Phase 4: Advanced**
- [ ] Takeover modes
- [ ] Audit logging
- [ ] Multi-supervisor support
- [ ] Configurable transparency

---

## Agent Prompt Additions

Agent should understand supervisor guidance:

```markdown
## Supervisor Guidance

You may receive messages marked as [Supervisor guidance]. These are instructions
from your owner/supervisor observing the conversation. 

When you receive guidance:
- Incorporate it naturally into your response
- Don't mention that you received guidance to the user
- Follow the instruction unless it conflicts with safety guidelines

Example:
[Supervisor guidance]: Offer 20% discount if customer threatens to cancel

Your response should naturally include the discount offer without saying
"my supervisor told me to..."
```

---

## Summary

| Level | Name | Capability | User Aware? |
|-------|------|------------|-------------|
| 1 | Browser | Read historical transcripts | N/A |
| 2 | Monitor | Watch live sessions | No |
| 3 | Shadow | Guide agent invisibly | No |
| 4 | Intervene | Pause/interrupt/redirect | No |
| 5a | Takeover (transparent) | Speak directly | Yes |
| 5b | Takeover (ghost) | Speak as agent | No |

Shadow Mode (Level 3) with basic intervention (Level 4) is the sweet spot for most use cases.
