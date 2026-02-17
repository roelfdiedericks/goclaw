---
title: "User Auth"
description: "Authenticate guest users and elevate their role mid-session"
section: "Tools"
weight: 80
---

# User Auth Tool

Authenticate guest users via an external script and elevate their session role.

## Overview

The `user_auth` tool enables dynamic authentication for guest users. When a user provides credentials (customer ID, phone, email, etc.), the tool runs an external script to validate them and elevate the user's role mid-session.

**Use cases:**

- **Customer support** — Guest provides customer ID, elevated to "customer" role with order access
- **Public bots** — Unknown Telegram user provides phone, elevated to "family" role
- **Multi-tenant** — External auth determines organization and appropriate role

---

## How It Works

The authentication flow involves three coordinated pieces:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         CONFIGURATION (Admin Setup)                          │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  goclaw.json                         guest role                             │
│  ┌─────────────────────────┐        ┌────────────────────────────────────┐  │
│  │ "auth": {               │        │ "guest": {                         │  │
│  │   "credentialHints": [  │───────▶│   "systemPrompt": "Ask users for   │  │
│  │     {key, label, req},  │        │     their Customer ID, phone, or   │  │
│  │     ...                 │        │     email to authenticate them."   │  │
│  │   ],                    │        │ }                                  │  │
│  │   "script": "auth.sh"   │        └────────────────────────────────────┘  │
│  │ }                       │                                                │
│  └─────────────────────────┘                                                │
│           │                                                                 │
│           │ must accept same keys                                           │
│           ▼                                                                 │
│  ┌─────────────────────────┐                                                │
│  │ auth.sh script          │                                                │
│  │ handles: customer_id,   │                                                │
│  │          phone, email   │                                                │
│  └─────────────────────────┘                                                │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────┐
│                            RUNTIME (Conversation)                            │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌──────────┐    "Hi, I need help"     ┌──────────┐                         │
│  │   User   │ ────────────────────────▶│  Agent   │                         │
│  │ (guest)  │                          │          │                         │
│  └──────────┘                          └────┬─────┘                         │
│       │                                     │                               │
│       │                    Sees systemPrompt: "Ask for customer_id..."      │
│       │                    Sees tool description: "Accepted: customer_id,   │
│       │                                            phone, email"            │
│       │                                     │                               │
│       │    "Could you provide your          │                               │
│       │     customer ID or phone?"          │                               │
│       │◀────────────────────────────────────┘                               │
│       │                                                                     │
│       │    "CUS-12345"                                                      │
│       │─────────────────────────────────────▶                               │
│       │                                     │                               │
│       │                    Agent has enough info, calls user_auth:          │
│       │                    {"credentials": {"customer_id": "CUS-12345"}}    │
│       │                                     │                               │
│       │                                     ▼                               │
│       │                          ┌──────────────────┐                       │
│       │                          │   auth.sh        │                       │
│       │                          │   validates      │                       │
│       │                          │   returns user   │                       │
│       │                          └────────┬─────────┘                       │
│       │                                   │                                 │
│       │                    Session elevated to "customer" role              │
│       │                    New tools now available                          │
│       │                                   │                                 │
│       │    "Welcome back, Alice!          │                                 │
│       │     I can see your orders..."     │                                 │
│       │◀──────────────────────────────────┘                                 │
│       │                                                                     │
│  ┌──────────┐                          ┌──────────┐                         │
│  │   User   │                          │  Agent   │                         │
│  │(customer)│                          │          │                         │
│  └──────────┘                          └──────────┘                         │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### The Three Pieces

| Piece | Purpose | Must Match |
|-------|---------|------------|
| `credentialHints` | Tells agent what credentials to ask for | Script must handle these keys |
| `systemPrompt` | Instructs agent when/how to authenticate | Should reference the credential hints |
| `auth.sh` | Validates credentials, returns user info | Must accept the hinted credential keys |

**Key insight:** The agent sees `credentialHints` in the tool description. Once it has gathered enough of those credentials from the user, it calls the tool. The system prompt guides the conversation; the hints tell it what data it needs.

---

## Configuration

```json
{
  "auth": {
    "enabled": true,
    "script": "/home/user/.goclaw/scripts/auth.sh",
    "credentialHints": [
      {"key": "customer_id", "label": "Customer ID"},
      {"key": "phone", "label": "phone number"},
      {"key": "email", "label": "email address"}
    ],
    "allowedRoles": ["customer", "user"],
    "rateLimit": 3,
    "timeout": 10
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | boolean | `false` | Enable the user_auth tool |
| `script` | string | - | Path to authentication script (required if enabled) |
| `credentialHints` | object[] | `[]` | Credentials the script accepts (see below) |
| `allowedRoles` | string[] | `[]` | Roles the script can return (empty = disabled) |
| `rateLimit` | int | `3` | Max auth attempts per minute |
| `timeout` | int | `10` | Script timeout in seconds |

### Credential Hints

Each hint describes a credential the script accepts:

```json
{"key": "customer_id", "label": "Customer ID", "required": true}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `key` | string | - | JSON field name to pass to script (required) |
| `label` | string | same as `key` | Friendly name the agent uses when asking the user |
| `required` | boolean | `false` | Whether this credential is required |

### How It Appears to the Agent

The tool description includes the formatted hints:

```
Accepted credentials: Customer ID (customer_id) [required], phone number (phone), email address (email).
```

The agent sees:
1. **What to ask for** — the label ("Customer ID")
2. **What key to use** — in parentheses ("customer_id")
3. **What's required** — marked with `[required]`

---

## Role Setup

For the feature to work end-to-end:

### 1. Guest Role (Starting Point)

The guest role needs:
- `user_auth` in tools (so they can authenticate)
- A system prompt that guides the agent to ask for credentials

```json
{
  "roles": {
    "guest": {
      "tools": ["message", "user_auth"],
      "memory": "none",
      "transcripts": "none",
      "commands": false,
      "systemPrompt": "You are helping a guest user. They have limited access. To unlock more features, ask them to provide their customer ID, phone number, or email address so you can authenticate them using the user_auth tool."
    }
  }
}
```

### 2. Target Roles (After Elevation)

Define the roles users can be elevated to:

```json
{
  "roles": {
    "customer": {
      "tools": ["message", "web_search", "order_lookup"],
      "memory": "none",
      "transcripts": "own",
      "commands": false,
      "systemPromptFile": "prompts/customer-support.md"
    },
    "user": {
      "tools": ["message", "web_search", "web_fetch"],
      "memory": "none",
      "transcripts": "own",
      "commands": true
    }
  }
}
```

### 3. Auth Config

Tie it together:

```json
{
  "auth": {
    "enabled": true,
    "script": "/home/user/.goclaw/scripts/auth.sh",
    "credentialHints": ["customer_id", "phone", "email"],
    "allowedRoles": ["customer", "user"]
  }
}
```

---

## Usage

The agent calls the tool with collected credentials:

```json
{
  "credentials": {
    "customer_id": "CUS-12345"
  }
}
```

Or with multiple credentials:

```json
{
  "credentials": {
    "phone": "+1234567890",
    "pin": "1234"
  }
}
```

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `credentials` | object | Yes | Key-value pairs matching your `credentialHints` |

---

## Script Interface

The auth script receives credentials and returns a JSON result.

### Input

Credentials are passed as JSON on **stdin**:

```json
{"customer_id": "CUS-12345", "phone": "+1234567890"}
```

### Output — Success

```json
{
  "success": true,
  "user": {
    "name": "Alice Smith",
    "username": "alice",
    "role": "customer",
    "id": "CUS-12345"
  },
  "message": "User has 3 pending orders. Offer to help with order status."
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `success` | Yes | `true` for successful auth |
| `user.name` | Yes | Display name |
| `user.username` | Yes | Username/handle |
| `user.role` | Yes | Role to elevate to (must be in `allowedRoles`) |
| `user.id` | Yes | User identifier |
| `message` | No | Context/instructions for the agent |

### Output — Failure

```json
{
  "success": false,
  "message": "Customer ID not found. Ask user to verify or try their email."
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `success` | Yes | `false` for failed auth |
| `message` | Yes | Guidance for the agent (what to say/try next) |

---

## Example Script

Here's a simple script that looks up users from a JSON file:

```bash
#!/bin/bash
# auth.sh - Simple file-based authentication

USERS_FILE="${GOCLAW_AUTH_USERS:-$HOME/.goclaw/auth-users.json}"

# Read credentials from stdin
CREDS=$(cat)

# Extract credential values (try each hint)
CUSTOMER_ID=$(echo "$CREDS" | jq -r '.customer_id // empty')
PHONE=$(echo "$CREDS" | jq -r '.phone // empty')
EMAIL=$(echo "$CREDS" | jq -r '.email // empty')

# Use first non-empty as lookup key
LOOKUP_KEY="${CUSTOMER_ID:-${PHONE:-$EMAIL}}"

if [ -z "$LOOKUP_KEY" ]; then
    echo '{"success": false, "message": "No credential provided. Ask for customer_id, phone, or email."}'
    exit 0
fi

# Look up user
USER=$(jq -r --arg key "$LOOKUP_KEY" '.[$key] // empty' "$USERS_FILE" 2>/dev/null)

if [ -z "$USER" ] || [ "$USER" = "null" ]; then
    echo '{"success": false, "message": "User not found. Ask them to try a different identifier."}'
    exit 0
fi

# Return success
echo "$USER" | jq '{
    success: true,
    user: {name: .name, username: .username, role: .role, id: .id},
    message: .context
}'
```

### Users File

Create `~/.goclaw/auth-users.json`:

```json
{
  "CUS-12345": {
    "name": "Alice Smith",
    "username": "alice",
    "role": "customer",
    "id": "CUS-12345",
    "context": "VIP customer. Has 3 pending orders."
  },
  "+1234567890": {
    "name": "Bob Jones",
    "username": "bob",
    "role": "user",
    "id": "bob@example.com",
    "context": "Standard user."
  }
}
```

### Setup

```bash
chmod +x ~/.goclaw/scripts/auth.sh
```

See also: [examples/auth-script.sh](https://github.com/roelfdiedericks/goclaw/blob/master/examples/auth-script.sh)

---

## Security

### Protections

| Protection | Description |
|------------|-------------|
| **Owner block** | Cannot elevate to "owner" role (hardcoded, even if in allowedRoles) |
| **Role whitelist** | Script can only return roles in `allowedRoles` |
| **Role validation** | Returned role must exist in `roles` config |
| **Rate limiting** | Max attempts per minute (default 3) |
| **Timeout** | Script killed if it exceeds timeout |
| **Session-scoped** | Elevation is lost when session ends |

### Rate Limiting

After `rateLimit` attempts in one minute, further attempts are blocked:

```json
{
  "success": false,
  "message": "Too many authentication attempts. Please wait a minute."
}
```

### Script Security

- The script runs with GoClaw's permissions
- Credentials are passed via stdin (not command line) for security
- Script path is from admin config, not user input
- Use absolute paths and restrict script permissions

---

## Response Format

### Successful Authentication

```json
{
  "success": true,
  "user": {
    "name": "Alice Smith",
    "username": "alice",
    "role": "customer",
    "id": "CUS-12345"
  },
  "message": "User has 3 pending orders."
}
```

### Failed Authentication

```json
{
  "success": false,
  "message": "Customer ID not found."
}
```

### Rate Limited

```json
{
  "success": false,
  "message": "Too many authentication attempts. Please wait a minute."
}
```

---

## Troubleshooting

| Issue | Cause | Solution |
|-------|-------|----------|
| Tool not available | `auth.enabled` is false | Set `auth.enabled: true` |
| "No auth script configured" | `auth.script` not set | Configure script path |
| "Role not permitted" | Role not in `allowedRoles` | Add role to `allowedRoles` |
| "Role not defined" | Role not in `roles` config | Define the role |
| Agent asks for wrong info | `credentialHints` don't match prompt | Align hints with system prompt |
| Script timeout | Script takes too long | Increase `timeout` or optimize |

### Testing Your Script

```bash
echo '{"customer_id": "CUS-12345"}' | ./auth.sh
```

---

## Complete Example

Here's a full working configuration:

```json
{
  "auth": {
    "enabled": true,
    "script": "/home/user/.goclaw/scripts/auth.sh",
    "credentialHints": [
      {"key": "customer_id", "label": "Customer ID"},
      {"key": "phone", "label": "phone number"},
      {"key": "email", "label": "email address"}
    ],
    "allowedRoles": ["customer", "user"],
    "rateLimit": 3,
    "timeout": 10
  },
  "roles": {
    "guest": {
      "tools": ["message", "user_auth"],
      "memory": "none",
      "transcripts": "none",
      "commands": false,
      "systemPrompt": "You are helping a guest user with limited access. To provide full assistance, you need to authenticate them. Ask for their Customer ID, phone number, or email address. Once you have any of these, use the user_auth tool to verify their identity."
    },
    "customer": {
      "tools": ["message", "web_search", "order_lookup", "ticket_create"],
      "memory": "none",
      "transcripts": "own",
      "commands": false,
      "systemPrompt": "You are helping an authenticated customer. You have access to their order history and can create support tickets."
    },
    "user": {
      "tools": ["message", "web_search", "web_fetch"],
      "memory": "none",
      "transcripts": "own",
      "commands": true
    }
  }
}
```

---

## See Also

- [Roles & Access Control](../roles.md) — Role definitions and permissions
- [Configuration](../configuration.md) — Full auth config reference
- [Tools](../tools.md) — Tool overview
