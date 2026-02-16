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

| Role | Description |
|------|-------------|
| `owner` | Full access to all tools and features |
| `user` | Limited access based on permissions |

## Role Configuration

Define custom role permissions in `goclaw.json`:

```json
{
  "roles": {
    "user": {
      "tools": ["read", "memory_search", "transcript_search"],
      "skills": "*",
      "memory": "full",
      "transcripts": "own",
      "commands": true
    },
    "guest": {
      "tools": ["read"],
      "skills": [],
      "memory": "none",
      "transcripts": "none",
      "commands": false,
      "systemPrompt": "You are in guest mode. Limited functionality."
    }
  }
}
```

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

1. Generate a password hash:
   ```bash
   goclaw hash-password
   ```

2. Add credentials to user config:
   ```json
   {
     "credentials": [
       {"type": "password", "hash": "<hash>", "label": "web-login"}
     ]
   }
   ```

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
    "allowedRoles": ["user", "owner"],
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

## Supervision

Configure how supervisors interact with agent sessions:

```json
{
  "supervision": {
    "guidance": {
      "prefix": "[Supervisor]: ",
      "systemNote": ""
    },
    "ghostwriting": {
      "typingDelayMs": 500
    }
  }
}
```

### Guidance

Supervisors can inject guidance messages visible only to the agent:

| Option | Default | Description |
|--------|---------|-------------|
| `prefix` | `[Supervisor]: ` | Message prefix |
| `systemNote` | - | Persistent system-level note |

### Ghostwriting

Supervisors can draft messages for the agent:

| Option | Default | Description |
|--------|---------|-------------|
| `typingDelayMs` | 500 | Typing indicator delay |

## Unknown Users

Users not in `users.json` are treated as guests with minimal permissions.

To allow unknown Telegram users:
```json
{
  "roles": {
    "guest": {
      "tools": ["read"],
      "memory": "none"
    }
  }
}
```

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
