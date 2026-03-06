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
}

// RegisterRoutes registers voice channel routes with the HTTP server
// Only registers the WebSocket endpoint - HTML and JS are served by the main http server
func (c *VoiceChannel) RegisterRoutes(srv HTTPServer) {
	mux := srv.Mux()
	users := srv.Users()

	// Voice WebSocket - NO middleware wrapper (WebSocket needs raw ResponseWriter for Hijacker)
	// Auth is handled manually inside the handler
	mux.HandleFunc("/voice/ws", func(w http.ResponseWriter, r *http.Request) {
		u := c.authenticateRequest(r, users)
		if u == nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		c.HandleWebSocket(w, r, u)
	})

	L_info("http_voice: websocket route registered")
}

// authenticateRequest extracts the user from the request (via basic auth header)
func (c *VoiceChannel) authenticateRequest(r *http.Request, users *user.Registry) *user.User {
	username, password, ok := r.BasicAuth()
	if !ok {
		return nil
	}

	u := users.Get(username)
	if u == nil || !u.VerifyHTTPPassword(password) {
		return nil
	}
	return u
}
