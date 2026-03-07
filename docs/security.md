---
title: "Security"
description: "Security model, credentials, and best practices for GoClaw"
section: "Security"
weight: 1
landing: true
---

# Security

GoClaw implements multiple layers of security to protect your system, data, and credentials from both accidental misuse and malicious prompts.

## Topics

| Topic | Description |
|-------|-------------|
| [Panic stop](security-panic-stop.md) | Emergency stop to immediately cancel agent runs |
| [Environment variables and secrets](security-envvars.md) | Why GoClaw uses the config file for secrets; env var substitution |
| [External content wrapping](security-external-content.md) | Protection against prompt injection from external sources |

## Rate Limiting

GoClaw applies rate limiting to protect against brute-force attacks:

**HTTP authentication** — After a failed login attempt, the IP is blocked for 10 seconds. This prevents rapid password guessing while minimizing impact on legitimate users who mistype.

**User authentication tool** — The `user_auth` tool (for in-conversation authentication) limits attempts to 3 per minute per user. This prevents agents from being tricked into brute-forcing passwords.

Rate limits are not currently configurable. They use sensible defaults that balance security with usability.

## Tool Restrictions by Purpose

Certain session purposes automatically restrict which tools are available:

| Purpose | Denied Tools | Rationale |
|---------|--------------|-----------|
| `hass` | `exec`, `write`, `edit` | Home Assistant automations shouldn't modify files or run commands |
| `webhook` | `exec`, `write`, `edit`, `cron` | Webhook-triggered sessions are untrusted by default |

This prevents a malicious webhook payload from using the agent to execute arbitrary commands or write files.

### Custom Restrictions

Override or extend restrictions in `goclaw.json`:

```json
{
  "security": {
    "toolRestrictions": {
      "webhook": {
        "deny": ["exec", "write", "edit", "cron", "skills"]
      },
      "custom_purpose": {
        "deny": ["exec"]
      }
    }
  }
}
```

User-defined restrictions **replace** the defaults for that purpose, they don't merge.

## Access Control

GoClaw enforces role-based access at multiple levels:

### Memory

- **MEMORY.md and daily memory files** are only loaded for owners
- Non-owners cannot see memories from other users' sessions
- Memory graph tools (`memory_update`, `memory_forget`) require owner role

This prevents personal context from leaking to shared or public sessions.

### Transcripts

- Owners see all messages in transcripts
- Non-owners only see messages they sent or received
- Session history is filtered by user identity

### Tools

- Tools can be restricted by role or per-user (see [Tool Permissions](tools.md#tool-permissions))
- Skills can be flagged and require explicit whitelist (see [Skills](skills.md))
- Certain tools are always denied for specific purposes (see above)

### Supervision

- Session supervision (pause, guidance, interrupt) is owner-only
- The supervision API requires owner authentication
- Non-owners cannot observe or control other users' sessions

## Defense Layers

GoClaw's security is layered — no single mechanism provides complete protection:

| Layer | Protects Against |
|-------|------------------|
| [Sandbox](sandbox.md) | File access escape, command injection |
| [URL safety](sandbox.md#browser-url-safety) | SSRF, internal network access |
| [Panic stop](security-panic-stop.md) | Runaway agents, unwanted actions |
| [External content wrapping](security-external-content.md) | Prompt injection from fetched content |
| [Roles](roles.md) | Unauthorized access, privilege escalation |
| [Tool permissions](tools.md#tool-permissions) | Limiting user capabilities |
| Rate limiting | Brute-force attacks |
| Tool restrictions | Malicious webhook/automation payloads |

## Best Practices

1. **Use the sandbox** — Enable bubblewrap for the exec tool in production
2. **Principle of least privilege** — Give users the minimum role they need
3. **Review skills** — Audit skills before enabling, especially flagged ones
4. **Keep panic stop enabled** — It's your emergency brake
5. **Separate workspaces** — Use different workspaces for different trust levels
6. **Monitor sessions** — Use supervision for sensitive operations

## Related

- [Sandbox](sandbox.md) — Execution isolation, file access control, URL safety
- [Roles](roles.md) — User roles and permissions
- [Tools](tools.md) — Tool list and permissions
- [Deployment](deployment.md) — Production security considerations
