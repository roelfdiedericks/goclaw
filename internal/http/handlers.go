package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
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
			L_info("http: SSE reconnect", "user", u.ID, "session", sessionID[:8]+"...", "lastEventID", lastEventID)
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
			L_info("http: SSE connection closed", "user", u.ID, "session", sessionID[:8]+"...")
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

	// Get active sessions info
	var sessions []SessionInfo
	if s.channel != nil {
		sessions = s.channel.GetSessionsInfo()
	}

	status := struct {
		Status   string        `json:"status"`
		User     string        `json:"user"`
		IsOwner  bool          `json:"isOwner"`
		Sessions []SessionInfo `json:"sessions"`
	}{
		Status:   "ready",
		User:     u.ID,
		IsOwner:  u.IsOwner(),
		Sessions: sessions,
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
