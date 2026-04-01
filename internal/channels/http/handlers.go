package http

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/acp"
	"github.com/roelfdiedericks/goclaw/internal/commands"
	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/delegatedrun"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	"github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/metrics"
	"github.com/roelfdiedericks/goclaw/internal/session"
	"github.com/roelfdiedericks/goclaw/internal/types"
	"github.com/roelfdiedericks/goclaw/internal/user"
	"github.com/roelfdiedericks/goclaw/internal/voicellm"
)

type runnerHistoryProvider interface {
	History(sessionID string) ([]session.Message, error)
}

func configureSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
}

func configureSSEWriteDeadline(w http.ResponseWriter, endpoint string) *http.ResponseController {
	rc := http.NewResponseController(w)
	// SSE streams are long-lived; disable per-response write deadline so slow clients/tab
	// throttling don't get the stream force-closed mid-chunk.
	if err := rc.SetWriteDeadline(time.Time{}); err != nil && err != http.ErrNotSupported {
		logging.L_trace("http: sse set write deadline unsupported", "endpoint", endpoint, "error", err)
	}
	return rc
}

func sseWritef(w http.ResponseWriter, rc *http.ResponseController, endpoint, format string, args ...any) bool {
	if _, err := fmt.Fprintf(w, format, args...); err != nil {
		logging.L_warn("http: sse write failed", "endpoint", endpoint, "error", err)
		return false
	}
	if err := rc.Flush(); err != nil && err != http.ErrNotSupported {
		logging.L_warn("http: sse flush failed", "endpoint", endpoint, "error", err)
		return false
	}
	return true
}

// handleIndex serves the dashboard page
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	// Only serve root path, not any other path
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// Reload templates in dev mode
	if err := s.reloadTemplatesIfDev(); err != nil {
		logging.L_error("http: template reload error", "error", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}

	u := getUserFromContext(r)
	if u == nil {
		logging.L_error("http: index failed - no user in context")
		http.Error(w, "Not authenticated", http.StatusUnauthorized)
		return
	}

	data := struct {
		Title     string
		User      *UserTemplateData
		Timestamp time.Time
		ChatPage  bool
	}{
		Title:     "GoClaw",
		User:      &UserTemplateData{Name: u.Name, Username: u.ID, Role: string(u.Role), IsOwner: u.IsOwner()},
		Timestamp: time.Now(),
		ChatPage:  false,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "index.html", data); err != nil {
		logging.L_error("http: template error", "error", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

// handleChat serves the chat interface
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	// Reload templates in dev mode
	if err := s.reloadTemplatesIfDev(); err != nil {
		logging.L_error("http: template reload error", "error", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}

	u := getUserFromContext(r)
	if u == nil {
		logging.L_error("http: chat failed - no user in context")
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
		logging.L_debug("http: chat in supervision mode", "session", superviseSession, "user", u.ID)
	} else if superviseSession != "" && !u.IsOwner() {
		// Non-owner trying to supervise - reject
		logging.L_warn("http: supervision denied - not owner", "user", u.ID, "session", superviseSession)
		superviseSession = ""
	}

	chatClientCfg := struct {
		IsSupervising    bool   `json:"isSupervising"`
		SuperviseSession string `json:"superviseSession"`
		TypingText       string `json:"typingText"`
	}{
		IsSupervising:    isSupervising,
		SuperviseSession: superviseSession,
		TypingText:       typingText,
	}
	cfgBytes, err := json.Marshal(chatClientCfg)
	if err != nil {
		logging.L_error("http: chat client config marshal failed", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	data := struct {
		Title            string
		User             *UserTemplateData
		AgentName        string
		TypingText       string
		Timestamp        time.Time
		SuperviseSession string
		IsSupervising    bool
		ChatPage         bool
		ChatConfigJSON   template.JS
	}{
		Title:            "GoClaw - Chat",
		User:             &UserTemplateData{Name: u.Name, Username: u.ID, Role: string(u.Role), IsOwner: u.IsOwner()},
		AgentName:        agentName,
		TypingText:       typingText,
		Timestamp:        time.Now(),
		SuperviseSession: superviseSession,
		IsSupervising:    isSupervising,
		ChatPage:         true,
		// #nosec G203 -- cfgBytes is server-generated JSON (json.Marshal), intentionally embedded as JS object literal.
		ChatConfigJSON: template.JS(cfgBytes),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "chat.html", data); err != nil {
		logging.L_error("http: template error", "error", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

// handleRunnersPage serves the delegated runners dashboard page (owner only).
func (s *Server) handleRunnersPage(w http.ResponseWriter, r *http.Request) {
	if err := s.reloadTemplatesIfDev(); err != nil {
		logging.L_error("http: template reload error", "error", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}

	u := getUserFromContext(r)
	if u == nil {
		http.Error(w, "Not authenticated", http.StatusUnauthorized)
		return
	}
	if !u.IsOwner() {
		http.Error(w, "Forbidden - owner only", http.StatusForbidden)
		return
	}

	data := struct {
		Title     string
		User      *UserTemplateData
		Timestamp time.Time
		ChatPage  bool
	}{
		Title:     "GoClaw - Runners",
		User:      &UserTemplateData{Name: u.Name, Username: u.ID, Role: string(u.Role), IsOwner: u.IsOwner()},
		Timestamp: time.Now(),
		ChatPage:  false,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "runners.html", data); err != nil {
		logging.L_error("http: runners template error", "error", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

// handleTranscript serves a read-only transcript page that renders saved browser localStorage history.
func (s *Server) handleTranscript(w http.ResponseWriter, r *http.Request) {
	if err := s.reloadTemplatesIfDev(); err != nil {
		logging.L_error("http: template reload error", "error", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}

	u := getUserFromContext(r)
	if u == nil {
		logging.L_error("http: transcript failed - no user in context")
		http.Error(w, "Not authenticated", http.StatusUnauthorized)
		return
	}

	superviseSession := strings.TrimSpace(r.URL.Query().Get("supervise"))
	isSupervising := false
	if superviseSession != "" && u.IsOwner() {
		isSupervising = true
	} else if superviseSession != "" && !u.IsOwner() {
		logging.L_warn("http: transcript supervision denied - not owner", "user", u.ID, "session", superviseSession)
		superviseSession = ""
	}

	transcriptClientCfg := struct {
		IsSupervising    bool   `json:"isSupervising"`
		SuperviseSession string `json:"superviseSession"`
	}{
		IsSupervising:    isSupervising,
		SuperviseSession: superviseSession,
	}
	cfgBytes, err := json.Marshal(transcriptClientCfg)
	if err != nil {
		logging.L_error("http: transcript config marshal failed", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	data := struct {
		Title              string
		User               *UserTemplateData
		Timestamp          time.Time
		ChatPage           bool
		IsSupervising      bool
		SuperviseSession   string
		TranscriptConfigJS template.JS
	}{
		Title:            "GoClaw - Transcript",
		User:             &UserTemplateData{Name: u.Name, Username: u.ID, Role: string(u.Role), IsOwner: u.IsOwner()},
		Timestamp:        time.Now(),
		ChatPage:         false,
		IsSupervising:    isSupervising,
		SuperviseSession: superviseSession,
		// #nosec G203 -- cfgBytes is server-generated JSON (json.Marshal), intentionally embedded as JS object literal.
		TranscriptConfigJS: template.JS(cfgBytes),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "transcript.html", data); err != nil {
		logging.L_error("http: transcript template error", "error", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

// handleVoice serves the voice chat interface
func (s *Server) handleVoice(w http.ResponseWriter, r *http.Request) {
	// Reload templates in dev mode
	if err := s.reloadTemplatesIfDev(); err != nil {
		logging.L_error("http: template reload error", "error", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}

	u := getUserFromContext(r)
	if u == nil {
		logging.L_error("http: voice failed - no user in context")
		http.Error(w, "Not authenticated", http.StatusUnauthorized)
		return
	}

	voiceAvailability := s.voiceAvailability()

	data := struct {
		Title              string
		User               *UserTemplateData
		Timestamp          time.Time
		VoiceAvailable     bool
		VoiceStatusMessage string
		ChatPage           bool
	}{
		Title:              "GoClaw - Voice",
		User:               &UserTemplateData{Name: u.Name, Username: u.ID, Role: string(u.Role), IsOwner: u.IsOwner()},
		Timestamp:          time.Now(),
		VoiceAvailable:     voiceAvailability.Available,
		VoiceStatusMessage: voiceAvailability.Message,
		ChatPage:           false,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "voice.html", data); err != nil {
		logging.L_error("http: template error", "error", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

func (s *Server) voiceAvailability() voicellm.Availability {
	if s.channel == nil || s.channel.gateway == nil {
		return voicellm.Availability{
			Available: false,
			Message:   "Voice chat backend is not attached to the HTTP server.",
		}
	}

	gatewayWithConfig, ok := s.channel.gateway.(interface {
		Config() *config.Config
	})
	if !ok || gatewayWithConfig.Config() == nil {
		registry := voicellm.GetRegistry()
		if registry == nil {
			return voicellm.Availability{
				Available: false,
				Message:   "Voice chat is not initialized.",
			}
		}
		return voicellm.AssessConfig(registry.GetConfig())
	}

	return voicellm.AssessConfig(gatewayWithConfig.Config().VoiceLLM)
}

// handleStaticJS serves embedded static assets under /js/.
// Served without auth middleware for AudioWorklet compatibility.
func (s *Server) handleStaticJS(w http.ResponseWriter, r *http.Request) {
	// In dev mode, serve from disk
	if s.devMode && s.templatesDir != "" {
		filePath := filepath.Join(s.templatesDir, r.URL.Path[1:]) // Remove leading /
		http.ServeFile(w, r, filePath)
		return
	}

	// Production: serve from embedded FS
	jsFS, err := fs.Sub(htmlFS, "html")
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	// Strip / prefix and let FileServer infer the content type by extension.
	http.StripPrefix("/", http.FileServer(http.FS(jsFS))).ServeHTTP(w, r)
}

// UserTemplateData holds user info for templates
type UserTemplateData struct {
	Name     string
	Username string
	Role     string
	IsOwner  bool
}

const (
	httpMultipartMaxMemory    = 64 << 20 // buffered to memory before spill
	httpMultipartMaxFiles     = 10
	httpMultipartFieldFiles   = "files"
	httpMultipartFieldMessage = "message"
)

// httpMediaStore returns the gateway media store for persisting uploads, or nil.
func (s *Server) httpMediaStore() *media.MediaStore {
	if s.channel == nil || s.channel.gateway == nil {
		return nil
	}
	type mediaStoreProvider interface {
		MediaStore() *media.MediaStore
	}
	gw, ok := s.channel.gateway.(mediaStoreProvider)
	if !ok {
		return nil
	}
	return gw.MediaStore()
}

// trySendPreflight handles shutdown phrase, panic phrase, /thinking, and built-in slash commands.
// Returns true if the HTTP response was fully written and the caller must return.
func (s *Server) trySendPreflight(w http.ResponseWriter, r *http.Request, u *user.User, sessionID string, message string) bool {
	if s.channel == nil {
		return false
	}

	if commands.IsShutdownPhrase(message) {
		if s.channel.gateway != nil {
			if err := s.channel.gateway.RequestShutdown(u.ID); err == nil {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "message": "Shutting down now."})
				return true
			}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "error", "message": "Shutdown denied."})
		return true
	}

	if commands.IsPanicPhrase(message) {
		if s.channel.gateway != nil {
			s.channel.gateway.StopAllUserSessions(u.ID) //nolint:errcheck
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "message": "Stopping all tasks. Send /resume to continue."})
		return true
	}

	if strings.HasPrefix(strings.TrimSpace(message), "/thinking") {
		s.handleThinkingCommand(w, sessionID, message)
		return true
	}

	trimmedMsg := strings.TrimSpace(message)
	if strings.HasPrefix(trimmedMsg, "/") {
		parts := strings.Fields(trimmedMsg)
		if len(parts) > 0 {
			if cmd := commands.GetManager().Get(parts[0]); cmd != nil {
				s.handleBuiltinCommand(w, r.Context(), sessionID, u.ID, trimmedMsg, cmd)
				return true
			}
		}
	}

	return false
}

// handleSend handles POST /api/send - send message to agent
func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		logging.L_warn("http: send - wrong method", "method", r.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	u := getUserFromContext(r)
	if u == nil {
		logging.L_error("http: send failed - no user in context")
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
		logging.L_warn("http: send - invalid JSON", "user", u.ID, "error", err)
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Need either message or images
	if req.Message == "" && len(req.Images) == 0 {
		logging.L_warn("http: send - empty message and no images", "user", u.ID)
		http.Error(w, "Message or image required", http.StatusBadRequest)
		return
	}

	sessionID := getSessionFromContext(r)
	if sessionID == "" {
		logging.L_error("http: send failed - no session in context", "user", u.ID)
		http.Error(w, "No session", http.StatusInternalServerError)
		return
	}

	// Convert images to ContentBlocks
	var contentBlocks []types.ContentBlock
	for _, img := range req.Images {
		contentBlocks = append(contentBlocks, types.ContentBlock{
			Type:     "image",
			Data:     img.Data,
			MimeType: img.MimeType,
			Source:   "http",
		})
	}

	logging.L_info("http: message received", "user", u.ID, "session", sessionID[:8]+"...", "length", len(req.Message), "images", len(contentBlocks))

	if s.trySendPreflight(w, r, u, sessionID, req.Message) {
		return
	}

	// Run agent request (will stream via SSE)
	msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	err := s.channel.RunAgentRequest(r.Context(), sessionID, u, req.Message, contentBlocks)
	if err != nil {
		logging.L_error("http: failed to run agent", "user", u.ID, "error", err)
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
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logging.L_warn("http: failed to encode response", "error", err)
	}
}

func httpGatewaySessionKey(sess *SSESession) string {
	if sess == nil {
		return ""
	}
	if sess.User != nil {
		if sess.User.IsOwner() {
			return session.PrimarySession
		}
		return fmt.Sprintf("user:%s", sess.User.ID)
	}
	return sess.SessionID
}

func (s *Server) handleACPRespond(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u := getUserFromContext(r)
	if u == nil {
		http.Error(w, "Not authenticated", http.StatusUnauthorized)
		return
	}
	sessionID := getSessionFromContext(r)
	if sessionID == "" {
		http.Error(w, "No session", http.StatusInternalServerError)
		return
	}
	sess := s.channel.GetSession(sessionID)
	if sess == nil {
		http.Error(w, "No session", http.StatusNotFound)
		return
	}
	sessionKey := httpGatewaySessionKey(sess)
	if sessionKey == "" {
		http.Error(w, "No session key", http.StatusBadRequest)
		return
	}
	var req struct {
		Driver          string          `json:"driver"`
		Method          string          `json:"method"`
		ToolCallID      string          `json:"toolCallId"`
		ResponsePayload json.RawMessage `json:"responsePayload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Driver) == "" || strings.TrimSpace(req.Method) == "" {
		http.Error(w, "driver and method are required", http.StatusBadRequest)
		return
	}
	if s.channel.gateway == nil {
		http.Error(w, "Gateway not configured", http.StatusInternalServerError)
		return
	}
	if err := s.channel.gateway.ACPRespond(sessionKey, acp.ACPDriverExtensionResponse{
		Driver:          strings.TrimSpace(req.Driver),
		Method:          strings.TrimSpace(req.Method),
		ToolCallID:      strings.TrimSpace(req.ToolCallID),
		ResponsePayload: req.ResponsePayload,
	}); err != nil {
		logging.L_warn("http: ACP interactive response failed", "user", u.ID, "session", sessionKey, "driver", req.Driver, "method", req.Method, "toolCallID", req.ToolCallID, "error", err)
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
}

// handleSendMultipart handles POST /api/send/multipart — message text + file parts (saved via MediaStore).
func (s *Server) handleSendMultipart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		logging.L_warn("http: send multipart - wrong method", "method", r.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u := getUserFromContext(r)
	if u == nil {
		logging.L_error("http: send multipart - no user in context")
		http.Error(w, "Not authenticated", http.StatusUnauthorized)
		return
	}
	sessionID := getSessionFromContext(r)
	if sessionID == "" {
		logging.L_error("http: send multipart - no session", "user", u.ID)
		http.Error(w, "No session", http.StatusInternalServerError)
		return
	}
	store := s.httpMediaStore()
	if store == nil {
		logging.L_error("http: send multipart - no media store", "user", u.ID)
		http.Error(w, "Server not ready", http.StatusInternalServerError)
		return
	}
	if s.channel == nil {
		logging.L_error("http: send multipart - no channel", "user", u.ID)
		http.Error(w, "Server not ready", http.StatusInternalServerError)
		return
	}

	if err := r.ParseMultipartForm(httpMultipartMaxMemory); err != nil {
		logging.L_warn("http: send multipart - parse failed", "user", u.ID, "error", err)
		http.Error(w, "Invalid multipart body", http.StatusBadRequest)
		return
	}
	msg := strings.TrimSpace(r.FormValue(httpMultipartFieldMessage))
	if r.MultipartForm == nil {
		http.Error(w, "Invalid multipart form", http.StatusBadRequest)
		return
	}
	headers := r.MultipartForm.File[httpMultipartFieldFiles]
	if len(headers) == 0 {
		logging.L_warn("http: send multipart - no files", "user", u.ID)
		http.Error(w, "At least one file field "+httpMultipartFieldFiles+" is required", http.StatusBadRequest)
		return
	}
	if len(headers) > httpMultipartMaxFiles {
		logging.L_warn("http: send multipart - too many files", "user", u.ID, "count", len(headers))
		http.Error(w, fmt.Sprintf("Too many files (max %d)", httpMultipartMaxFiles), http.StatusBadRequest)
		return
	}

	var contentBlocks []types.ContentBlock
	for _, fh := range headers {
		f, err := fh.Open()
		if err != nil {
			logging.L_warn("http: send multipart - open file failed", "user", u.ID, "error", err)
			http.Error(w, "Failed to read upload", http.StatusBadRequest)
			return
		}
		data, err := io.ReadAll(f)
		_ = f.Close()
		if err != nil {
			logging.L_warn("http: send multipart - read failed", "user", u.ID, "error", err)
			http.Error(w, "Failed to read upload", http.StatusBadRequest)
			return
		}
		if len(data) == 0 {
			continue
		}
		ext := strings.ToLower(filepath.Ext(fh.Filename))
		if ext == "" {
			ext = ".bin"
		}
		mime := media.DetectMIME(data)
		mediaType := "file"
		if strings.HasPrefix(mime, "image/") {
			mediaType = "image"
		}
		ctx := media.UploadContext{
			Channel:      "http",
			User:         u,
			ChatID:       sessionID,
			MediaType:    mediaType,
			OriginalName: fh.Filename,
		}
		absPath, _, err := store.SaveUpload(data, ext, ctx)
		if err != nil {
			logging.L_warn("http: send multipart - save failed", "user", u.ID, "error", err)
			http.Error(w, "Failed to store upload", http.StatusInternalServerError)
			return
		}
		if mediaType == "image" {
			contentBlocks = append(contentBlocks, types.ContentBlock{
				Type:     "image",
				FilePath: absPath,
				MimeType: mime,
				Source:   "http",
			})
		} else {
			contentBlocks = append(contentBlocks, types.ContentBlock{
				Type:     "file",
				FilePath: absPath,
				MimeType: mime,
				FileName: fh.Filename,
				Source:   "http",
			})
		}
	}

	if len(contentBlocks) == 0 {
		http.Error(w, "No non-empty file parts", http.StatusBadRequest)
		return
	}

	logging.L_info("http: multipart message received", "user", u.ID, "session", sessionID[:8]+"...", "files", len(contentBlocks))

	if s.trySendPreflight(w, r, u, sessionID, msg) {
		return
	}

	msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	if err := s.channel.RunAgentRequest(r.Context(), sessionID, u, msg, contentBlocks); err != nil {
		logging.L_error("http: multipart run agent failed", "user", u.ID, "error", err)
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
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logging.L_warn("http: failed to encode multipart response", "error", err)
	}
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
		if sess.ThinkingLevel == "" || sess.ThinkingLevel == "off" {
			sess.ThinkingLevel = llm.DefaultThinkingLevel.String()
		}
		resultMsg = fmt.Sprintf("Thinking output enabled (level: %s).", sess.ThinkingLevel)
	case "off":
		sess.ShowThinking = false
		sess.ThinkingLevel = "off"
		resultMsg = "Thinking output disabled. You'll only see final responses."
	case "toggle", "":
		sess.ShowThinking = !sess.ShowThinking
		if sess.ShowThinking {
			if sess.ThinkingLevel == "" || sess.ThinkingLevel == "off" {
				sess.ThinkingLevel = llm.DefaultThinkingLevel.String()
			}
			resultMsg = fmt.Sprintf("Thinking output enabled (level: %s).", sess.ThinkingLevel)
		} else {
			resultMsg = "Thinking output disabled."
		}
	case "status":
		if sess.ShowThinking {
			level := sess.ThinkingLevel
			if level == "" {
				level = llm.DefaultThinkingLevel.String()
			}
			resultMsg = fmt.Sprintf("Thinking output: ON, level: %s", level)
		} else {
			resultMsg = "Thinking output: OFF"
		}
	default:
		// Check if arg is a valid thinking level
		if llm.IsValidThinkingLevel(arg) {
			sess.ThinkingLevel = arg
			if arg == "off" {
				sess.ShowThinking = false
				resultMsg = "Thinking disabled."
			} else {
				sess.ShowThinking = true // Setting a level automatically enables thinking display
				resultMsg = fmt.Sprintf("Thinking level set to %s (output enabled).", arg)
			}
		} else {
			resultMsg = "Usage: /thinking [on|off|toggle|status|minimal|low|medium|high|xhigh]"
		}
	}

	// Send preference event to client with both thinking enabled and level
	sess.SendEvent(SSEEvent{
		Event: "preference",
		Data: map[string]interface{}{
			"key":   "thinking",
			"value": sess.ShowThinking,
			"level": sess.ThinkingLevel,
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
	if err := json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"message": resultMsg,
	}); err != nil {
		logging.L_warn("http: failed to encode response", "error", err)
	}
}

// handleBuiltinCommand handles built-in slash commands (/status, /compact, /clear, etc.)
func (s *Server) handleBuiltinCommand(w http.ResponseWriter, ctx context.Context, sessionID string, userID string, message string, cmd *commands.Command) {
	sess := s.channel.GetSession(sessionID)
	if sess == nil {
		http.Error(w, "Session not found", http.StatusInternalServerError)
		return
	}

	logging.L_info("http: handling command", "command", cmd.Name, "session", sessionID[:8]+"...")

	// For long-running commands like /compact, send a "working" message first
	if cmd.Name == "/compact" {
		sess.SendEvent(SSEEvent{
			Event: "system",
			Data: map[string]string{
				"message": "Compacting session... (this may take a minute)",
			},
		})
	}

	// Map HTTP transport session IDs to gateway session keys.
	// Owner traffic always runs on the shared primary session.
	commandSessionKey := sessionID
	if sess.User != nil {
		if sess.User.IsOwner() {
			commandSessionKey = session.PrimarySession
		} else {
			commandSessionKey = fmt.Sprintf("user:%s", sess.User.ID)
		}
	}

	// Execute command via manager (which has the provider wired up)
	mgr := commands.GetManager()
	result := mgr.Execute(ctx, message, commandSessionKey, userID)

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
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logging.L_warn("http: failed to encode response", "error", err)
	}
}

// commandListItem is JSON for GET /api/commands (web chat command palette).
type commandListItem struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Usage       string   `json:"usage,omitempty"`
	Aliases     []string `json:"aliases,omitempty"`
	OwnerOnly   bool     `json:"ownerOnly"`
}

// handleCommands handles GET /api/commands — built-in slash commands for the chat palette.
func (s *Server) handleCommands(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		logging.L_warn("http: commands - wrong method", "method", r.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u := getUserFromContext(r)
	if u == nil {
		logging.L_error("http: commands - no user in context")
		http.Error(w, "Not authenticated", http.StatusUnauthorized)
		return
	}

	list := commands.GetManager().List()
	out := make([]commandListItem, 0, len(list))
	for _, cmd := range list {
		if cmd.OwnerOnly && !u.IsOwner() {
			continue
		}
		aliases := cmd.Aliases
		if aliases == nil {
			aliases = []string{}
		}
		out = append(out, commandListItem{
			Name:        cmd.Name,
			Description: cmd.Description,
			Usage:       cmd.Usage,
			Aliases:     aliases,
			OwnerOnly:   cmd.OwnerOnly,
		})
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		logging.L_warn("http: commands - encode failed", "error", err)
	}
}

// handleEvents handles GET /api/events - SSE stream
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	u := getUserFromContext(r)
	if u == nil {
		logging.L_error("http: SSE failed - no user in context")
		http.Error(w, "Not authenticated", http.StatusUnauthorized)
		return
	}

	// Set SSE headers
	configureSSEHeaders(w)
	rc := configureSSEWriteDeadline(w, "/api/events")

	_, ok := w.(http.Flusher)
	if !ok {
		logging.L_error("http: SSE failed - flusher not supported", "user", u.ID)
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	if s.channel == nil {
		logging.L_error("http: SSE failed - channel not initialized", "user", u.ID)
		http.Error(w, "Server not ready", http.StatusInternalServerError)
		return
	}

	sessionID := getSessionFromContext(r)
	if sessionID == "" {
		logging.L_error("http: SSE failed - no session in context", "user", u.ID)
		http.Error(w, "No session", http.StatusInternalServerError)
		return
	}

	// Parse Last-Event-ID for replay (SSE standard reconnection mechanism)
	lastEventID := 0
	if lastIDStr := r.Header.Get("Last-Event-ID"); lastIDStr != "" {
		if parsed, err := strconv.Atoi(lastIDStr); err == nil {
			lastEventID = parsed
			logging.L_trace("http: SSE reconnect", "user", u.ID, "session", sessionID[:8]+"...", "lastEventID", lastEventID)
		}
	} else {
		logging.L_info("http: SSE connection opened", "user", u.ID, "session", sessionID[:8]+"...")
	}

	// Register connection and get events to replay
	sess, conn, replay := s.channel.RegisterConnection(sessionID, u, lastEventID)
	if conn == nil {
		logging.L_error("http: SSE failed - connection registration returned nil", "user", u.ID)
		http.Error(w, "Failed to register connection", http.StatusInternalServerError)
		return
	}
	defer s.channel.UnregisterConnection(sessionID, conn)

	// Send initial connected event (with current event ID for client tracking)
	sess.bufferMu.Lock()
	currentEventID := sess.nextEventID - 1
	sess.bufferMu.Unlock()
	if !sseWritef(w, rc, "/api/events", "retry: 2000\n\n") {
		return
	}
	if !sseWritef(w, rc, "/api/events", "event: connected\nid: %d\ndata: {\"user\":\"%s\",\"lastEventId\":%d}\n\n", currentEventID, u.ID, currentEventID) {
		return
	}

	// Send current preferences
	prefData, _ := json.Marshal(map[string]interface{}{
		"key":   "thinking",
		"value": sess.ShowThinking,
	})
	if !sseWritef(w, rc, "/api/events", "event: preference\ndata: %s\n\n", prefData) {
		return
	}

	// Replay missed events
	for _, buffered := range replay {
		data, err := json.Marshal(buffered.Event.Data)
		if err != nil {
			logging.L_error("http: failed to marshal replay event", "error", err)
			continue
		}
		if !sseWritef(w, rc, "/api/events", "event: %s\nid: %d\ndata: %s\n\n", buffered.Event.Event, buffered.ID, data) {
			return
		}
	}
	if len(replay) > 0 {
		logging.L_info("http: replayed events", "count", len(replay), "session", sessionID[:8]+"...")
	}

	// Keep connection open and forward events
	ctx := r.Context()
	ticker := time.NewTicker(15 * time.Second) // Heartbeat every 15s (more frequent for stability)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logging.L_trace("http: SSE connection closed", "user", u.ID, "session", sessionID[:8]+"...")
			return
		case <-conn.Done:
			logging.L_info("http: SSE connection replaced", "user", u.ID, "session", sessionID[:8]+"...")
			return
		case event := <-conn.Events:
			// Send event to client with ID
			data, err := json.Marshal(event.Data)
			if err != nil {
				logging.L_error("http: failed to marshal event", "error", err)
				continue
			}
			// Get current event ID from session
			sess.bufferMu.Lock()
			eventID := sess.nextEventID - 1 // Last assigned ID
			sess.bufferMu.Unlock()
			if !sseWritef(w, rc, "/api/events", "event: %s\nid: %d\ndata: %s\n\n", event.Event, eventID, data) {
				return
			}
		case <-ticker.C:
			// Send heartbeat comment (doesn't need ID)
			if !sseWritef(w, rc, "/api/events", ": heartbeat\n\n") {
				return
			}
		}
	}
}

// handleStatus handles GET /api/status - agent status
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		logging.L_warn("http: status - wrong method", "method", r.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	u := getUserFromContext(r)
	if u == nil {
		logging.L_error("http: status failed - no user in context")
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
		InstanceID      string               `json:"instanceID"`
		StartedAt       time.Time            `json:"startedAt"`
		User            string               `json:"user"`
		IsOwner         bool                 `json:"isOwner"`
		Sessions        []SessionInfo        `json:"sessions"`
		GatewaySessions []GatewaySessionInfo `json:"gatewaySessions,omitempty"`
	}{
		Status:          "ready",
		InstanceID:      s.instanceID,
		StartedAt:       s.startedAt,
		User:            u.ID,
		IsOwner:         u.IsOwner(),
		Sessions:        sessions,
		GatewaySessions: gatewaySessions,
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(status); err != nil {
		logging.L_warn("http: failed to encode status", "error", err)
	}
}

// handleRunners handles GET /api/runners - delegated run listing (owner only).
func (s *Server) handleRunners(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	u := getUserFromContext(r)
	if u == nil {
		http.Error(w, "Not authenticated", http.StatusUnauthorized)
		return
	}
	if !u.IsOwner() {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if s.channel == nil || s.channel.gateway == nil {
		http.Error(w, "Gateway unavailable", http.StatusServiceUnavailable)
		return
	}

	runs := s.channel.gateway.ListDelegatedRuns()
	items := normalizeRunnerItems(runs)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"items": items,
	}); err != nil {
		logging.L_warn("http: failed to encode delegated runners", "error", err)
	}
}

func (s *Server) handleRunnersAction(w http.ResponseWriter, r *http.Request) {
	u := getUserFromContext(r)
	if u == nil {
		http.Error(w, "Not authenticated", http.StatusUnauthorized)
		return
	}
	if !u.IsOwner() {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if s.channel == nil || s.channel.gateway == nil {
		http.Error(w, "Gateway unavailable", http.StatusServiceUnavailable)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/runners/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Invalid runner path", http.StatusBadRequest)
		return
	}
	runID := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch {
	case r.Method == http.MethodGet && action == "":
		runs := s.channel.gateway.ListDelegatedRuns()
		run, found := findRunnerRecord(runs, runID)
		if !found {
			http.Error(w, "Runner not found", http.StatusNotFound)
			return
		}
		details := normalizeRunnerItems(runs)
		var normalized map[string]interface{}
		for _, item := range details {
			if item["runId"] == runID {
				normalized = item
				break
			}
		}
		if normalized == nil {
			normalized = normalizeRunnerRecord(run, runs)
		}
		if promptPreview := runnerPromptPreview(s.channel.gateway, run.SessionKey); promptPreview != "" {
			normalized["promptPreview"] = promptPreview
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"run": normalized,
		})
		return
	case r.Method == http.MethodGet && action == "transcript":
		runs := s.channel.gateway.ListDelegatedRuns()
		run, found := findRunnerRecord(runs, runID)
		if !found {
			http.Error(w, "Runner not found", http.StatusNotFound)
			return
		}
		historyGateway, ok := s.channel.gateway.(runnerHistoryProvider)
		if !ok {
			http.Error(w, "Transcript unavailable", http.StatusNotImplemented)
			return
		}
		msgs, err := historyGateway.History(run.SessionKey)
		if err != nil {
			http.Error(w, "Transcript unavailable: "+err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"runId":      runID,
			"sessionKey": run.SessionKey,
			"items":      normalizeRunnerTranscript(msgs),
		})
		return
	case r.Method == http.MethodPost && action == "cancel":
		if err := s.channel.gateway.CancelDelegatedRun(runID); err != nil {
			http.Error(w, "Cancel failed: "+err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    true,
			"runId": runID,
		})
		return
	default:
		http.Error(w, "Unknown action", http.StatusBadRequest)
		return
	}
}

func findRunnerRecord(runs []delegatedrun.RunRecord, runID string) (delegatedrun.RunRecord, bool) {
	for _, item := range runs {
		if item.RunID == runID {
			return item, true
		}
	}
	return delegatedrun.RunRecord{}, false
}

func runnerPromptPreview(gw interface{}, sessionKey string) string {
	historyGateway, ok := gw.(runnerHistoryProvider)
	if !ok || strings.TrimSpace(sessionKey) == "" {
		return ""
	}
	msgs, err := historyGateway.History(sessionKey)
	if err != nil {
		return ""
	}
	for _, msg := range msgs {
		text := strings.TrimSpace(msg.Content)
		if msg.Role == "user" && text != "" {
			return truncateRunnerText(text, 180)
		}
	}
	for _, msg := range msgs {
		text := strings.TrimSpace(msg.Content)
		if text != "" {
			return truncateRunnerText(text, 180)
		}
	}
	return ""
}

func normalizeRunnerTranscript(msgs []session.Message) []map[string]interface{} {
	items := make([]map[string]interface{}, 0, len(msgs))
	for _, msg := range msgs {
		items = append(items, map[string]interface{}{
			"id":               msg.ID,
			"role":             msg.Role,
			"content":          strings.TrimSpace(msg.Content),
			"source":           msg.Source,
			"timestamp":        msg.Timestamp,
			"toolName":         msg.ToolName,
			"toolInput":        string(msg.ToolInput),
			"toolUseId":        msg.ToolUseID,
			"supervisor":       msg.Supervisor,
			"interventionType": msg.InterventionType,
		})
	}
	return items
}

func truncateRunnerText(text string, max int) string {
	text = strings.TrimSpace(text)
	if max <= 0 || len(text) <= max {
		return text
	}
	return text[:max] + "..."
}

func normalizeRunnerItems(runs []delegatedrun.RunRecord) []map[string]interface{} {
	items := make([]map[string]interface{}, 0, len(runs))
	for _, run := range runs {
		items = append(items, normalizeRunnerRecord(run, runs))
	}
	return items
}

func normalizeRunnerRecord(run delegatedrun.RunRecord, all []delegatedrun.RunRecord) map[string]interface{} {
	coordinator := delegatedrun.NewGraphDescendantCoordinator(all)
	hasActiveDescendants, activeDescendantCount := coordinator.HasActiveDescendants(run.RunID)
	now := time.Now()
	bindingAgeSeconds, bindingIdleSeconds, canDirectDispatch, directDispatchReason := delegatedrun.BindingTelemetry(run, now)
	item := map[string]interface{}{
		"runId":                        run.RunID,
		"parentRunId":                  run.ParentRunID,
		"requesterType":                run.RequesterType,
		"requesterId":                  run.RequesterID,
		"requesterSessionKey":          run.RequesterSessionKey,
		"requesterBindingState":        run.RequesterBindingState,
		"requesterBindingReason":       run.RequesterBindingReason,
		"requesterBindingUpdatedAt":    run.RequesterBindingUpdatedAt,
		"requesterBindingLastActiveAt": run.RequesterBindingLastActiveAt,
		"requesterBindingAgeSeconds":   bindingAgeSeconds,
		"requesterBindingIdleSeconds":  bindingIdleSeconds,
		"canDirectDispatch":            canDirectDispatch,
		"directDispatchReason":         directDispatchReason,
		"sessionKey":                   run.SessionKey,
		"purpose":                      run.Purpose,
		"resultMode":                   run.ResultMode,
		"expectsCompletionMessage":     run.ExpectsCompletionMessage,
		"dispatchOrder":                run.DispatchOrder,
		"fallbackMode":                 run.FallbackMode,
		"injectMode":                   run.InjectMode,
		"completionDispatchKey":        run.CompletionDispatchKey,
		"completionDispatchSeq":        run.CompletionDispatchSeq,
		"cleanupState":                 run.CleanupState,
		"deferredReason":               run.DeferredReason,
		"dispatchPhases":               run.DispatchPhases,
		"continuationState":            run.ContinuationState,
		"continuationReason":           run.ContinuationReason,
		"continuationWakeAt":           run.ContinuationWakeAt,
		"hasActiveDescendants":         hasActiveDescendants,
		"activeDescendantCount":        activeDescendantCount,
		"state":                        run.State,
		"startedAt":                    run.StartedAt,
		"finishedAt":                   run.FinishedAt,
		"result": map[string]interface{}{
			"finalText": run.Result.FinalText,
			"error":     run.Result.Error,
			"usage":     run.Result.Usage,
		},
	}
	return item
}

// handleRunnerEvents handles GET /api/runners/events as SSE stream backed by delegated_run_events.
func (s *Server) handleRunnerEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u := getUserFromContext(r)
	if u == nil {
		http.Error(w, "Not authenticated", http.StatusUnauthorized)
		return
	}
	if !u.IsOwner() {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if s.channel == nil || s.channel.gateway == nil {
		http.Error(w, "Gateway unavailable", http.StatusServiceUnavailable)
		return
	}

	lastID := int64(0)
	if v := strings.TrimSpace(r.Header.Get("Last-Event-ID")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			lastID = n
		}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("since")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			lastID = n
		}
	}

	configureSSEHeaders(w)
	rc := configureSSEWriteDeadline(w, "/api/runners/events")

	_, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	writeEvent := func(id int64, eventType string, payload interface{}) bool {
		data, err := json.Marshal(payload)
		if err != nil {
			logging.L_warn("http: runners sse marshal failed", "eventType", eventType, "error", err)
			return false
		}
		return sseWritef(w, rc, "/api/runners/events", "event: %s\nid: %d\ndata: %s\n\n", eventType, id, data)
	}

	ctx := r.Context()
	pollTicker := time.NewTicker(1000 * time.Millisecond)
	heartbeatTicker := time.NewTicker(15 * time.Second)
	defer pollTicker.Stop()
	defer heartbeatTicker.Stop()

	if !sseWritef(w, rc, "/api/runners/events", "retry: 2000\n\n") {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-pollTicker.C:
			events := s.channel.gateway.ListDelegatedRunEvents(lastID, 200)
			for _, ev := range events {
				payload := map[string]interface{}{
					"runId":         ev.RunID,
					"eventType":     ev.EventType,
					"payload":       ev.Payload,
					"timestamp":     ev.Timestamp,
					"schemaVersion": 1,
				}
				if ok := writeEvent(ev.ID, "delegated.run."+ev.EventType, payload); !ok {
					return
				}
				lastID = ev.ID
			}
		case <-heartbeatTicker.C:
			if !sseWritef(w, rc, "/api/runners/events", ": heartbeat\n\n") {
				return
			}
		}
	}
}

// handleMedia serves media files from media root or allowed paths
func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	u := getUserFromContext(r)
	if u == nil {
		logging.L_error("http: media failed - no user in context")
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
		logging.L_warn("http: media access denied", "path", filePath, "user", u.ID, "error", err)
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
		logging.L_error("http: media stat error", "path", absPath, "error", err)
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	if info.IsDir() {
		http.Error(w, "Cannot serve directory", http.StatusBadRequest)
		return
	}

	logging.L_debug("http: serving media", "path", absPath, "user", u.ID, "size", info.Size())

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
	if err := json.NewEncoder(w).Encode(snapshot); err != nil {
		logging.L_warn("http: failed to encode metrics snapshot", "error", err)
	}
}

// handleMetrics handles GET /metrics - metrics dashboard page
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	// Reload templates in dev mode
	if err := s.reloadTemplatesIfDev(); err != nil {
		logging.L_error("http: template reload error", "error", err)
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
		ChatPage  bool
	}{
		Title:     "GoClaw - Metrics",
		User:      &UserTemplateData{Name: u.Name, Username: u.ID, Role: string(u.Role), IsOwner: u.IsOwner()},
		Timestamp: time.Now(),
		ChatPage:  false,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "metrics.html", data); err != nil {
		logging.L_error("http: template error", "error", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}
