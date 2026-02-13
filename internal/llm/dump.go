package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

const (
	dumpDirName   = "llm_dumps"
	maxDumpFiles  = 20
)

// requestCaptureKey is the context key for per-request capture data
type requestCaptureKey struct{}

// RequestCapture holds HTTP capture data for a single request.
// This provides isolation so concurrent requests don't overwrite each other.
type RequestCapture struct {
	RequestBody   []byte
	ResponseBody  []byte
	Status        int
	URL           string
	StreamCapture *StreamingCapture
	mu            sync.RWMutex
}

// GetData returns the captured data thread-safely
func (rc *RequestCapture) GetData() (reqBody, respBody []byte, status int, url string) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	// For streaming responses, get captured data from the stream
	if rc.StreamCapture != nil {
		return rc.RequestBody, rc.StreamCapture.GetCaptured(), rc.Status, rc.URL
	}
	return rc.RequestBody, rc.ResponseBody, rc.Status, rc.URL
}

// NewRequestCapture creates a new per-request capture context
func NewRequestCapture() *RequestCapture {
	return &RequestCapture{}
}

// WithRequestCapture adds a RequestCapture to the context
func WithRequestCapture(ctx context.Context, capture *RequestCapture) context.Context {
	return context.WithValue(ctx, requestCaptureKey{}, capture)
}

// GetRequestCapture retrieves the RequestCapture from context
func GetRequestCapture(ctx context.Context) *RequestCapture {
	if v := ctx.Value(requestCaptureKey{}); v != nil {
		return v.(*RequestCapture)
	}
	return nil
}

// StreamingCapture wraps a response body to capture data while streaming.
// Instead of buffering the entire response (which blocks streaming),
// it captures chunks as they flow through.
type StreamingCapture struct {
	base    io.ReadCloser
	buffer  bytes.Buffer
	onChunk func([]byte) // Callback for SSE chunk processing (e.g., reasoning_details)
	mu      sync.Mutex
}

// Read implements io.Reader, capturing data as it flows through
func (s *StreamingCapture) Read(p []byte) (int, error) {
	n, err := s.base.Read(p)
	if n > 0 {
		s.mu.Lock()
		s.buffer.Write(p[:n]) // Capture for dump
		s.mu.Unlock()

		if s.onChunk != nil {
			s.onChunk(p[:n]) // Process chunk (parse reasoning_details)
		}
	}
	return n, err
}

// Close implements io.Closer
func (s *StreamingCapture) Close() error {
	return s.base.Close()
}

// GetCaptured returns all captured data so far
func (s *StreamingCapture) GetCaptured() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buffer.Bytes()
}

// CapturingTransport is an http.RoundTripper that captures request/response bodies
// for debugging purposes. Thread-safe.
// For streaming responses (SSE), it uses a passthrough reader to avoid blocking.
//
// CONCURRENCY: If a RequestCapture is present in the request context, capture data
// is written there (per-request isolation). Otherwise, falls back to shared buffer
// (legacy behavior, not safe for concurrent requests).
type CapturingTransport struct {
	Base http.RoundTripper

	mu            sync.RWMutex
	lastRequest   []byte
	lastResponse  []byte
	lastStatus    int
	lastURL       string
	streamCapture *StreamingCapture // Active streaming capture, if any
	onChunk       func([]byte)      // Callback for streaming chunk processing

	// Reasoning injection for OpenRouter/Kimi
	reasoningEffort string // If set, inject {"reasoning":{"effort":"..."}} into request body

	// Request counter for debugging concurrent request issues
	activeRequests int64
}

// RoundTrip implements http.RoundTripper, capturing request and response bodies.
// For streaming responses (SSE), it uses a passthrough reader to avoid blocking.
// If reasoningEffort is set, injects {"reasoning":{"effort":"..."}} into JSON request bodies.
//
// CONCURRENCY: If a RequestCapture is present in the request context, capture data
// is written there for per-request isolation. Otherwise falls back to shared buffer.
func (t *CapturingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Track concurrent requests for debugging
	active := atomic.AddInt64(&t.activeRequests, 1)
	defer atomic.AddInt64(&t.activeRequests, -1)

	// Check for per-request capture (provides isolation for concurrent requests)
	reqCapture := GetRequestCapture(req.Context())
	if active > 1 && reqCapture == nil {
		L_warn("transport: concurrent requests without per-request capture - data may be corrupted",
			"activeRequests", active,
			"url", req.URL.String())
	}

	// Capture request body
	var reqBody []byte
	if req.Body != nil {
		reqBody, _ = io.ReadAll(req.Body)
		req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}

	t.mu.Lock()
	reasoningEffort := t.reasoningEffort
	t.reasoningEffort = "" // Reset after use (single-shot)
	t.mu.Unlock()

	// Inject reasoning parameter if set (for OpenRouter/Kimi)
	if reasoningEffort != "" && len(reqBody) > 0 {
		reqBody = injectReasoningParam(reqBody, reasoningEffort)
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
		req.ContentLength = int64(len(reqBody))
	}

	// Store request data in per-request capture if available
	if reqCapture != nil {
		reqCapture.mu.Lock()
		reqCapture.RequestBody = reqBody
		reqCapture.URL = req.URL.String()
		reqCapture.mu.Unlock()
	}

	// Also store in shared buffer (legacy fallback)
	t.mu.Lock()
	t.lastRequest = reqBody
	t.lastURL = req.URL.String()
	t.lastResponse = nil
	t.streamCapture = nil
	t.mu.Unlock()

	// Execute request
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	// Check if this is a streaming response
	contentType := resp.Header.Get("Content-Type")
	isStreaming := strings.Contains(contentType, "text/event-stream") ||
		strings.Contains(contentType, "application/x-ndjson") ||
		strings.Contains(contentType, "application/stream+json")

	// Store response data
	if reqCapture != nil {
		reqCapture.mu.Lock()
		reqCapture.Status = resp.StatusCode

		if isStreaming {
			capture := &StreamingCapture{
				base:    resp.Body,
				onChunk: t.onChunk,
			}
			reqCapture.StreamCapture = capture
			resp.Body = capture
		} else {
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(respBody))
			reqCapture.ResponseBody = respBody
		}
		reqCapture.mu.Unlock()
	}

	// Also update shared buffer (legacy fallback)
	t.mu.Lock()
	t.lastStatus = resp.StatusCode

	if isStreaming && reqCapture == nil {
		// Only use shared streaming capture if no per-request capture
		capture := &StreamingCapture{
			base:    resp.Body,
			onChunk: t.onChunk,
		}
		t.streamCapture = capture
		resp.Body = capture
		L_trace("transport: using streaming capture (shared)", "contentType", contentType)
	} else if !isStreaming && reqCapture == nil {
		// Non-streaming without per-request capture
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		t.lastResponse = respBody
	}
	t.mu.Unlock()

	return resp, nil
}

// GetLastCapture returns the last captured request/response data.
// For streaming responses, the response body is retrieved from the StreamingCapture.
func (t *CapturingTransport) GetLastCapture() (reqBody, respBody []byte, status int, url string) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// For streaming responses, get the captured data from the stream
	if t.streamCapture != nil {
		return t.lastRequest, t.streamCapture.GetCaptured(), t.lastStatus, t.lastURL
	}
	return t.lastRequest, t.lastResponse, t.lastStatus, t.lastURL
}

// ClearCapture clears the captured data
func (t *CapturingTransport) ClearCapture() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastRequest = nil
	t.lastResponse = nil
	t.lastStatus = 0
	t.lastURL = ""
	t.streamCapture = nil
}

// SetOnChunk sets the callback for streaming chunk processing.
// The callback receives each chunk as it arrives (useful for parsing SSE events).
// Must be called before the request is made.
func (t *CapturingTransport) SetOnChunk(fn func([]byte)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onChunk = fn
}

// GetStreamCapture returns the active streaming capture, if any.
// Returns nil for non-streaming responses.
func (t *CapturingTransport) GetStreamCapture() *StreamingCapture {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.streamCapture
}

// SetReasoningEffort sets the reasoning effort level to inject into the next request.
// The effort is injected as {"reasoning":{"effort":"..."}} in the JSON request body.
// This is a single-shot setting - it's cleared after the next RoundTrip.
// Used for OpenRouter/Kimi thinking mode.
func (t *CapturingTransport) SetReasoningEffort(effort string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reasoningEffort = effort
}

// injectReasoningParam injects reasoning parameters into a JSON request body.
// Returns the modified body or the original if injection fails.
func injectReasoningParam(body []byte, effort string) []byte {
	// Parse the JSON body as a generic map
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		L_debug("transport: failed to parse request body for reasoning injection", "error", err)
		return body
	}

	// Inject the reasoning parameter
	data["reasoning"] = map[string]string{"effort": effort}

	// Re-marshal the body
	modified, err := json.Marshal(data)
	if err != nil {
		L_debug("transport: failed to marshal modified request body", "error", err)
		return body
	}

	L_trace("transport: injected reasoning param", "effort", effort, "originalLen", len(body), "modifiedLen", len(modified))
	return modified
}

// dumpDir returns the dump directory path, creating it if needed
func dumpDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	dir := filepath.Join(home, ".goclaw", dumpDirName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create dump dir: %w", err)
	}
	return dir, nil
}

// sanitizeFilename replaces characters that are problematic in filenames
func sanitizeFilename(s string) string {
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	s = strings.ReplaceAll(s, ":", "_")
	s = strings.ReplaceAll(s, " ", "_")
	return s
}

// TokenInfo holds token estimation details for debugging context issues
type TokenInfo struct {
	ContextWindow   int     // Model's context window size
	EstimatedInput  int     // Our token estimate for input
	ConfiguredMax   int     // User-configured max_tokens
	CappedMax       int     // After applying safety limits
	SafetyMargin    float64 // The multiplier applied (e.g., 1.2)
	Buffer          int     // Reserved buffer tokens
}

// DumpContext holds the context for a dump operation.
// Content is buffered in memory and only written to disk on error.
type DumpContext struct {
	Content        strings.Builder  // Buffered dump content
	Provider       string
	Model          string
	CallerFile     string
	CallerLine     int
	StartTime      time.Time
	RequestCapture *RequestCapture  // Per-request HTTP capture (provides isolation)
}

// SetTokenInfo appends token estimation details to the dump buffer.
// Call this after StartDump() to include context window debugging info.
func (ctx *DumpContext) SetTokenInfo(info TokenInfo) {
	if ctx == nil {
		return
	}

	available := info.ContextWindow - info.EstimatedInput - info.Buffer
	usagePercent := 0.0
	if info.ContextWindow > 0 {
		usagePercent = float64(info.EstimatedInput) / float64(info.ContextWindow) * 100
	}

	ctx.Content.WriteString("=== TOKEN ESTIMATION ===\n")
	ctx.Content.WriteString(fmt.Sprintf("Context Window:    %d tokens\n", info.ContextWindow))
	ctx.Content.WriteString(fmt.Sprintf("Estimated Input:   %d tokens (%.1f%% of context)\n", info.EstimatedInput, usagePercent))
	ctx.Content.WriteString(fmt.Sprintf("Buffer Reserved:   %d tokens\n", info.Buffer))
	ctx.Content.WriteString(fmt.Sprintf("Available Output:  %d tokens\n", available))
	ctx.Content.WriteString(fmt.Sprintf("Configured Max:    %d tokens\n", info.ConfiguredMax))
	ctx.Content.WriteString(fmt.Sprintf("Capped Max:        %d tokens\n", info.CappedMax))
	if info.SafetyMargin > 0 {
		ctx.Content.WriteString(fmt.Sprintf("Safety Margin:     %.1fx\n", info.SafetyMargin))
	}
	if info.CappedMax < info.ConfiguredMax {
		ctx.Content.WriteString(fmt.Sprintf("⚠️  max_tokens was reduced by %d to fit context\n", info.ConfiguredMax-info.CappedMax))
	}
	if available <= 0 {
		ctx.Content.WriteString("🚨 CONTEXT OVERFLOW: estimated input exceeds available space!\n")
	}
	ctx.Content.WriteString("\n")
}

// StartDump captures request context in memory before an API call.
// Returns a DumpContext that should be passed to FinishDump* functions.
// The content is only written to disk if an error occurs.
// callerSkip is the number of stack frames to skip (use 1 for direct callers).
func StartDump(provider, model, baseURL string, messages, tools interface{}, systemPrompt string, callerSkip int) *DumpContext {
	// Get caller info
	_, file, line, ok := runtime.Caller(callerSkip)
	if !ok {
		file = "unknown"
		line = 0
	} else {
		// Shorten to just filename
		file = filepath.Base(file)
	}

	ctx := &DumpContext{
		Provider:   provider,
		Model:      model,
		CallerFile: file,
		CallerLine: line,
		StartTime:  time.Now(),
	}

	// Buffer request context in memory
	ctx.Content.WriteString("=== LLM REQUEST DUMP ===\n")
	ctx.Content.WriteString(fmt.Sprintf("Timestamp: %s\n", ctx.StartTime.Format(time.RFC3339)))
	ctx.Content.WriteString(fmt.Sprintf("Source: %s:%d\n", file, line))
	ctx.Content.WriteString(fmt.Sprintf("Provider: %s\n", provider))
	ctx.Content.WriteString(fmt.Sprintf("Model: %s\n", model))
	ctx.Content.WriteString(fmt.Sprintf("BaseURL: %s\n", baseURL))
	ctx.Content.WriteString("\n")

	// System prompt
	if systemPrompt != "" {
		ctx.Content.WriteString("=== SYSTEM PROMPT ===\n")
		if len(systemPrompt) > 2000 {
			ctx.Content.WriteString(systemPrompt[:2000])
			ctx.Content.WriteString(fmt.Sprintf("\n... (truncated, total %d chars)\n", len(systemPrompt)))
		} else {
			ctx.Content.WriteString(systemPrompt)
			ctx.Content.WriteString("\n")
		}
		ctx.Content.WriteString("\n")
	}

	// Messages (JSON format for clarity)
	if messages != nil {
		ctx.Content.WriteString("=== MESSAGES ===\n")
		msgJSON, err := json.MarshalIndent(messages, "", "  ")
		if err != nil {
			ctx.Content.WriteString(fmt.Sprintf("(failed to marshal: %v)\n", err))
		} else {
			// Truncate if too large
			if len(msgJSON) > 50000 {
				ctx.Content.Write(msgJSON[:50000])
				ctx.Content.WriteString(fmt.Sprintf("\n... (truncated, total %d bytes)\n", len(msgJSON)))
			} else {
				ctx.Content.Write(msgJSON)
				ctx.Content.WriteString("\n")
			}
		}
		ctx.Content.WriteString("\n")
	}

	// Tools
	if tools != nil {
		ctx.Content.WriteString("=== TOOLS ===\n")
		toolJSON, err := json.MarshalIndent(tools, "", "  ")
		if err != nil {
			ctx.Content.WriteString(fmt.Sprintf("(failed to marshal: %v)\n", err))
		} else {
			if len(toolJSON) > 20000 {
				ctx.Content.Write(toolJSON[:20000])
				ctx.Content.WriteString(fmt.Sprintf("\n... (truncated, total %d bytes)\n", len(toolJSON)))
			} else {
				ctx.Content.Write(toolJSON)
				ctx.Content.WriteString("\n")
			}
		}
		ctx.Content.WriteString("\n")
	}

	L_trace("dump: request context captured", "provider", provider, "model", model, "bytes", ctx.Content.Len())
	return ctx
}

// SetRequestCapture associates a per-request capture with this dump context.
// This enables isolated capture for concurrent requests.
// Call this after StartDump() and before making the HTTP request.
func (ctx *DumpContext) SetRequestCapture(rc *RequestCapture) {
	if ctx == nil {
		return
	}
	ctx.RequestCapture = rc
}

// FinishDumpError writes the buffered content plus error information to disk.
// This is the only time a dump file is created - only on error.
// Uses per-request capture if available (ctx.RequestCapture), otherwise falls back to transport.
func FinishDumpError(ctx *DumpContext, err error, transport *CapturingTransport) {
	if ctx == nil {
		return
	}

	// Append error information to buffered content
	ctx.Content.WriteString("\n=== ERROR ===\n")
	ctx.Content.WriteString(fmt.Sprintf("Timestamp: %s\n", time.Now().Format(time.RFC3339)))
	ctx.Content.WriteString(fmt.Sprintf("Duration: %s\n", time.Since(ctx.StartTime).Round(time.Millisecond)))
	ctx.Content.WriteString(fmt.Sprintf("Error Type: %T\n", err))
	ctx.Content.WriteString(fmt.Sprintf("Error Message: %v\n", err))

	// Add captured HTTP data - prefer per-request capture for isolation
	var reqBody, respBody []byte
	var status int
	var url string

	if ctx.RequestCapture != nil {
		// Use per-request capture (concurrency-safe)
		reqBody, respBody, status, url = ctx.RequestCapture.GetData()
		ctx.Content.WriteString("\n=== HTTP CAPTURE (per-request) ===\n")
	} else if transport != nil {
		// Fall back to shared transport capture (may be inaccurate with concurrent requests)
		reqBody, respBody, status, url = transport.GetLastCapture()
		ctx.Content.WriteString("\n=== HTTP CAPTURE (shared - may be inaccurate) ===\n")
	}

	if url != "" || status != 0 || len(reqBody) > 0 || len(respBody) > 0 {
		ctx.Content.WriteString(fmt.Sprintf("URL: %s\n", url))
		ctx.Content.WriteString(fmt.Sprintf("Status: %d\n", status))

		if len(reqBody) > 0 {
			ctx.Content.WriteString("\n--- Request Body ---\n")
			if len(reqBody) > 50000 {
				ctx.Content.Write(reqBody[:50000])
				ctx.Content.WriteString(fmt.Sprintf("\n... (truncated, total %d bytes)\n", len(reqBody)))
			} else {
				ctx.Content.Write(reqBody)
				ctx.Content.WriteString("\n")
			}
		}

		if len(respBody) > 0 {
			ctx.Content.WriteString("\n--- Response Body ---\n")
			if len(respBody) > 50000 {
				ctx.Content.Write(respBody[:50000])
				ctx.Content.WriteString(fmt.Sprintf("\n... (truncated, total %d bytes)\n", len(respBody)))
			} else {
				ctx.Content.Write(respBody)
				ctx.Content.WriteString("\n")
			}
		}
	}

	// Now write to disk
	dir, dirErr := dumpDir()
	if dirErr != nil {
		L_warn("dump: failed to create dump dir", "error", dirErr)
		return
	}

	now := time.Now()
	timestamp := now.Format("20060102-150405")
	millis := now.Nanosecond() / 1000000
	sanitizedModel := sanitizeFilename(ctx.Model)
	filename := fmt.Sprintf("%s_%s_%s_%03d_error.txt", ctx.Provider, sanitizedModel, timestamp, millis)
	path := filepath.Join(dir, filename)

	if writeErr := os.WriteFile(path, []byte(ctx.Content.String()), 0644); writeErr != nil {
		L_warn("dump: failed to write error dump", "path", path, "error", writeErr)
		return
	}

	L_info("dump: LLM error captured", "path", path, "source", fmt.Sprintf("%s:%d", ctx.CallerFile, ctx.CallerLine))

	// Cleanup old dumps
	cleanupDumps()
}

// FinishDumpSuccess handles successful requests.
// By default, does nothing (no file was created).
// If keepOnSuccess is true, writes the buffered content to disk with _success.txt suffix.
func FinishDumpSuccess(ctx *DumpContext, keepOnSuccess bool) {
	if ctx == nil {
		return
	}

	if !keepOnSuccess {
		// No-op: no file was created, nothing to clean up
		return
	}

	// Write success dump to disk
	dir, dirErr := dumpDir()
	if dirErr != nil {
		L_warn("dump: failed to create dump dir", "error", dirErr)
		return
	}

	// Append success marker to content
	ctx.Content.WriteString("\n=== SUCCESS ===\n")
	ctx.Content.WriteString(fmt.Sprintf("Timestamp: %s\n", time.Now().Format(time.RFC3339)))
	ctx.Content.WriteString(fmt.Sprintf("Duration: %s\n", time.Since(ctx.StartTime).Round(time.Millisecond)))

	now := time.Now()
	timestamp := now.Format("20060102-150405")
	millis := now.Nanosecond() / 1000000
	sanitizedModel := sanitizeFilename(ctx.Model)
	filename := fmt.Sprintf("%s_%s_%s_%03d_success.txt", ctx.Provider, sanitizedModel, timestamp, millis)
	path := filepath.Join(dir, filename)

	if writeErr := os.WriteFile(path, []byte(ctx.Content.String()), 0644); writeErr != nil {
		L_warn("dump: failed to write success dump", "path", path, "error", writeErr)
		return
	}

	L_debug("dump: success dump saved", "path", path)
	cleanupDumps()
}

// cleanupDumps keeps only the most recent maxDumpFiles files
func cleanupDumps() {
	dir, err := dumpDir()
	if err != nil {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	// Collect files with their mod times
	type fileInfo struct {
		name    string
		modTime time.Time
	}
	var files []fileInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileInfo{name: e.Name(), modTime: info.ModTime()})
	}

	if len(files) <= maxDumpFiles {
		return
	}

	// Sort by mod time, oldest first
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.Before(files[j].modTime)
	})

	// Delete oldest files
	toDelete := len(files) - maxDumpFiles
	for i := 0; i < toDelete; i++ {
		path := filepath.Join(dir, files[i].name)
		if err := os.Remove(path); err != nil {
			L_warn("dump: failed to cleanup old dump", "path", path, "error", err)
		} else {
			L_debug("dump: cleaned up old dump", "path", path)
		}
	}
}
