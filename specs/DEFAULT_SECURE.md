# DEFAULT_SECURE.md — Secure-by-Default Configuration

## Problem Statement

Currently, GoClaw uses role-based defaults for sandbox settings:
- **Owner:** Sandbox OFF (trusted)
- **User:** Sandbox ON (untrusted)

However, most GoClaw/OpenClaw users are owners — power users running their own agents. This means the effective default for the majority of users is **sandbox disabled**.

From 1Password's agent security recommendations:
> "Add friction for remote code/command execution"

We have the friction mechanisms (bwrap sandbox, tool isolation), but they're opt-in for owners. This is backwards — security should be the default, convenience should be opt-in.

## Proposed Changes

### 1. Flip the Default: Sandbox ON for Everyone

**Current (`config/users.go`):**
```go
func (e *UserEntry) applyDefaults() {
    isOwner := e.Role == "owner"
    if e.Sandbox == nil {
        val := !isOwner // false for owner, true for others
        e.Sandbox = &val
    }
}
```

**Proposed:**
```go
func (e *UserEntry) applyDefaults() {
    if e.Sandbox == nil {
        val := true // Sandbox ON for everyone by default
        e.Sandbox = &val
    }
}
```

### 2. Setup Wizard: Explicit Security Question

During user creation in the wizard, ask explicitly about security posture:

```
═══════════════════════════════════════
         Security Configuration
═══════════════════════════════════════

GoClaw can sandbox the exec and browser tools to restrict
file access to the workspace directory only.

This is HIGHLY RECOMMENDED for security, but may break some
workflows that need access to files outside the workspace.

? Security mode for this user

  ❯ Secure (recommended) — Sandbox enabled, workspace-only access
    Unrestricted — Full filesystem access (use with caution)
```

If user selects "Unrestricted", show a confirmation:

```
⚠ WARNING: Unrestricted mode allows the agent to:
  • Read any file on the system (including ~/.ssh, credentials)
  • Execute commands with your full user permissions
  • Access browser profiles and stored sessions

This should only be used if you fully trust all skills and prompts.

? Are you sure you want unrestricted access?
  Yes, I understand the risks
  No, keep it secure
```

### 3. User Editor: Same Question on Edit

When editing a user via `goclaw user edit`, if changing sandbox from ON to OFF, show the same warning.

### 4. Config File: Explicit `sandbox` Field

Currently, `sandbox: null` means "use role default". After this change:
- `sandbox: null` → defaults to `true` (secure)
- `sandbox: true` → explicitly secure
- `sandbox: false` → explicitly unrestricted (user consciously chose this)

### 5. Runtime Warning for Unrestricted Mode

When loading a user with `sandbox: false`, log a warning:

```
WARN users: user 'rodent' running in unrestricted mode (sandbox disabled)
```

This creates a paper trail and reminds operators of the security posture.

## Implementation Checklist

### Phase 1: Core Default Change

- [ ] `internal/config/users.go`: Change `applyDefaults()` to default `Sandbox = true`
- [ ] Update comments to reflect new default
- [ ] Add startup warning for `sandbox: false` users

### Phase 2: Wizard Updates

- [ ] `internal/setup/wizard.go`: Add security mode question in `setupUser()`
- [ ] Add confirmation dialog for unrestricted mode
- [ ] Update summary to show security mode

### Phase 3: User Editor Updates

- [ ] `internal/setup/users.go`: Add security mode to `addUser()`
- [ ] Add confirmation when changing sandbox from true → false in `editUser()`
- [ ] Show current security mode in user list

### Phase 4: Documentation

- [ ] Update README with security defaults
- [ ] Add SECURITY.md explaining the threat model
- [ ] Update AGENTS.md with sandbox recommendations

## Migration Path

None. We have two users. If it breaks, we fix it.

## UI/UX Considerations

### Terminology

Avoid technical jargon. Don't say "sandbox" — say:
- "Secure mode" / "Unrestricted mode"
- "Restricted access" / "Full access"
- "Workspace only" / "Full filesystem"

### The "Slutty Mode" Question

Per RoDent's suggestion, the wizard should ask:

```
? How much do you trust your agent?

  ❯ Keep it on a leash (secure, recommended)
    Let it run free (unrestricted, advanced users only)
```

Or more professionally:

```
? File access for this user

  ❯ Workspace only (secure) — Agent can only access workspace files
    Full access (advanced) — Agent can access any file you can
```

### Warning Aesthetics

Use appropriate visual weight:

```
┌─────────────────────────────────────────────────────────────┐
│  ⚠️  WARNING: You are about to enable unrestricted access   │
│                                                             │
│  The agent will be able to read sensitive files including:  │
│    • SSH keys (~/.ssh/)                                     │
│    • Cloud credentials (~/.aws/, ~/.config/gcloud/)         │
│    • Browser data and saved passwords                       │
│    • Any file your user account can access                  │
│                                                             │
│  Only proceed if you fully trust all installed skills       │
│  and understand the security implications.                  │
└─────────────────────────────────────────────────────────────┘
```

## Testing

1. **New user creation:** Verify sandbox defaults to true
2. **Wizard flow:** Verify security question appears and works
3. **User editor:** Verify warning when disabling sandbox
4. **Migration:** Test upgrade path for existing users
5. **Runtime:** Verify warning logged for unrestricted users

## Future Considerations

### Per-Session Override

Consider a `/sandbox off` command that temporarily disables sandbox for a session:
- Requires confirmation
- Auto-reverts after session ends
- Logged for audit trail

### Per-Tool Granularity

Instead of global sandbox flag, consider per-tool settings:
```json
{
  "sandbox": {
    "exec": true,
    "browser": true,
    "write": false
  }
}
```

### Dangerous Command Interception

Beyond sandboxing, intercept known-dangerous patterns:
- `rm -rf /` or `rm -rf ~`
- `curl | sh` or `wget | bash`
- Commands containing credentials

Prompt for confirmation before executing.

## Summary

**The core change is simple:** Default `sandbox` to `true` for all roles.

**The UX change is important:** Make users consciously choose to disable security, with appropriate warnings.

**The goal:** Make the secure path the easy path. Friction for danger, not for safety.
