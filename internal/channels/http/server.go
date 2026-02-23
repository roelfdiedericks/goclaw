// Package http provides the HTTP server for web UI and API.
package http

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/channels/types"
	"github.com/roelfdiedericks/goclaw/internal/gateway"
	"github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

//go:embed html/*.html
var htmlFS embed.FS

// Server represents the HTTP server
type Server struct {
	server       *http.Server
	users        *user.Registry
	templates    *template.Template
	rateLimiter  *RateLimiter
	shutdownChan chan struct{}
	wg           sync.WaitGroup

	// Channel for gateway integration
	channel *HTTPChannel

	// Media root for serving inline media
	mediaRoot string

	// Dev mode: reload templates from disk on each request
	devMode      bool
	templatesDir string

	// Config for reload
	config *Config
	listen string

	// State tracking for ManagedChannel interface
	mu        sync.RWMutex
	running   bool
	startedAt time.Time
	lastError error
}

// ServerConfig holds HTTP server configuration
type ServerConfig struct {
	Listen    string // Address to listen on (e.g., ":1337", "127.0.0.1:1337")
	DevMode   bool   // Reload templates from disk on each request
	MediaRoot string // Base directory for media files
}

// NewServer creates a new HTTP server instance
func NewServer(cfg *ServerConfig, users *user.Registry) (*Server, error) {
	logging.L_debug("http: NewServer starting", "listen", cfg.Listen, "devMode", cfg.DevMode, "mediaRoot", cfg.MediaRoot)

	// Validate that at least one user has HTTP credentials
	hasHTTPUsers := false
	userList := users.List()
	logging.L_debug("http: checking users for HTTP auth", "userCount", len(userList))
	for _, u := range userList {
		hasAuth := u.HasHTTPAuth()
		logging.L_debug("http: user auth check", "userID", u.ID, "hasHTTPAuth", hasAuth)
		if hasAuth {
			hasHTTPUsers = true
			break
		}
	}
	if !hasHTTPUsers {
		logging.L_error("http: no users with HTTP credentials found")
		return nil, fmt.Errorf("HTTP server requires at least one user with HTTP credentials (use 'goclaw user set-http')")
	}
	logging.L_debug("http: user validation passed")

	listen := cfg.Listen
	if listen == "" {
		listen = ":1337"
	}

	s := &Server{
		users:        users,
		rateLimiter:  NewRateLimiter(10 * time.Second),
		shutdownChan: make(chan struct{}),
		devMode:      cfg.DevMode,
		mediaRoot:    cfg.MediaRoot,
		listen:       listen,
	}

	// Create HTTP channel
	s.channel = NewHTTPChannel(s)

	// In dev mode, find the templates directory from source location
	if s.devMode {
		_, file, _, ok := runtime.Caller(0)
		if !ok {
			return nil, fmt.Errorf("dev mode: failed to determine source directory")
		}
		s.templatesDir = filepath.Join(filepath.Dir(file), "html")
		if _, err := os.Stat(s.templatesDir); err != nil {
			return nil, fmt.Errorf("dev mode: templates directory not found: %s", s.templatesDir)
		}
		logging.L_info("http: dev mode enabled, loading templates from disk", "dir", s.templatesDir)
	}

	// Load templates (embedded or from disk)
	logging.L_debug("http: loading templates", "devMode", s.devMode, "templatesDir", s.templatesDir)
	if err := s.loadTemplates(); err != nil {
		logging.L_error("http: template loading failed", "error", err, "devMode", s.devMode, "templatesDir", s.templatesDir)
		return nil, fmt.Errorf("failed to load templates: %w", err)
	}
	logging.L_debug("http: templates loaded successfully")

	// Create HTTP server
	s.server = &http.Server{
		Addr:         listen,
		Handler:      s.setupRoutes(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second, // Longer for SSE
		IdleTimeout:  120 * time.Second,
	}

	return s, nil
}

// Channel returns the HTTP channel for gateway registration
func (s *Server) Channel() *HTTPChannel {
	return s.channel
}

// SetGateway sets the gateway for agent interaction
func (s *Server) SetGateway(gw GatewayRunner) {
	s.channel.SetGateway(gw)
}

// setupRoutes configures all HTTP routes
func (s *Server) setupRoutes() http.Handler {
	mux := http.NewServeMux()

	// Apply middleware chain: logging -> strip headers -> rate limit -> auth
	wrap := func(h http.HandlerFunc) http.HandlerFunc {
		return s.logRequest(s.stripHeaders(s.rateLimit(s.basicAuth(h))))
	}

	// API routes
	mux.HandleFunc("/api/send", wrap(s.handleSend))
	mux.HandleFunc("/api/events", wrap(s.handleEvents))
	mux.HandleFunc("/api/status", wrap(s.handleStatus))
	mux.HandleFunc("/api/media", wrap(s.handleMedia))
	mux.HandleFunc("/api/metrics", wrap(s.handleMetricsAPI))

	// Supervision routes (owner-only, checked in handler)
	mux.HandleFunc("/api/sessions/", wrap(s.handleSessionsAction))

	// Web UI routes
	mux.HandleFunc("/", wrap(s.handleIndex))
	mux.HandleFunc("/chat", wrap(s.handleChat))
	mux.HandleFunc("/metrics", wrap(s.handleMetrics))

	return mux
}

// loadTemplates loads HTML templates (from disk in dev mode, embedded otherwise)
func (s *Server) loadTemplates() error {
	if s.devMode && s.templatesDir != "" {
		// Dev mode: load from disk
		pattern := filepath.Join(s.templatesDir, "*.html")
		tmpl, err := template.ParseGlob(pattern)
		if err != nil {
			return fmt.Errorf("failed to parse templates from disk: %w", err)
		}
		s.templates = tmpl
		logging.L_trace("http: loaded templates from disk", "dir", s.templatesDir)
		return nil
	}

	// Production: load from embedded FS
	htmlDir, err := fs.Sub(htmlFS, "html")
	if err != nil {
		return fmt.Errorf("failed to get html subdirectory: %w", err)
	}

	tmpl, err := template.ParseFS(htmlDir, "*.html")
	if err != nil {
		return fmt.Errorf("failed to parse templates: %w", err)
	}

	s.templates = tmpl
	logging.L_debug("http: loaded embedded templates")
	return nil
}

// reloadTemplatesIfDev reloads templates from disk if in dev mode
// Call this before rendering to get live template changes
func (s *Server) reloadTemplatesIfDev() error {
	if !s.devMode {
		return nil
	}
	return s.loadTemplates()
}

// Start starts the HTTP server (implements ManagedChannel)
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil // Already running
	}

	// Track current listen address for config test
	SetCurrentListenAddr(s.server.Addr)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		logging.L_info("http: server starting", "addr", s.server.Addr)

		err := s.server.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			logging.L_error("http: server error", "error", err)
			s.mu.Lock()
			s.lastError = err
			s.running = false
			s.mu.Unlock()
		}
	}()

	s.running = true
	s.startedAt = time.Now()
	s.lastError = nil
	return nil
}

// Stop gracefully shuts down the HTTP server (implements ManagedChannel)
func (s *Server) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil // Already stopped
	}
	s.mu.Unlock()

	close(s.shutdownChan)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.server.Shutdown(ctx); err != nil {
		logging.L_error("http: shutdown error", "error", err)
		return err
	}

	s.wg.Wait()
	logging.L_info("http: server stopped")

	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
	return nil
}

// Reload applies new configuration (implements ManagedChannel)
// Note: HTTP server reload is complex - for now we just update config and log
func (s *Server) Reload(cfg any) error {
	newCfg, ok := cfg.(*Config)
	if !ok {
		return fmt.Errorf("expected *http.Config, got %T", cfg)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// If listen address changed and we're running, we'd need to restart
	// For now, just note this limitation
	if s.running && newCfg.Listen != s.listen && newCfg.Listen != "" {
		logging.L_warn("http: listen address change requires restart", "old", s.listen, "new", newCfg.Listen)
	}

	s.config = newCfg
	return nil
}

// Status returns current channel status (implements ManagedChannel)
func (s *Server) Status() types.ChannelStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return types.ChannelStatus{
		Running:   s.running,
		Connected: s.running, // HTTP is "connected" if listening
		Error:     s.lastError,
		StartedAt: s.startedAt,
		Info:      s.listen,
	}
}

// Name returns the channel name (implements gateway.Channel)
func (s *Server) Name() string {
	return s.channel.Name()
}

// Send sends a message (implements gateway.Channel)
func (s *Server) Send(ctx context.Context, msg string) error {
	return s.channel.Send(ctx, msg)
}

// SendMirror sends a mirror message (implements gateway.Channel)
func (s *Server) SendMirror(ctx context.Context, source, userMsg, response string) error {
	return s.channel.SendMirror(ctx, source, userMsg, response)
}

// HasUser checks if a user has this channel (implements gateway.Channel)
func (s *Server) HasUser(u *user.User) bool {
	return s.channel.HasUser(u)
}

// StreamEvent streams an event to a user (implements gateway.Channel)
func (s *Server) StreamEvent(u *user.User, event gateway.AgentEvent) bool {
	return s.channel.StreamEvent(u, event)
}

// DeliverGhostwrite delivers a ghostwrite message (implements gateway.Channel)
func (s *Server) DeliverGhostwrite(ctx context.Context, u *user.User, message string) error {
	return s.channel.DeliverGhostwrite(ctx, u, message)
}

// logRequest wraps an HTTP handler to log requests
func (s *Server) logRequest(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		handler(lw, r)

		logging.L_trace("http: request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", lw.statusCode,
			"duration", time.Since(start))
	}
}

// loggingResponseWriter wraps ResponseWriter to capture status code
type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (lw *loggingResponseWriter) WriteHeader(code int) {
	lw.statusCode = code
	lw.ResponseWriter.WriteHeader(code)
}

// Flush implements http.Flusher for SSE support
func (lw *loggingResponseWriter) Flush() {
	if f, ok := lw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// stripHeaders removes fingerprinting headers
func (s *Server) stripHeaders(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Remove default headers that reveal server info
		w.Header().Del("Server")
		w.Header().Del("X-Powered-By")

		handler(w, r)
	}
}

// RegisterOperationalCommands registers runtime commands for this server instance.
// Call this after creating the server to enable status command.
func (s *Server) RegisterOperationalCommands() {
	bus.RegisterCommand("http", "status", s.handleStatusCommand)
}

// handleStatusCommand returns the current server status
func (s *Server) handleStatusCommand(cmd bus.Command) bus.CommandResult {
	addr := s.server.Addr
	if addr == "" {
		addr = ":1337"
	}

	return bus.CommandResult{
		Success: true,
		Message: fmt.Sprintf("HTTP server listening on %s", addr),
		Data: map[string]any{
			"listening": true,
			"address":   addr,
		},
	}
}
