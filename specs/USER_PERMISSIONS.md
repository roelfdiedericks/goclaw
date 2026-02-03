# User Data Permissions Specification

## Overview

Define clear boundaries between owner data and other users' data. Prevent cross-user information leakage and unauthorized memory writes.

## User Roles

| Role | Description |
|------|-------------|
| `owner` | Primary user (TheRoDent). Full access to everything. |
| `user` | Authenticated users. Isolated session, limited access. |
| `guest` | Unauthenticated or temporary. Most restricted. |

## Data Types & Permissions

### Memory Files (MEMORY.md, memory/*.md)

Global knowledge store. Owner's curated information.

| Action | Owner | User | Guest |
|--------|-------|------|-------|
| Read (via agent) | ✅ | ❌ | ❌ |
| Write | ✅ | ❌ | ❌ |
| Search (memory_search) | ✅ | ❌ | ❌ |

**Enforcement:**
- Agent instructions prohibit memory writes for non-owners
- Memory search tool checks user role before executing
- Memory files not loaded into context for non-owner sessions

### Transcripts (sessions.db)

Per-user conversation history.

| Action | Owner | User | Guest |
|--------|-------|------|-------|
| Own transcript search | ✅ | ✅ | ❌ |
| Other users' transcripts | ✅ | ❌ | ❌ |
| Cross-user queries | ✅ | ❌ | ❌ |

**Enforcement:**
- transcript_search scoped by session_key/user
- Non-owners can only query their own session
- Owner can query any session (for debugging/support)

### Workspace Files

| Action | Owner | User | Guest |
|--------|-------|------|-------|
| Read workspace | ✅ | Limited | ❌ |
| Write workspace | ✅ | ❌ | ❌ |
| SOUL.md, USER.md | ✅ | ❌ | ❌ |
| AGENTS.md, TOOLS.md | ✅ | ❌ | ❌ |

**Limited read for users:** May access skill files, documentation, but not personal owner files.

## Session Isolation

Each non-owner user gets an isolated session:

```
Session keys:
- owner    → primary (shared across all owner channels)
- alice    → user:alice
- bob      → user:bob
```

**Isolation guarantees:**
- Users cannot see each other's message history
- Users cannot see owner's personal conversations
- Context is built only from their own session + public knowledge

## Agent Behavior Rules

### For Owner Sessions

```markdown
You are talking to the owner. You have full access to:
- Memory files (read/write)
- All transcripts
- Workspace files
- Personal information

Behave as their personal assistant with full context.
```

### For User Sessions

```markdown
You are talking to a user (not the owner). 

RESTRICTIONS:
- DO NOT read or write to MEMORY.md or memory/*.md
- DO NOT reference other users' conversations
- DO NOT share owner's personal information
- DO NOT access USER.md, HOUSEHOLD.md, or other personal files

You may:
- Access their transcript history (for continuity)
- Use skills and tools appropriate for their role
- Reference public documentation and knowledge

Keep their information in their transcript only, not in memory files.
```

### Information Handling

**When a non-owner shares personal info:**
```
User Alice: "My phone number is 555-1234"

CORRECT: Store in transcript (implicit, automatic)
WRONG: Write to MEMORY.md or any file

Later with Alice:
Agent: "I can see from our previous conversation your number is 555-1234"
✅ Retrieved from Alice's transcript
```

**Cross-user protection:**
```
User Bob: "What do you know about Alice?"

CORRECT: "I can't share information about other users."
WRONG: Anything from Alice's transcript or owner's memories
```

## Implementation

### Tool-Level Enforcement

```go
func (t *MemorySearchTool) Execute(ctx context.Context, input Input) (string, error) {
    user := getUserFromContext(ctx)
    
    if user.Role != "owner" {
        return "", errors.New("memory search is only available to the owner")
    }
    
    // ... proceed with search
}

func (t *TranscriptSearchTool) Execute(ctx context.Context, input Input) (string, error) {
    user := getUserFromContext(ctx)
    sessionKey := getSessionKey(ctx)
    
    // Non-owners can only search their own session
    if user.Role != "owner" {
        input.SessionScope = sessionKey  // Force scope to own session
    }
    
    // ... proceed with search
}
```

### Context Building

```go
func (g *Gateway) buildContext(ctx context.Context, user User) Context {
    var files []WorkspaceFile
    
    if user.Role == "owner" {
        // Load everything
        files = loadAllWorkspaceFiles()
    } else {
        // Load only public/allowed files
        files = loadPublicWorkspaceFiles()  // Skills, docs, not USER.md etc.
    }
    
    return Context{
        SystemPrompt: buildSystemPrompt(user.Role),
        Files: files,
        // ...
    }
}
```

### Prompt Injection

System prompt varies by role:

```go
func buildSystemPrompt(role string) string {
    base := loadBasePrompt()
    
    switch role {
    case "owner":
        return base + ownerPermissions
    case "user":
        return base + userRestrictions
    default:
        return base + guestRestrictions
    }
}
```

## File Access Matrix

| File | Owner | User | Guest | Notes |
|------|-------|------|-------|-------|
| MEMORY.md | RW | - | - | Owner's long-term memory |
| memory/*.md | RW | - | - | Daily notes |
| USER.md | RW | - | - | Owner's personal info |
| HOUSEHOLD.md | RW | - | - | Family, pets, vehicles |
| SOUL.md | RW | R | - | Identity (users see personality) |
| AGENTS.md | RW | R | - | Behavior guidelines |
| TOOLS.md | RW | R | R | Tool configuration |
| skills/*.md | R | R | R | Skill documentation |
| HEARTBEAT.md | RW | - | - | Owner's heartbeat tasks |

## Error Messages

When users attempt restricted actions:

```
"I can't access memory files in this session."
"I can only search your conversation history, not other users'."
"That information is not available to me in this context."
```

Keep it simple, don't explain the security model in detail.

## Future Considerations

### Shared Memories
Some information might be intentionally shared:
- Company knowledge base
- FAQ responses
- Public documentation

Could introduce a `shared/` memory directory that all users can access.

### User-Specific Memory
If users become long-term (employees, regular customers):
- `memory/users/alice.md` - Alice-specific notes
- Still isolated, but persisted beyond transcript

### Audit Logging
Track sensitive operations:
- Memory writes (who, when, what)
- Cross-session queries (owner only)
- Failed permission checks

## Summary

| Principle | Implementation |
|-----------|----------------|
| Memory is owner-only | Tool checks, prompt instructions |
| Transcripts are per-user | Session scoping, query filtering |
| No cross-user leakage | Isolation by session_key |
| Owner sees all | Full access for debugging/support |
| Fail secure | Deny by default, explicit grants |
