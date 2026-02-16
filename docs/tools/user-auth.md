---
title: "User Auth"
description: "Authenticate users and elevate their role"
section: "Tools"
weight: 80
---

# User Auth Tool

Authenticate users and elevate their role.

## Purpose

When a guest user provides credentials, this tool authenticates them and elevates their permissions.

## Usage

```json
{
  "identity": "user@example.com",
  "provider": "email"
}
```

| Parameter | Required | Description |
|-----------|----------|-------------|
| `identity` | Yes | User identifier (email, phone, ID) |
| `provider` | No | Identity provider type |

## Flow

1. Guest interacts with the agent
2. Agent requests authentication when needed
3. User provides identifying information
4. Agent calls `user_auth` with the identity
5. If matched in `users.json`, user is elevated

## Configuration

Users must be configured in `users.json`:

```json
{
  "users": [
    {
      "name": "TheRoDent",
      "role": "owner",
      "identities": [
        {"provider": "email", "id": "therodent@example.com"},
        {"provider": "telegram", "id": "123456789"}
      ]
    }
  ]
}
```

## Response

**Successful authentication:**
```json
{
  "authenticated": true,
  "user": {
    "name": "TheRoDent",
    "role": "owner"
  }
}
```

**Failed authentication:**
```json
{
  "authenticated": false,
  "error": "No user found with this identity"
}
```

## Roles

| Role | Description |
|------|-------------|
| `owner` | Full access, all tools |
| `admin` | Administrative access |
| `user` | Standard access |
| `guest` | Limited access |

---

## See Also

- [Roles](../roles.md) — Role-based access control
- [Tools](../tools.md) — Tool overview
- [Configuration](../configuration.md) — User configuration
