# ROLES.md — Role-Based Access Control Specification (v1)

## Overview

Granular permissions for different user roles. Roles are configured in `goclaw.json`, users reference roles via `users.json`.

## Architecture

### users.json (unchanged)

User identity and authentication. Role is a reference string.

```json
[
  {
    "id": "telegram:123456",
    "name": "RoDent",
    "role": "owner",
    "passwordHash": "..."
  },
  {
    "id": "telegram:789012",
    "name": "Ames",
    "role": "family"
  },
  {
    "id": "telegram:345678",
    "name": "Guest",
    "role": "user"
  }
]
```

### goclaw.json (roles section)

```json
{
  "roles": {
    "owner": {
      "tools": "*",
      "skills": "*",
      "memory": "full",
      "transcripts": "all",
      "commands": true
    },
    "family": {
      "tools": ["hass", "web_search", "web_fetch", "message", "browser"],
      "skills": ["home-assistant"],
      "memory": "none",
      "transcripts": "own",
      "commands": true,
      "systemPrompt": "You are helping a family member with home automation.",
      "systemPromptFile": "prompts/family-guidelines.md"
    },
    "user": {
      "tools": ["web_search", "web_fetch", "message"],
      "skills": [],
      "memory": "none",
      "transcripts": "own",
      "commands": false,
      "systemPromptFile": "prompts/customer.md"
    },
    "guest": {
      "tools": ["message"],
      "skills": [],
      "memory": "none",
      "transcripts": "none",
      "commands": false,
      "systemPrompt": "You can only chat. No tools or special capabilities are available."
    }
  }
}
```

## Permission Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `tools` | `"*"` or `string[]` | `[]` | Tool allowlist |
| `skills` | `"*"` or `string[]` | `[]` | Skill allowlist |
| `memory` | `"full"` \| `"none"` | `"none"` | Memory files + memory tool access |
| `transcripts` | `"all"` \| `"own"` \| `"none"` | `"none"` | Transcript search scope |
| `commands` | `boolean` | `false` | Slash commands enabled |
| `systemPrompt` | `string` | `""` | Inline role-specific prompt |
| `systemPromptFile` | `string` | `""` | Path to role prompt file (relative to workspace) |

## Permission Details

### tools

What tools the user can invoke.

| Value | Description |
|-------|-------------|
| `"*"` | All tools allowed |
| `["tool1", "tool2"]` | Only listed tools allowed |
| `[]` | No tools (chat only) |

Tools not in the allowlist are **not injected** into the agent's context at all.

### skills

What skills are available to the user.

| Value | Description |
|-------|-------------|
| `"*"` | All skills allowed |
| `["skill1", "skill2"]` | Only listed skills allowed |
| `[]` | No skills |

Skills not in the allowlist are not loaded. If a skill's underlying tools are denied, tool calls within the skill will fail.

### memory

Access to workspace memory files.

| Value | Description |
|-------|-------------|
| `"full"` | MEMORY.md and memory/*.md loaded into context, memory tools available |
| `"none"` | No memory files loaded, memory tools not injected |

Memory is personal to the owner. Non-owner roles typically have `"none"`.

### transcripts

Access to conversation history via transcript tool.

| Value | Description |
|-------|-------------|
| `"all"` | Can search/view all transcripts |
| `"own"` | Can only search/view own conversations |
| `"none"` | No transcript access, tool not injected |

### commands

Whether slash commands are enabled.

| Value | Description |
|-------|-------------|
| `true` | All `/commands` work (`/help`, `/thinking`, `/model`, `/transcript`, etc.) |
| `false` | `/anything` is treated as literal text, sent to agent as regular message |

When disabled, users cannot change model, thinking mode, or access any command functionality. This prevents information leakage through configuration commands.

### systemPrompt / systemPromptFile

Role-specific additions to the system prompt.

- `systemPrompt`: Inline text added to system prompt
- `systemPromptFile`: Path to markdown file (relative to workspace root)
- **Both can be specified** — content is combined (inline first, then file)

Example use cases:
- Customer support role with specific guidelines
- Family role with friendly tone instructions
- Restricted role explaining limitations

## Built-in Guest Role

If a user's role is not found in config, the built-in `guest` defaults apply:

```go
var defaultGuestRole = Role{
    Tools:       []string{},   // No tools
    Skills:      []string{},   // No skills  
    Memory:      "none",
    Transcripts: "none",
    Commands:    false,
}
```

To customize guest behavior, explicitly define a `guest` role in config.

## Implementation Notes

### Tool Filtering

When building tool list for LLM context:
1. Get user's role from config
2. If `tools: "*"`, include all registered tools
3. Otherwise, include only tools in the allowlist
4. Disallowed tools are never injected (agent doesn't know they exist)

```go
func (g *Gateway) getToolsForUser(user *User) []Tool {
    role := g.config.Roles[user.Role]
    if role == nil {
        role = defaultGuestRole
    }
    
    if role.Tools == "*" {
        return g.allTools
    }
    
    var allowed []Tool
    for _, t := range g.allTools {
        if contains(role.Tools, t.Name()) {
            allowed = append(allowed, t)
        }
    }
    return allowed
}
```

### Command Handling

```go
func (g *Gateway) handleMessage(user *User, text string) {
    role := g.getRole(user)
    
    if strings.HasPrefix(text, "/") && role.Commands {
        g.executeCommand(user, text)
    } else {
        // Regular message - "/" is literal text if commands disabled
        g.sendToAgent(user, text)
    }
}
```

### Memory Enforcement

When `memory: "none"`:
1. Skip loading `MEMORY.md` into context
2. Skip loading `memory/*.md` files
3. Do not inject `memory_search` tool
4. Do not inject `memory` tool (if separate)

### Transcript Filtering

For `transcripts: "own"`:
```go
func (t *TranscriptTool) search(user *User, query string) []Result {
    role := getRole(user)
    
    results := t.searchAll(query)
    
    if role.Transcripts == "own" {
        results = filterByUser(results, user.ID)
    } else if role.Transcripts == "none" {
        return []Result{}
    }
    
    return results
}
```

### Session Isolation

Every user gets their own session with their role-based permissions:
- Tool list filtered per user
- Memory context per user's permission
- Transcript access per user's permission

### Fail Secure

- Unknown role → use built-in guest defaults (deny all)
- Log warning on startup for users with undefined roles
- Never expose permission errors to the LLM

## Example Roles

### Owner (Full Access)
```json
"owner": {
  "tools": "*",
  "skills": "*",
  "memory": "full",
  "transcripts": "all",
  "commands": true
}
```

### Family (Home Control)
```json
"family": {
  "tools": ["hass", "web_search", "web_fetch", "message", "browser"],
  "skills": ["home-assistant"],
  "memory": "none",
  "transcripts": "own",
  "commands": true,
  "systemPrompt": "You are helping a family member. Be friendly and helpful."
}
```

### Customer/User (Limited)
```json
"user": {
  "tools": ["web_search", "web_fetch", "message"],
  "skills": ["customer-support"],
  "memory": "none",
  "transcripts": "own",
  "commands": false,
  "systemPromptFile": "prompts/customer-support.md"
}
```

### Guest (Minimal)
```json
"guest": {
  "tools": ["message", "user_auth"],
  "skills": [],
  "memory": "none",
  "transcripts": "none",
  "commands": false,
  "systemPrompt": "You can only chat. Ask users for their ID to authenticate them."
}
```

## Role Elevation

Guest users can be authenticated and elevated to a different role mid-session using the `user_auth` tool.

### How It Works

1. Guest user connects with minimal permissions
2. Agent (via system prompt/skills) asks for identification
3. Agent calls `user_auth` tool with provided credentials
4. Tool validates and returns new role
5. Session is elevated - new permissions apply immediately

### Configuration

Top-level in `goclaw.json`:
```json
"auth": {
  "enabled": true,
  "script": "/path/to/validate-user.sh",
  "allowedRoles": ["customer", "user"],
  "rateLimit": 3,
  "timeout": 10
}
```

- `script` - Path to validation script
- `allowedRoles` - Explicit list of roles script can return (empty = disabled)
- `rateLimit` - Max attempts per minute (default 3)
- `timeout` - Script timeout in seconds (default 10)

The `user_auth` tool is only registered when `auth.enabled = true`.

Elevation fails if:
- Role is `owner` (hardcoded block, even if in allowedRoles)
- Role not in `allowedRoles` list
- Role not defined in `roles` config section

### Script Interface

**Input:** Script receives credentials via:
- **stdin**: JSON object for complex credentials
- **arguments**: Simple `key=value` pairs

**Output JSON (success):**
```json
{
  "success": true,
  "user": {
    "name": "Alice Smith",
    "username": "alice",
    "role": "customer",
    "id": "CUS-12345"
  },
  "message": "User authenticated as Alice Smith (customer). They have 3 pending orders. Greet them and offer help with orders."
}
```

**Output JSON (failure):**
```json
{
  "success": false,
  "message": "Customer ID not found. Ask user to double-check or try their email address instead."
}
```

The `message` field is for the agent to interpret - it can contain instructions, context, or suggestions for how to respond. Not displayed directly to user.

### Security Constraints

- Cannot elevate to `owner` role (hardcoded, even if in allowedRoles)
- Role must be in `allowedRoles` list (empty = elevation disabled)
- Role must exist in `roles` config section
- Rate limited: configurable per minute (default 3)
- Elevation is session-scoped (lost on disconnect)
- Logged via standard logging

### Example Flow

```
Guest: "Hi, I need help with my order"
Agent: "I'd be happy to help! Could you please provide your customer ID?"
Guest: "CUS-12345"
Agent: [calls user_auth with id="CUS-12345"]
Tool: {success: true, role: "customer", userId: "alice@example.com"}
Agent: "Welcome back, Alice! I can now access your order history. What do you need help with?"
[Session now has "customer" role permissions]
```

## Security Notes

- **Fail closed** — unknown role = deny all (guest defaults)
- **Explicit allowlists** — tools must be explicitly allowed, no auto-include
- **Owner escape hatch** — owner role should always exist with full access
- **Command isolation** — disabled commands prevent config/info leakage
- **Log denials** — log permission denials for debugging (not to LLM)
- **No tool hints** — disallowed tools not mentioned to agent at all

## Future Considerations (v2+)

These are explicitly out of scope for v1:

- **HASS entity-level filtering** — allow/deny specific entities
- **Cron per-user** — users create their own cron jobs
- **Channel restrictions** — limit roles to specific channels
- **Time-based access** — active hours per role
- **Rate limiting** — request limits per role
- **Workspace isolation** — separate workspace directories per role
- **Fine-grained commands** — allow some commands but not others
