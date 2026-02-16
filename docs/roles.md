---
title: "Roles & Access Control"
description: "Role-based access control (RBAC) for user permissions"
section: "Advanced"
weight: 30
---

# Roles & Access Control

GoClaw provides role-based access control (RBAC) to manage what users can do.

## Overview

The access control system has three layers:

1. **Users** — Who is allowed access (defined in `users.json`)
2. **Roles** — What permissions a user has (defined in `goclaw.json`)
3. **Authentication** — How users prove their identity

## Users

Users are defined in `users.json`:

```json
{
  "users": [
    {
      "name": "TheRoDent",
      "role": "owner",
      "identities": [
        {"provider": "telegram", "id": "123456789"},
        {"provider": "http", "id": "therodent"}
      ],
      "credentials": [
        {"type": "password", "hash": "<argon2-hash>", "label": "web-login"}
      ]
    },
    {
      "name": "Ratpup",
      "role": "user",
      "identities": [
        {"provider": "telegram", "id": "987654321"}
      ],
      "permissions": ["read", "memory_search"]
    }
  ]
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Display name |
| `role` | Yes | `owner` or `user` |
| `identities` | Yes | How to identify this user |
| `credentials` | No | For password/API key auth |
| `permissions` | No | Tool whitelist (non-owners only) |

### Identities

Identities link external accounts to GoClaw users:

| Provider | ID Format | Example |
|----------|-----------|---------|
| `telegram` | Telegram user ID | `123456789` |
| `http` | Username for web UI | `therodent` |
| `local` | `owner` for CLI | `owner` |

### Built-in Roles

GoClaw recognizes three built-in role names:

| Role | Default Permissions | Description |
|------|---------------------|-------------|
| `owner` | Full access (built-in) | Has built-in defaults even if not defined in config |
| `user` | Must define in config | Authenticated user with configurable permissions |
| `guest` | Must define in config | Unauthenticated user — **no access unless explicitly configured** |

**Security by default:** Only `owner` has built-in permissions. Both `user` and `guest` roles must be explicitly defined in the `roles` section of `goclaw.json` or users with those roles will be denied access.

## Role Configuration

Define custom role permissions in `goclaw.json`:

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
    "user": {
      "tools": ["read", "memory_search", "transcript_search"],
      "skills": "*",
      "memory": "full",
      "transcripts": "own",
      "commands": true
    }
  }
}
```

**Note:** The `guest` role is not defined by default (secure by default). See [Unknown Users](#unknown-users-and-guest-role) for how to enable guest access.

| Option | Values | Description |
|--------|--------|-------------|
| `tools` | `"*"` or `["tool1", "tool2"]` | Allowed tools |
| `skills` | `"*"` or `["skill1"]` | Allowed skills |
| `memory` | `"full"`, `"none"` | Memory file access |
| `transcripts` | `"all"`, `"own"`, `"none"` | Transcript search scope |
| `commands` | `true`, `false` | Slash commands enabled |
| `systemPrompt` | string | Custom system prompt |
| `systemPromptFile` | path | Load prompt from file |

## Authentication

### Password Authentication

For web UI access:

```bash
goclaw user set-password <username>
```

This prompts for a password interactively and stores the hash in `users.json`.

### API Key Authentication

For programmatic access:

```json
{
  "credentials": [
    {"type": "apikey", "hash": "<hash>", "label": "automation-key"}
  ]
}
```

### Role Elevation

The `user_auth` tool allows guests to elevate their role by providing credentials:

```json
{
  "auth": {
    "script": "./scripts/auth.sh",
    "allowedRoles": ["user"],
    "rateLimit": 3,
    "timeout": 10
  }
}
```

| Option | Default | Description |
|--------|---------|-------------|
| `script` | - | Auth script path |
| `allowedRoles` | [] | Roles the script can grant |
| `rateLimit` | 3 | Max attempts per minute |
| `timeout` | 10 | Script timeout (seconds) |

**Security rules:**

- **Elevation to `owner` is always blocked** — Even if the auth script returns `owner`, GoClaw refuses. Owner access requires being defined in `users.json`.
- **Role must be in `allowedRoles`** — The script can only grant roles explicitly listed.
- **Role must be defined** — The target role must exist in the `roles` config.

## Supervision

Owners can supervise active sessions — watching, guiding, or ghostwriting agent responses in real-time.

See [Session Supervision](supervision.md) for full documentation on:

- Real-time session monitoring
- Guidance messages (visible only to agent)
- Ghostwriting mode (respond as the agent)
- Interrupt generation
- Configuration options

## Unknown Users and Guest Role

Users not in `users.json` are treated as guests. **By default, no `guest` role is defined**, which means unknown users have no access — this is secure by default.

To allow unknown users (e.g., for a public-facing bot with authentication):

```json
{
  "roles": {
    "guest": {
      "tools": ["read", "user_auth"],
      "skills": [],
      "memory": "none",
      "transcripts": "none",
      "commands": false,
      "systemPrompt": "You are in guest mode. Ask the user for credentials to authenticate."
    }
  }
}
```

**Important:** If you define a `guest` role, consider:
- Keep permissions minimal (read-only, no skills)
- Include `user_auth` tool if you want guests to authenticate
- Use a system prompt that guides the agent to request credentials
- The agent can then use `user_auth` to elevate the guest to `user` role

Unknown users appear in logs:
```
telegram: unknown user ignored userID=999999999
```

## Examples

### Read-only User

```json
{
  "name": "Viewer",
  "role": "user",
  "identities": [{"provider": "telegram", "id": "111111111"}],
  "permissions": ["read"]
}
```

### Power User with Custom Role

In `goclaw.json`:
```json
{
  "roles": {
    "poweruser": {
      "tools": "*",
      "skills": "*",
      "memory": "full",
      "transcripts": "all",
      "commands": true
    }
  }
}
```

In `users.json`:
```json
{
  "name": "PowerUser",
  "role": "poweruser",
  "identities": [{"provider": "telegram", "id": "222222222"}]
}
```

---

## See Also

- [Configuration](configuration.md) — Full config reference
- [Telegram](telegram.md) — Telegram user setup
- [Web UI](web-ui.md) — HTTP authentication
- [Tools](tools.md) — Available tools
