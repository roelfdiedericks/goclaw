# HTTP API Specification

## Overview

GoClaw exposes a minimal HTTP interface for web UI and API access. Security is paramount — no defaults, no leaky headers, no trust assumptions.

**Philosophy:** HTTP is for *using* the agent, not *administering* it. Admin happens via CLI/TUI where you already have filesystem access.

## Users & Authentication

### users.json

Users are managed in a separate `users.json` file (not in `goclaw.json`).

**Location (same priority as goclaw.json):**
1. `./users.json` (current directory - highest priority)
2. `~/.openclaw/users.json` (fallback)

**No plaintext passwords. Ever.** Passwords are stored as Argon2id hashes.

```json
{
  "rodent": {
    "name": "TheRoDent",
    "role": "owner",
    "telegram_id": "123456789",
    "http_password_hash": "$argon2id$v=19$m=65536,t=3,p=4$..."
  },
  "alice": {
    "name": "Alice",
    "role": "user",
    "telegram_id": "987654321",
    "http_password_hash": "$argon2id$v=19$m=65536,t=3,p=4$..."
  }
}
```

**The key IS the username.** No separate `http_username` field needed.

| Field | Required | Description |
|-------|----------|-------------|
| (key) | Yes | Username (HTTP auth, session key). Max 32 chars, `^[a-z][a-z0-9_]{0,31}$` |
| `name` | Yes | Display name |
| `role` | Yes | `"owner"` or `"user"` |
| `telegram_id` | No | Telegram user ID (from @userinfobot) |
| `http_password_hash` | No | Argon2id hash of password |

**Rules:**
- At least one identity required per user (telegram_id or http_password_hash)
- Same user can have both Telegram and HTTP identities → same session

### Session Keys

| User | Session Key |
|------|-------------|
| Owner (e.g., `rodent`) | `primary` |
| Other users (e.g., `alice`) | `user:alice` |

**Owner always uses `"primary"` session** — this is non-negotiable. All channels (Telegram, HTTP, TUI, cron) for the owner share the same session.

Non-owner users get isolated sessions keyed by their username: `user:<username>`.

### User Management CLI

Users are created via CLI tool (not manual JSON editing):

```bash
# Add owner user
goclaw user add rodent --name "TheRoDent" --role owner

# Set Telegram identity
goclaw user set-telegram rodent 123456789

# Set HTTP password (prompts for password, hashes it)
goclaw user set-http rodent
Password: ********
Confirm: ********
HTTP password set for user 'rodent'

# List users
goclaw user list

# Delete user (preserves session data)
goclaw user delete alice

# Delete owner (requires --force)
goclaw user delete rodent --force

# Delete user AND purge all session data (destructive!)
goclaw user delete alice --purge
```

The CLI handles password hashing. Users never touch raw hashes.

### User Deletion

**Default behavior (`goclaw user delete alice`):**
- Removes user from `users.json`
- Does NOT touch SQLite session data
- User can no longer authenticate
- Session history preserved (can recreate user later)

**With --purge flag (`goclaw user delete alice --purge`):**
- Removes user from `users.json`
- ALSO deletes session data from SQLite (`user:alice`)
- Deletes compaction history
- Irreversible - requires typing `DELETE <username>` to confirm

**Deleting owner (`goclaw user delete rodent --force`):**
- Requires `--force` flag
- Warning: this breaks everything until a new owner is configured
- Does NOT delete `primary` session data (use `--purge` for that too)

## HTTP Security

### Authentication

**Basic Auth. Required. Always.**

```
Authorization: Basic base64(username:password)
```

- No users with HTTP credentials? HTTP server disabled.
- No localhost bypass. No special cases. No "trusted proxies".

### Auth Flow

```go
func authMiddleware(users *user.Registry) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            ip := getSourceIP(r)
            
            // Rate limit check
            if isRateLimited(ip) {
                writeUnauthorized(w)
                return
            }
            
            username, password, ok := r.BasicAuth()
            if !ok {
                recordFailure(ip)
                writeUnauthorized(w)
                return
            }
            
            // Look up user by username (key in users.json)
            user := users.Get(username)
            if user == nil {
                recordFailure(ip)
                writeUnauthorized(w)
                return
            }
            
            // Verify password against stored hash
            if !user.VerifyHTTPPassword(password) {
                recordFailure(ip)
                writeUnauthorized(w)
                return
            }
            
            // Success - attach user to context
            clearFailure(ip)
            ctx := context.WithValue(r.Context(), userContextKey, user)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}
```

### Rate Limiting

**10 second delay per source IP after failed authentication.**

```go
var failedIPs sync.Map // map[string]time.Time

func isRateLimited(ip string) bool {
    if lastFail, ok := failedIPs.Load(ip); ok {
        return time.Since(lastFail.(time.Time)) < 10*time.Second
    }
    return false
}
```

### Source IP

**`r.RemoteAddr` only. No forwarded headers.**

```go
func getSourceIP(r *http.Request) string {
    host, _, _ := net.SplitHostPort(r.RemoteAddr)
    return host
}
```

`X-Forwarded-For`, `X-Real-IP`, etc. can be faked. We don't trust them.

If you're behind a reverse proxy:
- All requests come from proxy IP
- One failed auth = 10s delay for ALL requests through that proxy
- That's the proxy operator's problem

### Response Headers

**Give attackers nothing.**

No:
- `Server: GoClaw/0.0.1` ❌
- `X-Powered-By: Go` ❌
- `X-Request-Id: ...` ❌
- `Date:` ❌
- Verbose error messages ❌

**Every auth error looks identical:**

```go
func writeUnauthorized(w http.ResponseWriter) {
    w.Header().Set("WWW-Authenticate", `Basic realm="restricted"`)
    w.Header().Set("Content-Length", "12")
    w.Header().Set("Connection", "close")
    w.WriteHeader(401)
    w.Write([]byte("Unauthorized"))
}
```

- Wrong password? `401 Unauthorized`
- Wrong username? `401 Unauthorized`
- User doesn't exist? `401 Unauthorized`
- Rate limited? `401 Unauthorized` (not 429 — don't confirm lockout)

Fingerprinting gets you nothing.

## Configuration

HTTP config in `goclaw.json` (no credentials here):

```json
{
  "http": {
    "enabled": true,
    "listen": "127.0.0.1:1337"
  }
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `enabled` | No | Default: `true` if any user has HTTP credentials, `false` otherwise |
| `listen` | No | Address to bind. Default: `:1337` |

**Validation on startup:**
- HTTP server starts automatically if any user has HTTP credentials
- Set `enabled: false` explicitly to disable HTTP even when credentials exist
- If `users.json` doesn't exist → create empty, warn

### Development Mode

Run with `--dev` flag to enable template hot-reloading:

```bash
goclaw gateway --dev
```

In dev mode, HTML templates are loaded from disk on each request, allowing rapid iteration without restarting the server.

## Endpoints

### MVP Endpoints

```
GET  /           → Web UI dashboard
GET  /chat       → Web UI chat interface
POST /api/send   → Send message to agent
GET  /api/events → SSE stream for real-time updates
GET  /api/status → Agent status (JSON)
```

### GET /

Web UI dashboard. Shows:
- Agent status (model, uptime)
- Session info (for authenticated user)
- Recent activity

### GET /chat

Web UI chat interface. Simple send/receive via SSE.

### POST /api/send

Send a message to the agent.

**Request:**
```json
{
  "message": "What's the weather like?"
}
```

**Response:**
```json
{
  "ok": true,
  "id": "msg_abc123"
}
```

Response content is delivered via SSE stream, not in this response.

Session is determined by authenticated user:
- Owner → `primary` session
- Other users → `user:<user_id>` session

### GET /api/events

Server-Sent Events stream for real-time updates.

```
GET /api/events
Accept: text/event-stream
```

Events:
```
event: message
data: {"role":"assistant","content":"The weather is..."}

event: tool_use
data: {"tool":"weather","input":{"location":"Johannesburg"}}

event: typing
data: {"typing":true}

event: done
data: {"id":"msg_abc123"}
```

### GET /api/status

Agent status as JSON. Session key depends on user role.

**Owner response:**
```json
{
  "ok": true,
  "user": "rodent",
  "uptime": "2h15m",
  "model": "claude-sonnet-4-20250514",
  "session": {
    "key": "primary",
    "messages": 42,
    "tokens": 45000,
    "max_tokens": 200000,
    "usage_percent": 22.5
  }
}
```

**Non-owner response:**
```json
{
  "ok": true,
  "user": "alice",
  "uptime": "2h15m",
  "model": "claude-sonnet-4-20250514",
  "session": {
    "key": "user:alice",
    "messages": 12,
    "tokens": 8500,
    "max_tokens": 200000,
    "usage_percent": 4.25
  }
}
```

## Web UI

### Tech Stack

- **Bootstrap 5.3** — CDN (`cdn.jsdelivr.net`)
- **Bootstrap Icons** — CDN
- **jQuery 3.7** — CDN (`cdn.jsdelivr.net`) for SSE handling and DOM manipulation
- **html/template** — Go standard library

No build step. No npm. No webpack. Single binary with embedded templates.

### File Structure

```
goclaw/
├── internal/
│   └── http/
│       ├── server.go       # HTTP server, auth middleware
│       ├── handlers.go     # Route handlers  
│       ├── api.go          # API endpoints
│       └── sse.go          # Server-Sent Events
├── html/
│   ├── header.html         # {{define "header"}} - navbar, CDN imports
│   ├── footer.html         # {{define "footer"}} - scripts, closing tags
│   ├── index.html          # Dashboard
│   └── chat.html           # Chat interface
└── httpdemo/               # Reference implementation (standalone demo)
```

### Template Pattern

**header.html:**
```html
{{define "header"}}
<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>{{.Title}} - GoClaw</title>
    <link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.3/dist/css/bootstrap.min.css" rel="stylesheet">
    <link href="https://cdn.jsdelivr.net/npm/bootstrap-icons@1.11.3/font/bootstrap-icons.min.css" rel="stylesheet">
</head>
<body>
<nav class="navbar navbar-dark bg-dark">
    <div class="container">
        <a class="navbar-brand" href="/">🐀 GoClaw</a>
        <span class="navbar-text">{{.User.Name}}</span>
    </div>
</nav>
<div class="container mt-4">
{{end}}
```

**footer.html:**
```html
{{define "footer"}}
</div>
<script src="https://cdn.jsdelivr.net/npm/bootstrap@5.3.3/dist/js/bootstrap.bundle.min.js"></script>
</body>
</html>
{{end}}
```

### Template Loading

```go
//go:embed html/*.html
var templateFS embed.FS

func loadTemplates() *template.Template {
    return template.Must(template.ParseFS(templateFS, "html/*.html"))
}
```

Templates embedded at compile time. Single binary, no external dependencies.

### Reference Implementation

See `httpdemo/` for a standalone HTTP server demo showing:
- Template parsing and rendering
- Static file serving
- Path traversal protection

Can be moved to `internal/http/` and integrated with gateway.

## Implementation Notes

### Middleware Chain

```go
handler := authMiddleware(users)(
    stripHeadersMiddleware(
        logRequestMiddleware(
            routes,
        ),
    ),
)
```

### Header Stripping

```go
func stripHeadersMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        wrapped := &headerStripper{ResponseWriter: w}
        next.ServeHTTP(wrapped, r)
    })
}

type headerStripper struct {
    http.ResponseWriter
}

func (h *headerStripper) WriteHeader(code int) {
    h.Header().Del("X-Content-Type-Options")
    h.Header().Del("Date")
    h.ResponseWriter.WriteHeader(code)
}
```

### Graceful Shutdown

```go
func (s *HTTPServer) Shutdown(ctx context.Context) error {
    return s.server.Shutdown(ctx)
}
```

## What HTTP is NOT For

- Config editing → Use CLI or edit files
- User management → Use CLI (`goclaw user`)
- Session browsing → Use TUI
- Tool management → Use CLI
- Cron management → Use CLI/tool
- Anything destructive → Local access only

If HTTP is giving you grief, Telegram's always there. That's the whole point of multi-channel.

## Password Hashing

Argon2id with recommended parameters:

```go
import "golang.org/x/crypto/argon2"

func hashPassword(password string) string {
    salt := make([]byte, 16)
    rand.Read(salt)
    
    hash := argon2.IDKey([]byte(password), salt, 3, 64*1024, 4, 32)
    
    return fmt.Sprintf("$argon2id$v=19$m=65536,t=3,p=4$%s$%s",
        base64.RawStdEncoding.EncodeToString(salt),
        base64.RawStdEncoding.EncodeToString(hash))
}

func verifyPassword(password, encoded string) bool {
    // Parse encoded string, extract params/salt/hash
    // Recompute hash with same params
    // Constant-time compare
}
```

## See Also

- [Architecture](../docs/architecture.md) — System overview
- [httpdemo/](../httpdemo/) — Reference HTTP server implementation
- [CRON.md](./CRON.md) — Cron system (not exposed via HTTP)
