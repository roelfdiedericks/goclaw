# HTTP API Specification

## Overview

GoClaw exposes a minimal HTTP interface for web UI and API access. Security is paramount — no defaults, no leaky headers, no trust assumptions.

**Philosophy:** HTTP is for *using* the agent, not *administering* it. Admin happens via CLI/TUI where you already have filesystem access.

## Security

### Authentication

**Basic Auth. Required. Always.**

```
Authorization: Basic base64(username:password)
```

- No username configured? HTTP disabled.
- No password configured? HTTP disabled.
- No defaults. Ever.
- No localhost bypass. No special cases. No "trusted proxies".

```go
type HTTPConfig struct {
    Enabled  bool   `json:"enabled"`
    Listen   string `json:"listen"`   // e.g., "127.0.0.1:1337" or ":1337"
    Username string `json:"username"` // Required if enabled
    Password string `json:"password"` // Required if enabled
}
```

### Rate Limiting

**10 second delay per source IP after failed authentication.**

```go
var failedIPs sync.Map // map[string]time.Time

func authMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ip := getSourceIP(r)
        
        // Check if IP is in timeout
        if lastFail, ok := failedIPs.Load(ip); ok {
            if time.Since(lastFail.(time.Time)) < 10*time.Second {
                writeUnauthorized(w)
                return
            }
        }
        
        user, pass, ok := r.BasicAuth()
        if !ok || user != cfg.Username || pass != cfg.Password {
            failedIPs.Store(ip, time.Now())
            writeUnauthorized(w)
            return
        }
        
        // Success - clear any previous failure
        failedIPs.Delete(ip)
        next.ServeHTTP(w, r)
    })
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

```json
{
  "http": {
    "enabled": true,
    "listen": "127.0.0.1:1337",
    "username": "admin",
    "password": "your-secure-password-here"
  }
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `enabled` | No | Default: false |
| `listen` | Yes | Address to bind (e.g., `127.0.0.1:1337`, `:1337`) |
| `username` | Yes | Basic Auth username |
| `password` | Yes | Basic Auth password |

**Validation on startup:**
- If `enabled: true` but no username → error, refuse to start
- If `enabled: true` but no password → error, refuse to start

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
- Session info
- Recent activity

### GET /chat

Web UI chat interface. Simple send/receive.

### POST /api/send

Send a message to the agent.

**Request:**
```json
{
  "message": "What's the weather like?",
  "session": "main"
}
```

**Response:**
```json
{
  "ok": true,
  "id": "msg_abc123"
}
```

Response is delivered via SSE stream, not in this response.

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

event: status
data: {"typing":true}
```

### GET /api/status

Agent status as JSON.

```json
{
  "ok": true,
  "uptime": "2h15m",
  "model": "claude-opus-4-5",
  "session": "agent:main:main",
  "context": {
    "used": 45000,
    "max": 200000,
    "percent": 22.5
  }
}
```

## Web UI

### Tech Stack

- **Bootstrap 5.3** — CDN (`cdn.jsdelivr.net`)
- **Bootstrap Icons** — CDN
- **jQuery 3.7** — CDN
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
    </div>
</nav>
<div class="container mt-4">
{{end}}
```

**footer.html:**
```html
{{define "footer"}}
</div>
<script src="https://cdn.jsdelivr.net/npm/jquery@3.7.1/dist/jquery.min.js"></script>
<script src="https://cdn.jsdelivr.net/npm/bootstrap@5.3.3/dist/js/bootstrap.bundle.min.js"></script>
</body>
</html>
{{end}}
```

**index.html:**
```html
{{template "header" .}}

<h1>Dashboard</h1>
<div class="card">
    <div class="card-body">
        <p><strong>Status:</strong> {{.Status}}</p>
        <p><strong>Model:</strong> {{.Model}}</p>
        <p><strong>Uptime:</strong> {{.Uptime}}</p>
    </div>
</div>

{{template "footer" .}}
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
- Basic Auth implementation
- Template parsing and rendering
- Static file serving
- Path traversal protection

Can be moved to `internal/http/` and integrated with gateway.

## Implementation Notes

### Middleware Chain

```go
handler := authMiddleware(
    stripHeadersMiddleware(
        routes,
    ),
)
```

### Header Stripping

```go
func stripHeadersMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Wrap ResponseWriter to intercept headers
        wrapped := &headerStripper{ResponseWriter: w}
        next.ServeHTTP(wrapped, r)
    })
}

type headerStripper struct {
    http.ResponseWriter
}

func (h *headerStripper) WriteHeader(code int) {
    // Remove any auto-added headers
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
- Session browsing → Use TUI
- Tool management → Use CLI
- Cron management → Use CLI/tool
- Anything destructive → Local access only

If HTTP is giving you grief, Telegram's always there. That's the whole point of multi-channel.

## See Also

- [Architecture](../docs/architecture.md) — System overview
- [httpdemo/](../httpdemo/) — Reference HTTP server implementation
- [CRON.md](./CRON.md) — Cron system (not exposed via HTTP)
