package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/commands"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/metrics"
	"github.com/roelfdiedericks/goclaw/internal/session"
)

// handleIndex serves the dashboard page
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	// Only serve root path, not any other path
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// Reload templates in dev mode
	if err := s.reloadTemplatesIfDev(); err != nil {
		L_error("http: template reload error", "error", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}

	u := getUserFromContext(r)
	if u == nil {
		L_error("http: index failed - no user in context")
		http.Error(w, "Not authenticated", http.StatusUnauthorized)
		return
	}

	data := struct {
		Title     string
		User      *UserTemplateData
		Timestamp time.Time
	}{
		Title:     "GoClaw",
		User:      &UserTemplateData{Name: u.Name, Username: u.ID, IsOwner: u.IsOwner()},
		Timestamp: time.Now(),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "index.html", data); err != nil {
		L_error("http: template error", "error", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

// handleChat serves the chat interface
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	// Reload templates in dev mode
	if err := s.reloadTemplatesIfDev(); err != nil {
		L_error("http: template reload error", "error", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}

	u := getUserFromContext(r)
	if u == nil {
		L_error("http: chat failed - no user in context")
		http.Error(w, "Not authenticated", http.StatusUnauthorized)
		return
	}

	// Get agent identity for display
	agentName := "GoClaw"
	typingText := "GoClaw is typing..."
	if s.channel != nil && s.channel.gateway != nil {
		identity := s.channel.gateway.AgentIdentity()
		if identity != nil {
			agentName = identity.DisplayName()
			typingText = identity.TypingText()
		}
	}

	// Check for supervision mode (owner only)
	superviseSession := r.URL.Query().Get("supervise")
	isSupervising := false
	if superviseSession != "" && u.IsOwner() {
		isSupervising = true
		L_debug("http: chat in supervision mode", "session", superviseSession, "user", u.ID)
	} else if superviseSession != "" && !u.IsOwner() {
		// Non-owner trying to supervise - reject
		L_warn("http: supervision denied - not owner", "user", u.ID, "session", superviseSession)
		superviseSession = ""
	}

	data := struct {
		Title            string
		User             *UserTemplateData
		AgentName        string
		TypingText       string
		Timestamp        time.Time
		SuperviseSession string
		IsSupervising    bool
	}{
		Title:            "GoClaw - Chat",
		User:             &UserTemplateData{Name: u.Name, Username: u.ID, IsOwner: u.IsOwner()},
		AgentName:        agentName,
		TypingText:       typingText,
		Timestamp:        time.Now(),
		SuperviseSession: superviseSession,
		IsSupervising:    isSupervising,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "chat.html", data); err != nil {
		L_error("http: template error", "error", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

// UserTemplateData holds user info for templates
type UserTemplateData struct {
	Name     string
	Username string
	IsOwner  bool
}

// handleSend handles POST /api/send - send message to agent
func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		L_warn("http: send - wrong method", "method", r.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	u := getUserFromContext(r)
	if u == nil {
		L_error("http: send failed - no user in context")
		http.Error(w, "Not authenticated", http.StatusUnauthorized)
		return
	}

	// Parse request body
	var req struct {
		Message string `json:"message"`
		Images  []struct {
			Data     string `json:"data"`     // Base64-encoded image data
			MimeType string `json:"mimeType"` // MIME type (e.g., "image/png")
		} `json:"images"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		L_warn("http: send - invalid JSON", "user", u.ID, "error", err)
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Need either message or images
	if req.Message == "" && len(req.Images) == 0 {
		L_warn("http: send - empty message and no images", "user", u.ID)
		http.Error(w, "Message or image required", http.StatusBadRequest)
		return
	}

	sessionID := getSessionFromContext(r)
	if sessionID == "" {
		L_error("http: send failed - no session in context", "user", u.ID)
		http.Error(w, "No session", http.StatusInternalServerError)
		return
	}

	// Convert images to ImageAttachments
	var images []session.ImageAttachment
	for _, img := range req.Images {
		images = append(images, session.ImageAttachment{
			Data:     img.Data,
			MimeType: img.MimeType,
			Source:   "http",
		})
	}

	L_info("http: message received", "user", u.ID, "session", sessionID[:8]+"...", "length", len(req.Message), "images", len(images))

	// Handle /thinking command locally (channel-specific preference)
	if strings.HasPrefix(strings.TrimSpace(req.Message), "/thinking") {
		s.handleThinkingCommand(w, sessionID, req.Message)
		return
	}

	// Handle built-in commands (/status, /compact, /clear, /help, etc.)
	trimmedMsg := strings.TrimSpace(req.Message)
	if strings.HasPrefix(trimmedMsg, "/") {
		cmdMgr := commands.GetManager()
		// Parse command name (first word)
		cmdName := strings.Fields(trimmedMsg)[0]
		if cmd := cmdMgr.Get(cmdName); cmd != nil {
			s.handleBuiltinCommand(w, r.Context(), sessionID, trimmedMsg, cmd)
			return
		}
	}

	// Run agent request (will stream via SSE)
	msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	err := s.channel.RunAgentRequest(r.Context(), sessionID, u, req.Message, images)
	if err != nil {
		L_error("http: failed to run agent", "user", u.ID, "error", err)
		http.Error(w, fmt.Sprintf("Failed to process: %v", err), http.StatusInternalServerError)
		return
	}

	resp := struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Message string `json:"message"`
	}{
		ID:      msgID,
		Status:  "processing",
		Message: "Message sent to agent",
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(resp)
}

// handleThinkingCommand handles the /thinking command for toggling tool visibility
func (s *Server) handleThinkingCommand(w http.ResponseWriter, sessionID string, message string) {
	sess := s.channel.GetSession(sessionID)
	if sess == nil {
		http.Error(w, "Session not found", http.StatusInternalServerError)
		return
	}

	// Parse subcommand
	parts := strings.Fields(message)
	arg := ""
	if len(parts) > 1 {
		arg = strings.ToLower(parts[1])
	}

	var resultMsg string
	switch arg {
	case "on":
		sess.ShowThinking = true
		resultMsg = "Thinking output enabled. You'll now see tool calls and working output."
	case "off":
		sess.ShowThinking = false
		resultMsg = "Thinking output disabled. You'll only see final responses."
	case "toggle", "":
		sess.ShowThinking = !sess.ShowThinking
		if sess.ShowThinking {
			resultMsg = "Thinking output enabled."
		} else {
			resultMsg = "Thinking output disabled."
		}
	case "status":
		if sess.ShowThinking {
			resultMsg = "Thinking output is currently ON."
		} else {
			resultMsg = "Thinking output is currently OFF."
		}
	default:
		resultMsg = "Usage: /thinking [on|off|toggle|status]"
	}

	// Send preference event to client
	sess.SendEvent(SSEEvent{
		Event: "preference",
		Data: map[string]interface{}{
			"key":   "thinking",
			"value": sess.ShowThinking,
		},
	})

	// Send response as system message
	sess.SendEvent(SSEEvent{
		Event: "system",
		Data: map[string]string{
			"message": resultMsg,
		},
	})

	// Respond to HTTP request
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"message": resultMsg,
	})
}

// handleBuiltinCommand handles built-in slash commands (/status, /compact, /clear, etc.)
func (s *Server) handleBuiltinCommand(w http.ResponseWriter, ctx context.Context, sessionID string, message string, cmd *commands.Command) {
	sess := s.channel.GetSession(sessionID)
	if sess == nil {
		http.Error(w, "Session not found", http.StatusInternalServerError)
		return
	}

	L_info("http: handling command", "command", cmd.Name, "session", sessionID[:8]+"...")

	// For long-running commands like /compact, send a "working" message first
	if cmd.Name == "/compact" {
		sess.SendEvent(SSEEvent{
			Event: "system",
			Data: map[string]string{
				"message": "Compacting session... (this may take a minute)",
			},
		})
	}

	// Execute command via manager (which has the provider wired up)
	mgr := commands.GetManager()
	result := mgr.Execute(ctx, message, sessionID)

	// Determine message to show
	responseText := result.Text
	if responseText == "" {
		responseText = "Command executed."
	}

	// Send as system message via SSE
	sess.SendEvent(SSEEvent{
		Event: "system",
		Data: map[string]string{
			"message": responseText,
		},
	})

	// Respond to HTTP request
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	resp := map[string]interface{}{
		"status":  "ok",
		"command": cmd.Name,
		"message": responseText,
	}
	if result.Error != nil {
		resp["status"] = "error"
		resp["error"] = result.Error.Error()
	}
	json.NewEncoder(w).Encode(resp)
}

// handleEvents handles GET /api/events - SSE stream
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	u := getUserFromContext(r)
	if u == nil {
		L_error("http: SSE failed - no user in context")
		http.Error(w, "Not authenticated", http.StatusUnauthorized)
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	flusher, ok := w.(http.Flusher)
	if !ok {
		L_error("http: SSE failed - flusher not supported", "user", u.ID)
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	if s.channel == nil {
		L_error("http: SSE failed - channel not initialized", "user", u.ID)
		http.Error(w, "Server not ready", http.StatusInternalServerError)
		return
	}

	sessionID := getSessionFromContext(r)
	if sessionID == "" {
		L_error("http: SSE failed - no session in context", "user", u.ID)
		http.Error(w, "No session", http.StatusInternalServerError)
		return
	}

	// Parse Last-Event-ID for replay (SSE standard reconnection mechanism)
	lastEventID := 0
	if lastIDStr := r.Header.Get("Last-Event-ID"); lastIDStr != "" {
		if parsed, err := strconv.Atoi(lastIDStr); err == nil {
			lastEventID = parsed
			L_trace("http: SSE reconnect", "user", u.ID, "session", sessionID[:8]+"...", "lastEventID", lastEventID)
		}
	} else {
		L_info("http: SSE connection opened", "user", u.ID, "session", sessionID[:8]+"...")
	}

	// Register connection and get events to replay
	sess, conn, replay := s.channel.RegisterConnection(sessionID, u, lastEventID)
	if conn == nil {
		L_error("http: SSE failed - connection registration returned nil", "user", u.ID)
		http.Error(w, "Failed to register connection", http.StatusInternalServerError)
		return
	}
	defer s.channel.UnregisterConnection(sessionID, conn)

	// Send initial connected event (with current event ID for client tracking)
	sess.bufferMu.Lock()
	currentEventID := sess.nextEventID - 1
	sess.bufferMu.Unlock()
	fmt.Fprintf(w, "event: connected\nid: %d\ndata: {\"user\":\"%s\",\"lastEventId\":%d}\n\n", currentEventID, u.ID, currentEventID)
	flusher.Flush()

	// Send current preferences
	prefData, _ := json.Marshal(map[string]interface{}{
		"key":   "thinking",
		"value": sess.ShowThinking,
	})
	fmt.Fprintf(w, "event: preference\ndata: %s\n\n", prefData)
	flusher.Flush()

	// Replay missed events
	for _, buffered := range replay {
		data, err := json.Marshal(buffered.Event.Data)
		if err != nil {
			L_error("http: failed to marshal replay event", "error", err)
			continue
		}
		fmt.Fprintf(w, "event: %s\nid: %d\ndata: %s\n\n", buffered.Event.Event, buffered.ID, data)
		flusher.Flush()
	}
	if len(replay) > 0 {
		L_info("http: replayed events", "count", len(replay), "session", sessionID[:8]+"...")
	}

	// Keep connection open and forward events
	ctx := r.Context()
	ticker := time.NewTicker(15 * time.Second) // Heartbeat every 15s (more frequent for stability)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			L_trace("http: SSE connection closed", "user", u.ID, "session", sessionID[:8]+"...")
			return
		case <-conn.Done:
			L_info("http: SSE connection replaced", "user", u.ID, "session", sessionID[:8]+"...")
			return
		case event := <-conn.Events:
			// Send event to client with ID
			data, err := json.Marshal(event.Data)
			if err != nil {
				L_error("http: failed to marshal event", "error", err)
				continue
			}
			// Get current event ID from session
			sess.bufferMu.Lock()
			eventID := sess.nextEventID - 1 // Last assigned ID
			sess.bufferMu.Unlock()
			fmt.Fprintf(w, "event: %s\nid: %d\ndata: %s\n\n", event.Event, eventID, data)
			flusher.Flush()
		case <-ticker.C:
			// Send heartbeat comment (doesn't need ID)
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

// handleStatus handles GET /api/status - agent status
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		L_warn("http: status - wrong method", "method", r.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	u := getUserFromContext(r)
	if u == nil {
		L_error("http: status failed - no user in context")
		http.Error(w, "Not authenticated", http.StatusUnauthorized)
		return
	}

	// Get active HTTP sessions info (browser connections)
	var sessions []SessionInfo
	if s.channel != nil {
		sessions = s.channel.GetSessionsInfo()
	}

	// For owner, also include gateway sessions (for supervision)
	var gatewaySessions []GatewaySessionInfo
	if u.IsOwner() && s.channel != nil && s.channel.gateway != nil {
		gatewaySessions = s.getGatewaySessionsInfo()
	}

	status := struct {
		Status          string               `json:"status"`
		User            string               `json:"user"`
		IsOwner         bool                 `json:"isOwner"`
		Sessions        []SessionInfo        `json:"sessions"`
		GatewaySessions []GatewaySessionInfo `json:"gatewaySessions,omitempty"`
	}{
		Status:          "ready",
		User:            u.ID,
		IsOwner:         u.IsOwner(),
		Sessions:        sessions,
		GatewaySessions: gatewaySessions,
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(status)
}

// handleMedia serves media files from media root or allowed paths
func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	u := getUserFromContext(r)
	if u == nil {
		L_error("http: media failed - no user in context")
		http.Error(w, "Not authenticated", http.StatusUnauthorized)
		return
	}

	// Get file path from query param
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		http.Error(w, "Missing path parameter", http.StatusBadRequest)
		return
	}

	// Use media.ResolveMediaPath for path resolution and security
	var absPath string
	var err error

	if s.mediaRoot != "" {
		// Try to resolve via media root first
		absPath, err = media.ResolveMediaPath(s.mediaRoot, filePath)
	} else {
		// Fallback: only allow absolute paths in allowed directories
		if !filepath.IsAbs(filePath) {
			http.Error(w, "Invalid path (no media root configured)", http.StatusBadRequest)
			return
		}
		absPath = filepath.Clean(filePath)
		// Security: only allow certain directories
		allowed := false
		for _, prefix := range []string{"/tmp/", "/home/", "/var/tmp/"} {
			if strings.HasPrefix(absPath, prefix) {
				allowed = true
				break
			}
		}
		if !allowed {
			err = fmt.Errorf("path outside allowed directories")
		}
	}

	if err != nil {
		L_warn("http: media access denied", "path", filePath, "user", u.ID, "error", err)
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	// Check file exists
	info, err := os.Stat(absPath)
	if os.IsNotExist(err) {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	if err != nil {
		L_error("http: media stat error", "path", absPath, "error", err)
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	if info.IsDir() {
		http.Error(w, "Cannot serve directory", http.StatusBadRequest)
		return
	}

	L_debug("http: serving media", "path", absPath, "user", u.ID, "size", info.Size())

	// Serve the file with proper content type detection
	http.ServeFile(w, r, absPath)
}

// handleMetricsAPI handles GET /api/metrics - JSON metrics snapshot
func (s *Server) handleMetricsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	u := getUserFromContext(r)
	if u == nil {
		http.Error(w, "Not authenticated", http.StatusUnauthorized)
		return
	}

	snapshot := metrics.GetInstance().GetSnapshot()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(snapshot)
}

// handleMetrics handles GET /metrics - metrics dashboard page
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	// Reload templates in dev mode
	if err := s.reloadTemplatesIfDev(); err != nil {
		L_error("http: template reload error", "error", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}

	u := getUserFromContext(r)
	if u == nil {
		http.Error(w, "Not authenticated", http.StatusUnauthorized)
		return
	}

	data := struct {
		Title     string
		User      *UserTemplateData
		Timestamp time.Time
	}{
		Title:     "GoClaw - Metrics",
		User:      &UserTemplateData{Name: u.Name, Username: u.ID, IsOwner: u.IsOwner()},
		Timestamp: time.Now(),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "metrics.html", data); err != nil {
		L_error("http: template error", "error", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}
