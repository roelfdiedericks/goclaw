package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
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

	data := struct {
		Title     string
		User      *UserTemplateData
		Timestamp time.Time
	}{
		Title:     "GoClaw - Chat",
		User:      &UserTemplateData{Name: u.Name, Username: u.ID, IsOwner: u.IsOwner()},
		Timestamp: time.Now(),
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
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		L_warn("http: send - invalid JSON", "user", u.ID, "error", err)
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Message == "" {
		L_warn("http: send - empty message", "user", u.ID)
		http.Error(w, "Message required", http.StatusBadRequest)
		return
	}

	sessionID := getSessionFromContext(r)
	if sessionID == "" {
		L_error("http: send failed - no session in context", "user", u.ID)
		http.Error(w, "No session", http.StatusInternalServerError)
		return
	}

	L_info("http: message received", "user", u.ID, "session", sessionID[:8]+"...", "length", len(req.Message))

	// Run agent request (will stream via SSE)
	msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	err := s.channel.RunAgentRequest(r.Context(), sessionID, u, req.Message)
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

	L_info("http: SSE connection opened", "user", u.ID, "session", sessionID[:8]+"...")

	// Register SSE client
	client := s.channel.RegisterClient(sessionID, u)
	if client == nil {
		L_error("http: SSE failed - client registration returned nil", "user", u.ID)
		http.Error(w, "Failed to register client", http.StatusInternalServerError)
		return
	}
	defer s.channel.UnregisterClient(sessionID, client)

	// Send initial connected event
	fmt.Fprintf(w, "event: connected\ndata: {\"user\":\"%s\"}\n\n", u.ID)
	flusher.Flush()

	// Keep connection open and forward events
	ctx := r.Context()
	ticker := time.NewTicker(30 * time.Second) // Heartbeat every 30s
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			L_info("http: SSE connection closed", "user", u.ID)
			return
		case <-client.Done:
			L_info("http: SSE client disconnected", "user", u.ID)
			return
		case event := <-client.Events:
			// Send event to client
			data, err := json.Marshal(event.Data)
			if err != nil {
				L_error("http: failed to marshal event", "error", err)
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Event, data)
			flusher.Flush()
		case <-ticker.C:
			// Send heartbeat
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

	// TODO: Get actual status from gateway
	status := struct {
		Status  string `json:"status"`
		User    string `json:"user"`
		IsOwner bool   `json:"isOwner"`
	}{
		Status:  "ready",
		User:    u.ID,
		IsOwner: u.IsOwner(),
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(status)
}

// handleMedia serves media files from allowed paths
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

	// Security: only allow absolute paths and validate they exist
	if !filepath.IsAbs(filePath) {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	// Clean the path to prevent traversal
	cleanPath := filepath.Clean(filePath)

	// Additional security: only allow certain directories
	// Allow: workspace media, tmp, common screenshot locations
	allowed := false
	allowedPrefixes := []string{
		"/tmp/",
		"/home/",
		"/var/tmp/",
	}
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(cleanPath, prefix) {
			allowed = true
			break
		}
	}
	if !allowed {
		L_warn("http: media access denied", "path", cleanPath, "user", u.ID)
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	// Check file exists
	info, err := os.Stat(cleanPath)
	if os.IsNotExist(err) {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	if err != nil {
		L_error("http: media stat error", "path", cleanPath, "error", err)
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	if info.IsDir() {
		http.Error(w, "Cannot serve directory", http.StatusBadRequest)
		return
	}

	L_debug("http: serving media", "path", cleanPath, "user", u.ID, "size", info.Size())

	// Serve the file with proper content type detection
	http.ServeFile(w, r, cleanPath)
}
