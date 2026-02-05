# Managed Browser Specification

## Overview

GoClaw's browser tool needs a Chromium binary. Currently, go-rod auto-downloads from their CDN without user consent. This spec defines a safer, more transparent approach.

## Problems with Current State

- User didn't consent to downloading a ~150MB binary
- go-rod CDN is a trust dependency we don't control
- No verification of what we downloaded
- Could be compromised, outdated, or modified

## Approach: Hybrid with User Consent

### Browser Detection Order

1. **System Chromium/Chrome** — distro-maintained, user already trusts it
2. **Managed Chromium** — downloaded with consent and verification
3. **Skip** — user might not need browser features

```go
// Check in order of preference
browsers := []struct {
    name string
    paths []string
}{
    {"chromium", []string{
        "/usr/bin/chromium",
        "/usr/bin/chromium-browser",
        "/snap/bin/chromium",
    }},
    {"chrome", []string{
        "/usr/bin/google-chrome",
        "/usr/bin/google-chrome-stable",
        "/opt/google/chrome/chrome",
    }},
}
```

### Wizard Flow (System Browser Found)

```
=== Browser Automation ===

Detected: /usr/bin/google-chrome (v122.0.6261.94)

Browser profiles will be stored separately from your personal Chrome data
at: ~/.openclaw/goclaw/browser/profiles/

Use detected Chrome? [Y/n]: y

✓ Browser configured (system Chrome, isolated profiles)
```

### Wizard Flow (No System Browser)

```
=== Browser Automation ===

No system Chromium detected.

Options:
  [1] Download managed Chromium (sandboxed, ~150MB)
  [2] Skip browser features
  
Note: You can also install chromium via your package manager:
  apt install chromium-browser

Choice [1]:
```

## Profile Separation

**Critical:** System binary ≠ system profile.

Using system Chrome/Chromium with the user's default profile would mix agent activity with personal browsing — cookies, history, saved passwords. Bad idea.

**All configurations use isolated profiles:**

| Browser Binary | Profile Location |
|----------------|------------------|
| System Chromium | `~/.openclaw/goclaw/browser/profiles/` |
| System Chrome | `~/.openclaw/goclaw/browser/profiles/` |
| Managed Chromium | `~/.openclaw/goclaw/browser/profiles/` |
| Chrome Extension Relay | User's actual Chrome (user-controlled) |

Our "default" profile is `~/.openclaw/goclaw/browser/profiles/default/` — completely separate from Chrome's default profile. No mapping to system profile. Fresh start, agent builds its own sessions.

**If user wants agent to use their existing logins** → Chrome Extension Relay (`profile="chrome"`), where user explicitly attaches tabs.

## SHA256 Verification

For managed Chromium downloads, we embed known-good SHA256 hashes in the GoClaw binary.

**Problem:** We can't maintain hashes for every version. If go-rod CDN serves an unknown version, we need a fallback.

**Strict mode (default):**
```
SHA256 verification failed or unknown version.
Options:
  [1] Abort (recommended)
  [2] Continue anyway (at your own risk)
```

**Relaxed mode (config flag):**
```json
{
  "browser": {
    "managed": {
      "allowUnverified": true  // Skip verification (not recommended)
    }
  }
}
```

We ship with hashes for known-good versions. If hash unknown or mismatch, user decides.

## Configuration

```json
{
  "browser": {
    "binary": "",              // "" = auto-detect, or explicit path
    "preferSystem": true,      // Try system chrome/chromium first
    "profileDir": "",          // "" = ~/.openclaw/goclaw/browser/profiles
    "managed": {
      "enabled": false,        // Download managed Chromium if no system browser
      "verified": true,        // Require SHA256 verification
      "allowUnverified": false // Allow unverified as fallback
    }
  }
}
```

| Option | Default | Description |
|--------|---------|-------------|
| `binary` | `""` | Explicit browser path, or auto-detect |
| `preferSystem` | `true` | Prefer system Chrome/Chromium over managed |
| `profileDir` | `""` | Profile storage location |
| `managed.enabled` | `false` | Allow downloading managed Chromium |
| `managed.verified` | `true` | Require SHA256 verification |
| `managed.allowUnverified` | `false` | Fallback if hash unknown |

## Implementation Notes

### System Binary with Isolated Profile

```go
launcher.New().
    Bin("/usr/bin/chromium").
    UserDataDir("~/.openclaw/goclaw/browser/profiles/default")
```

System binary, but isolated profile directory. No mixing with user's personal Chrome.

### Chrome Extension Relay

`profile="chrome"` connects to user's running browser via extension. User explicitly attaches tabs. This is the only way to access user's existing sessions — by design.

## Summary

- **Prefer system browser** — user already trusts it, distro handles updates
- **Managed download as fallback** — with consent, verification, sandboxing
- **Always isolate profiles** — never mix with user's personal browser data
- **Verification with fallback** — strict by default, user can override
- **Wizard handles opt-in** — no silent downloads
