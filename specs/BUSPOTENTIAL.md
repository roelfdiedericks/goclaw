# Message Bus Architecture

*Inspired by PicoClaw (sipeed/picoclaw) — researched 2026-02-10*

## The Problem

GoClaw's `gateway.go` is a god object. It handles:
- Channel multiplexing (Telegram, HTTP, TUI)
- Session management
- Tool dispatch
- Response routing
- Event handling (HA subscriptions)
- Cron integration
- Heartbeats

Adding a new channel means surgery. Testing is painful. Everything is tangled.

## The Solution: Message Bus

A pub/sub decoupling layer with two channels:

```go
type MessageBus struct {
    inbound  chan InboundMessage  // user → agent
    outbound chan OutboundMessage // agent → user
    handlers map[string]MessageHandler
}
```

### Message Types

```go
type InboundMessage struct {
    Channel    string            // "telegram", "http", "tui", "cron", "hass"
    SenderID   string            // who sent it
    ChatID     string            // where to reply
    Content    string            // the text
    Media      []string          // attached files
    SessionKey string            // for session lookup
    Metadata   map[string]string // extras (message_id, username, etc.)
}

type OutboundMessage struct {
    Channel string
    ChatID  string
    Content string
    Media   []string  // optional media paths
}
```

## Flow

```
┌──────────────┐
│   Telegram   │──┐
├──────────────┤  │     ┌─────────────┐     ┌─────────────┐
│     HTTP     │──┼────▶│  MessageBus │────▶│  AgentLoop  │
├──────────────┤  │     │  (inbound)  │     │             │
│     TUI      │──┘     │  (outbound) │◀────│  (process)  │
├──────────────┤        └─────────────┘     └─────────────┘
│  HA Events   │──────────────┘                    │
├──────────────┤                                   │
│    Cron      │───────────────────────────────────┘
└──────────────┘
```

1. **Channel receives message** → publishes to `inbound`
2. **Agent loop** consumes from `inbound`, processes, publishes to `outbound`
3. **Channel manager** routes `outbound` back to correct channel's `Send()`

## Why It's Cool

### 1. Decoupling
- Telegram code doesn't import agent code
- Agent doesn't know about Telegram
- They just know about message types

### 2. Adding Channels is Trivial
```go
// New channel? Just implement these:
func (c *NewChannel) Start(ctx context.Context) error {
    // Listen for input, then:
    c.bus.PublishInbound(InboundMessage{...})
}

func (c *NewChannel) Send(msg OutboundMessage) error {
    // Send to your platform
}
```
No gateway.go surgery required.

### 3. Everything Becomes a Publisher
- User messages → `InboundMessage{Channel: "telegram", ...}`
- HA events → `InboundMessage{Channel: "hass", ...}`
- Cron triggers → `InboundMessage{Channel: "cron", ...}`
- Heartbeats → `InboundMessage{Channel: "heartbeat", ...}`

Same flow for everything. Agent doesn't care where it came from.

### 4. Testable
Mock the bus, test components in isolation. No need to spin up Telegram/HTTP to test agent logic.

### 5. Session Key Travels with Message
Agent looks up session by key. Channels don't need to know about sessions.

## PicoClaw's Implementation

Dead simple (~60 lines):

```go
func (mb *MessageBus) PublishInbound(msg InboundMessage) {
    mb.inbound <- msg
}

func (mb *MessageBus) ConsumeInbound(ctx context.Context) (InboundMessage, bool) {
    select {
    case msg := <-mb.inbound:
        return msg, true
    case <-ctx.Done():
        return InboundMessage{}, false
    }
}

// Symmetric for outbound...
```

Agent loop:
```go
func (al *AgentLoop) Run(ctx context.Context) error {
    for {
        msg, ok := al.bus.ConsumeInbound(ctx)
        if !ok {
            continue
        }
        response, _ := al.processMessage(ctx, msg)
        al.bus.PublishOutbound(OutboundMessage{
            Channel: msg.Channel,
            ChatID:  msg.ChatID,
            Content: response,
        })
    }
}
```

## GoClaw Refactor Path

### Phase 1: Extract Bus
- Create `pkg/bus/` with types and MessageBus
- Keep gateway.go working, just route through bus internally

### Phase 2: Channel Independence
- Move channel-specific code to `channels/telegram.go`, `channels/http.go`, etc.
- Each implements `Start()` and `Send()`
- Gateway becomes thin orchestrator

### Phase 3: Unify Event Sources
- HA events → bus publisher
- Cron triggers → bus publisher
- Heartbeats → bus publisher
- All share same InboundMessage flow

### Phase 4: Agent Loop Extraction
- Pull agent processing out of gateway
- Pure message in → message out
- Session management stays with agent

## Trade-offs

**Pros:**
- Clean separation of concerns
- Easy to add channels
- Easy to test
- Unified event handling

**Cons:**
- Extra layer of indirection
- Slight overhead (channel operations)
- Migration effort from current architecture

## Verdict

Gateway.go is past the complexity threshold where this pattern pays off. The bus adds structure without adding much code. PicoClaw proves it works for agent architectures.

Worth doing when there's time for a proper refactor.

---

## Groundwork: Unified Message Types (Implemented)

*Added 2026-02-11*

We've defined unified message types in `internal/types/` as foundation for future refactoring:

### InboundMessage (`internal/types/inbound.go`)

Represents any message that triggers agent processing:

```go
type InboundMessage struct {
    ID, SessionKey string
    User           *user.User
    Source         string            // "telegram", "http", "cron", "hass", etc.
    Text           string
    Images         []ImageAttachment
    ReplyTo        string            // channel-specific target
    Meta           map[string]string // channel-specific metadata
    
    // Behavior flags
    Wake, SkipMirror, SkipAddMessage, FreshContext, IsHeartbeat bool
    SuppressPrefix, EnableThinking string
    
    // Supervision
    Supervisor       *user.User
    InterventionType string
}
```

### Mapping to Current Entry Points

| Current Function | InboundMessage Equivalent |
|------------------|---------------------------|
| `RunAgent(AgentRequest)` | `InboundMessage{Wake: true}` + streaming via events |
| `invokeAgentInternal(...)` | `InboundMessage{Wake: true, SkipMirror: true}` + ch.Send delivery |
| `InvokeAgent(source, msg, suppress)` | `NewInboundMessage(source, owner, msg).WithSuppression(suppress)` |
| `RunAgentForCron(cronReq)` | `NewInboundMessage("cron", user, msg).WithSessionKey("cron:"+jobID)` |
| `InjectMessage(..., invokeLLM=true)` | `InboundMessage{Source: "supervision", Wake: true}.ForSupervision(...)` |
| `InjectMessage(..., invokeLLM=false)` | `InboundMessage{Source: "supervision", Wake: false}` (passive inject) |

### OutboundMessage (`internal/types/outbound.go`)

For final delivery after agent run completes:

```go
type OutboundMessage struct {
    SessionKey, Channel, ReplyTo string
    Text   string
    Media  []string
    Format string // "text", "markdown"
    Source, RunID string
    Suppress bool
    Error    string
}
```

### Unification Path (When Ready)

**Step 1: Create `ProcessMessage(ctx, *InboundMessage) (*DeliveryReport, error)`**

Single entry point that:
1. Resolves session key (default to user session)
2. Builds `AgentRequest` from `InboundMessage` fields
3. Calls existing `RunAgent()` if `Wake=true`
4. Handles delivery based on flags
5. Returns `DeliveryReport`

**Step 2: Update callers to use ProcessMessage**

```go
// Before (invokeAgentInternal)
g.invokeAgentInternal(ctx, u, sessionKey, "hass:entity", message, "EVENT_OK")

// After
msg := types.NewInboundMessage("hass:entity", u, message).
    WithSessionKey(sessionKey).
    WithSuppression("EVENT_OK")
g.ProcessMessage(ctx, msg)
```

**Step 3: Simplify Channel interface**

```go
type Channel interface {
    Name() string
    Deliver(ctx context.Context, msg *types.OutboundMessage) error
    HasUser(u *user.User) bool
}
```

**Step 4: (Future) Extract to bus**

Once all entry points use `ProcessMessage`, extracting to pub/sub is mechanical:
- `ProcessMessage` → `bus.PublishInbound`
- Agent loop consumes from bus, calls existing logic
- Delivery → `bus.PublishOutbound`

### What This Enables Now

- **Documentation**: The types document the mental model
- **Type Safety**: Future changes can use these types incrementally
- **Testing**: Can write tests against `InboundMessage` structure
- **Consistency**: New features can adopt these types from the start

### What This Does NOT Do

- No pub/sub infrastructure yet
- No changes to gateway.go flow
- Existing entry points unchanged (for now)

---

*Reference: https://github.com/sipeed/picoclaw/tree/main/pkg/bus*
