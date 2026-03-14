package http_voice

import (
	"net/http"

	"github.com/gorilla/websocket"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024 * 16, // 16KB for audio chunks
	WriteBufferSize: 1024 * 16,
	CheckOrigin: func(r *http.Request) bool {
		return true // TODO: Configure allowed origins
	},
}

// HandleWebSocket handles the WebSocket upgrade for voice connections
func (c *VoiceChannel) HandleWebSocket(w http.ResponseWriter, r *http.Request, u *user.User) {
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		L_error("http_voice: websocket upgrade failed", "error", err)
		return
	}

	L_info("http_voice: websocket connected", "user", u.ID, "remote", r.RemoteAddr)

	// Create voice session (takes ownership of conn)
	_, err = c.CreateSession(u, conn)
	if err != nil {
		L_error("http_voice: failed to create session", "error", err, "user", u.ID)
		conn.Close()
		return
	}

	// Session handles the connection from here
}

// HTTPServer is the minimal interface we need from the HTTP server
type HTTPServer interface {
	Mux() *http.ServeMux
	WrapHandler(http.HandlerFunc) http.HandlerFunc
	Users() *user.Registry
	GetUserBySession(r *http.Request) *user.User
}

// RegisterRoutes registers voice channel routes with the HTTP server
// Only registers the WebSocket endpoint - HTML and JS are served by the main http server
func (c *VoiceChannel) RegisterRoutes(srv HTTPServer) {
	mux := srv.Mux()

	// Voice WebSocket - NO middleware wrapper (WebSocket needs raw ResponseWriter for Hijacker)
	// Auth is handled manually inside the handler
	mux.HandleFunc("/voice/ws", func(w http.ResponseWriter, r *http.Request) {
		u := authenticateWebSocket(r, srv)
		if u == nil {
			L_warn("http_voice: websocket auth failed", "remote", r.RemoteAddr)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		c.HandleWebSocket(w, r, u)
	})

	L_info("http_voice: websocket route registered")
}

// authenticateWebSocket authenticates WebSocket requests.
// Tries Basic Auth first (works in Chrome), then falls back to session cookie
// (needed for webkit2gtk/webview which doesn't forward Basic Auth to WebSocket).
func authenticateWebSocket(r *http.Request, srv HTTPServer) *user.User {
	users := srv.Users()

	// Try Basic Auth first (Chrome forwards this to WebSocket)
	if username, password, ok := r.BasicAuth(); ok {
		L_debug("http_voice: trying basic auth", "username", username)
		if u := users.Get(username); u != nil && u.VerifyHTTPPassword(password) {
			L_debug("http_voice: auth via basic auth", "user", u.ID)
			return u
		}
	} else {
		L_debug("http_voice: no basic auth header")
	}

	// Fallback: try session cookie (webkit2gtk/webview)
	L_debug("http_voice: trying session cookie auth")
	if u := srv.GetUserBySession(r); u != nil {
		L_debug("http_voice: auth via session cookie", "user", u.ID)
		return u
	}
	L_debug("http_voice: session cookie auth failed")

	return nil
}
