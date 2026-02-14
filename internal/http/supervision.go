// Package http provides the HTTP server for web UI and API.
package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/gateway"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/session"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// SupervisionGateway extends GatewayRunner with supervision capabilities.
// This interface is implemented by the Gateway for supervision features.
type SupervisionGateway interface {
	GatewayRunner

	// Sessions returns info about all active gateway sessions
	Sessions() []session.SessionInfo

	// SessionManager returns the session manager for direct access
	SessionManager() *session.Manager

	// History returns the messages for a specific session
	History(sessionKey string) ([]session.Message, error)

	// Users returns the user registry
	Users() *user.Registry

	// RunAgentForSession triggers an agent run for a specific session.
	// DEPRECATED: Use InjectMessage instead. Will be removed.
	RunAgentForSession(ctx context.Context, sessionKey string, events chan<- gateway.AgentEvent) error

	// InjectMessage injects a message into a user's session and delivers appropriately.
	// If invokeLLM is true: adds as user message with prefix, triggers agent run
	// If invokeLLM is false: adds as assistant message, delivers directly
	// The supervisor parameter identifies who performed the injection (for audit logging).
	InjectMessage(ctx context.Context, sessionKey, message string, invokeLLM bool, supervisor *user.User) error
}

// GatewaySessionInfo contains information about a gateway session for supervision.
type GatewaySessionInfo struct {
	Key          string    `json:"key"`          // Session key (e.g., "primary", "user:alice")
	Messages     int       `json:"messages"`     // Number of messages
	TotalTokens  int       `json:"totalTokens"`  // Current token count
	MaxTokens    int       `json:"maxTokens"`    // Model's context window
	ContextUsage float64   `json:"contextUsage"` // 0.0 to 1.0
	Supervised   bool      `json:"supervised"`   // Is being supervised
	LLMEnabled   bool      `json:"llmEnabled"`   // Agent can respond
	UpdatedAt    time.Time `json:"updatedAt"`    // Last activity
}

// handleSessionsAction handles all /api/sessions/* routes
func (s *Server) handleSessionsAction(w http.ResponseWriter, r *http.Request) {
	u := getUserFromContext(r)
	if u == nil {
		L_error("http: supervision failed - no user in context")
		http.Error(w, "Not authenticated", http.StatusUnauthorized)
		return
	}

	// Owner-only check
	if !u.IsOwner() {
		L_warn("http: supervision denied - not owner", "user", u.ID)
		http.Error(w, "Forbidden - owner only", http.StatusForbidden)
		return
	}

	// Parse the session key from URL path
	// Format: /api/sessions/{key}/action
	path := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 1 || parts[0] == "" {
		http.Error(w, "Invalid session path", http.StatusBadRequest)
		return
	}

	sessionKey := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	L_debug("http: supervision request", "user", u.ID, "session", sessionKey, "action", action, "method", r.Method)

	switch action {
	case "events":
		s.handleSessionEvents(w, r, sessionKey, u)
	case "guidance":
		s.handleSessionGuidance(w, r, sessionKey, u)
	case "llm":
		s.handleSessionLLM(w, r, sessionKey, u)
	case "message":
		s.handleSessionMessage(w, r, sessionKey, u)
	default:
		http.Error(w, "Unknown action", http.StatusBadRequest)
	}
}

// handleSessionEvents handles GET /api/sessions/:key/events - SSE stream for a specific session
func (s *Server) handleSessionEvents(w http.ResponseWriter, r *http.Request, sessionKey string, u *user.User) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	gw, ok := s.channel.gateway.(SupervisionGateway)
	if !ok || gw == nil {
		L_error("http: supervision failed - gateway doesn't support supervision")
		http.Error(w, "Supervision not available", http.StatusInternalServerError)
		return
	}

	// Get the session from session manager
	sess := gw.SessionManager().Get(sessionKey)
	if sess == nil {
		L_warn("http: supervision - session not found", "session", sessionKey)
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// Mark session as supervised
	supervision := sess.EnsureSupervision()
	supervision.SetSupervised(u.ID)
	L_info("http: supervision started", "session", sessionKey, "supervisor", u.ID)

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// Send connected event with session info
	connectData := map[string]interface{}{
		"sessionKey":  sessionKey,
		"messages":    sess.MessageCount(),
		"supervised":  true,
		"llmEnabled":  supervision.IsLLMEnabled(),
		"totalTokens": sess.GetTotalTokens(),
		"maxTokens":   sess.GetMaxTokens(),
	}
	data, _ := json.Marshal(connectData)
	fmt.Fprintf(w, "event: connected\ndata: %s\n\n", data)
	flusher.Flush()

	// Send existing message history
	messages := sess.GetMessages()
	for _, msg := range messages {
		msgData := map[string]interface{}{
			"role":      msg.Role,
			"content":   msg.Content,
			"timestamp": msg.Timestamp.Unix(),
		}
		if msg.ToolName != "" {
			msgData["toolName"] = msg.ToolName
		}
		if msg.ToolUseID != "" {
			msgData["toolId"] = msg.ToolUseID
		}
		if msg.Source != "" {
			msgData["source"] = msg.Source
		}
		if msg.Supervisor != "" {
			msgData["supervisor"] = msg.Supervisor
		}
		if msg.InterventionType != "" {
			msgData["interventionType"] = msg.InterventionType
		}
		data, _ := json.Marshal(msgData)
		fmt.Fprintf(w, "event: history\ndata: %s\n\n", data)
	}
	flusher.Flush()

	// Subscribe to real-time events
	eventChan := supervision.Subscribe()
	defer supervision.Unsubscribe()

	ctx := r.Context()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	defer func() {
		supervision.ClearSupervised()
		L_info("http: supervision ended", "session", sessionKey, "supervisor", u.ID)
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		case ev, ok := <-eventChan:
			if !ok {
				// Channel closed
				return
			}
			// Convert gateway event to SSE and send
			sseEvent := s.supervisionEventToSSE(ev)
			if sseEvent != nil {
				data, _ := json.Marshal(sseEvent.Data)
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", sseEvent.Event, data)
				flusher.Flush()
			}
		}
	}
}

// handleSessionGuidance handles POST /api/sessions/:key/guidance - send guidance to agent
func (s *Server) handleSessionGuidance(w http.ResponseWriter, r *http.Request, sessionKey string, u *user.User) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	gw, ok := s.channel.gateway.(SupervisionGateway)
	if !ok || gw == nil {
		http.Error(w, "Supervision not available", http.StatusInternalServerError)
		return
	}

	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Content == "" {
		http.Error(w, "Content required", http.StatusBadRequest)
		return
	}

	sess := gw.SessionManager().Get(sessionKey)
	if sess == nil {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	supervision := sess.EnsureSupervision()
	L_debug("http: guidance: session found, supervision state ensured", "session", sessionKey, "llmEnabled", supervision.IsLLMEnabled())

	L_info("http: guidance sent", "session", sessionKey, "supervisor", u.ID, "contentLen", len(req.Content))

	// Use InjectMessage to add message to session and trigger agent run through user's channels
	// This ensures proper event streaming to the user
	// Pass supervisor for audit logging
	go func() {
		err := gw.InjectMessage(context.Background(), sessionKey, req.Content, true, u) // invokeLLM=true, supervisor=u
		if err != nil {
			L_error("http: guidance inject failed", "session", sessionKey, "error", err)
		}
	}()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"status":       "delivered",
		"regenerating": true,
	}); err != nil {
		L_warn("http: failed to encode response", "error", err)
	}
}

// handleSessionLLM handles POST /api/sessions/:key/llm - enable/disable LLM responses
func (s *Server) handleSessionLLM(w http.ResponseWriter, r *http.Request, sessionKey string, u *user.User) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	gw, ok := s.channel.gateway.(SupervisionGateway)
	if !ok || gw == nil {
		http.Error(w, "Supervision not available", http.StatusInternalServerError)
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	sess := gw.SessionManager().Get(sessionKey)
	if sess == nil {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	supervision := sess.EnsureSupervision()
	supervision.SetLLMEnabled(req.Enabled)

	action := "enabled"
	if !req.Enabled {
		action = "disabled"
	}
	L_info("http: LLM "+action, "session", sessionKey, "supervisor", u.ID)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "ok",
		"llmEnabled": req.Enabled,
	}); err != nil {
		L_warn("http: failed to encode response", "error", err)
	}
}

// handleSessionMessage handles POST /api/sessions/:key/message - ghostwrite a message
func (s *Server) handleSessionMessage(w http.ResponseWriter, r *http.Request, sessionKey string, u *user.User) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	gw, ok := s.channel.gateway.(SupervisionGateway)
	if !ok || gw == nil {
		http.Error(w, "Supervision not available", http.StatusInternalServerError)
		return
	}

	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Content == "" {
		http.Error(w, "Content required", http.StatusBadRequest)
		return
	}

	sess := gw.SessionManager().Get(sessionKey)
	if sess == nil {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// Use InjectMessage to add message to session and deliver to user's channels
	// invokeLLM=false means this is a ghostwrite (no LLM response)
	// Pass supervisor for audit logging
	if err := gw.InjectMessage(r.Context(), sessionKey, req.Content, false, u); err != nil {
		L_error("http: ghostwrite inject failed", "session", sessionKey, "error", err)
		http.Error(w, "Failed to deliver message", http.StatusInternalServerError)
		return
	}

	L_info("http: ghostwrite sent", "session", sessionKey, "supervisor", u.ID, "contentLen", len(req.Content))

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "sent",
		"messageId": fmt.Sprintf("ghost_%d", time.Now().UnixNano()),
	}); err != nil {
		L_warn("http: failed to encode response", "error", err)
	}
}

// getGatewaySessionsInfo returns info about all gateway sessions for supervision.
// This is called from handleStatus for owner users.
func (s *Server) getGatewaySessionsInfo() []GatewaySessionInfo {
	gw, ok := s.channel.gateway.(SupervisionGateway)
	if !ok || gw == nil {
		return nil
	}

	sessions := gw.Sessions()
	result := make([]GatewaySessionInfo, 0, len(sessions))

	mgr := gw.SessionManager()
	for _, info := range sessions {
		sess := mgr.Get(info.ID)
		if sess == nil {
			continue
		}

		supervised := false
		llmEnabled := true
		if supervision := sess.GetSupervision(); supervision != nil {
			supervised = supervision.IsSupervised()
			llmEnabled = supervision.IsLLMEnabled()
		}

		result = append(result, GatewaySessionInfo{
			Key:          info.ID,
			Messages:     info.MessageCount,
			TotalTokens:  info.TotalTokens,
			MaxTokens:    info.MaxTokens,
			ContextUsage: info.ContextUsage,
			Supervised:   supervised,
			LLMEnabled:   llmEnabled,
			UpdatedAt:    parseTime(info.UpdatedAt),
		})
	}

	return result
}

// parseTime parses an ISO 8601 timestamp, returning zero time on error
func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// supervisionEventToSSE converts a gateway event to an SSE event for supervision.
func (s *Server) supervisionEventToSSE(event interface{}) *SSEEvent {
	switch e := event.(type) {
	case gateway.EventAgentStart:
		return &SSEEvent{Event: "start", Data: e}

	case gateway.EventTextDelta:
		return &SSEEvent{Event: "message", Data: map[string]string{
			"runId":   e.RunID,
			"content": e.Delta,
		}}

	case gateway.EventToolStart:
		inputStr := string(e.Input)
		if len(inputStr) > 2048 {
			inputStr = inputStr[:2048] + "..."
		}
		return &SSEEvent{Event: "tool_start", Data: map[string]interface{}{
			"runId":    e.RunID,
			"toolName": e.ToolName,
			"toolId":   e.ToolID,
			"input":    inputStr,
		}}

	case gateway.EventToolEnd:
		result := e.Result
		if len(result) > 2048 {
			result = result[:2048] + "..."
		}
		return &SSEEvent{Event: "tool_end", Data: map[string]interface{}{
			"runId":    e.RunID,
			"toolName": e.ToolName,
			"toolId":   e.ToolID,
			"result":   result,
			"isError":  e.Error != "",
		}}

	case gateway.EventAgentEnd:
		return &SSEEvent{Event: "done", Data: map[string]string{
			"runId":     e.RunID,
			"finalText": e.FinalText,
		}}

	case gateway.EventAgentError:
		return &SSEEvent{Event: "agent_error", Data: map[string]string{
			"runId": e.RunID,
			"error": e.Error,
		}}

	case gateway.EventThinking:
		return &SSEEvent{Event: "thinking", Data: map[string]string{
			"runId":   e.RunID,
			"content": e.Content,
		}}

	case gateway.EventThinkingDelta:
		return &SSEEvent{Event: "thinking_delta", Data: map[string]string{
			"runId":   e.RunID,
			"content": e.Delta,
		}}

	// User message event from gateway
	case gateway.EventUserMessage:
		return &SSEEvent{Event: "user_message", Data: map[string]interface{}{
			"content":    e.Content,
			"source":     e.Source,
			"supervisor": e.Supervisor,
		}}

	default:
		return nil
	}
}
