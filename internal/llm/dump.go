package llm

import (
	"bytes"
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
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

const (
	dumpDirName   = "llm_dumps"
	maxDumpFiles  = 20
)

// CapturingTransport is an http.RoundTripper that captures request/response bodies
// for debugging purposes. Thread-safe.
type CapturingTransport struct {
	Base http.RoundTripper

	mu           sync.RWMutex
	lastRequest  []byte
	lastResponse []byte
	lastStatus   int
	lastURL      string
}

// RoundTrip implements http.RoundTripper, capturing request and response bodies
func (t *CapturingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Capture request body
	var reqBody []byte
	if req.Body != nil {
		reqBody, _ = io.ReadAll(req.Body)
		req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}

	t.mu.Lock()
	t.lastRequest = reqBody
	t.lastURL = req.URL.String()
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

	// Capture response body (re-wrap so caller can still read it)
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(respBody))

	t.mu.Lock()
	t.lastResponse = respBody
	t.lastStatus = resp.StatusCode
	t.mu.Unlock()

	return resp, nil
}

// GetLastCapture returns the last captured request/response data
func (t *CapturingTransport) GetLastCapture() (reqBody, respBody []byte, status int, url string) {
	t.mu.RLock()
	defer t.mu.RUnlock()
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

// DumpContext holds the context for a dump operation
type DumpContext struct {
	Path         string
	Provider     string
	Model        string
	BaseURL      string
	Messages     interface{} // Provider-specific message format
	Tools        interface{} // Provider-specific tool format
	SystemPrompt string
	CallerFile   string
	CallerLine   int
	StartTime    time.Time
}

// StartDump creates a dump file with request context before an API call.
// Returns a DumpContext that should be passed to FinishDump* functions.
// callerSkip is the number of stack frames to skip (use 1 for direct callers).
func StartDump(provider, model, baseURL string, messages, tools interface{}, systemPrompt string, callerSkip int) *DumpContext {
	dir, err := dumpDir()
	if err != nil {
		L_warn("dump: failed to create dump dir", "error", err)
		return nil
	}

	// Get caller info
	_, file, line, ok := runtime.Caller(callerSkip)
	if !ok {
		file = "unknown"
		line = 0
	} else {
		// Shorten to just filename
		file = filepath.Base(file)
	}

	timestamp := time.Now().Format("20060102-150405")
	sanitizedModel := sanitizeFilename(model)
	filename := fmt.Sprintf("%s_%s_%s.txt", provider, sanitizedModel, timestamp)
	path := filepath.Join(dir, filename)

	ctx := &DumpContext{
		Path:         path,
		Provider:     provider,
		Model:        model,
		BaseURL:      baseURL,
		Messages:     messages,
		Tools:        tools,
		SystemPrompt: systemPrompt,
		CallerFile:   file,
		CallerLine:   line,
		StartTime:    time.Now(),
	}

	// Write initial request context
	var sb strings.Builder
	sb.WriteString("=== LLM REQUEST DUMP ===\n")
	sb.WriteString(fmt.Sprintf("Timestamp: %s\n", ctx.StartTime.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("Source: %s:%d\n", file, line))
	sb.WriteString(fmt.Sprintf("Provider: %s\n", provider))
	sb.WriteString(fmt.Sprintf("Model: %s\n", model))
	sb.WriteString(fmt.Sprintf("BaseURL: %s\n", baseURL))
	sb.WriteString("\n")

	// System prompt
	if systemPrompt != "" {
		sb.WriteString("=== SYSTEM PROMPT ===\n")
		if len(systemPrompt) > 2000 {
			sb.WriteString(systemPrompt[:2000])
			sb.WriteString(fmt.Sprintf("\n... (truncated, total %d chars)\n", len(systemPrompt)))
		} else {
			sb.WriteString(systemPrompt)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	// Messages (JSON format for clarity)
	if messages != nil {
		sb.WriteString("=== MESSAGES ===\n")
		msgJSON, err := json.MarshalIndent(messages, "", "  ")
		if err != nil {
			sb.WriteString(fmt.Sprintf("(failed to marshal: %v)\n", err))
		} else {
			// Truncate if too large
			if len(msgJSON) > 50000 {
				sb.Write(msgJSON[:50000])
				sb.WriteString(fmt.Sprintf("\n... (truncated, total %d bytes)\n", len(msgJSON)))
			} else {
				sb.Write(msgJSON)
				sb.WriteString("\n")
			}
		}
		sb.WriteString("\n")
	}

	// Tools
	if tools != nil {
		sb.WriteString("=== TOOLS ===\n")
		toolJSON, err := json.MarshalIndent(tools, "", "  ")
		if err != nil {
			sb.WriteString(fmt.Sprintf("(failed to marshal: %v)\n", err))
		} else {
			if len(toolJSON) > 20000 {
				sb.Write(toolJSON[:20000])
				sb.WriteString(fmt.Sprintf("\n... (truncated, total %d bytes)\n", len(toolJSON)))
			} else {
				sb.Write(toolJSON)
				sb.WriteString("\n")
			}
		}
		sb.WriteString("\n")
	}

	if err := os.WriteFile(path, []byte(sb.String()), 0644); err != nil {
		L_warn("dump: failed to write request dump", "path", path, "error", err)
		return nil
	}

	L_debug("dump: request context saved", "path", path)
	return ctx
}

// FinishDumpError appends error information to the dump file and renames it to _error.txt
func FinishDumpError(ctx *DumpContext, err error, transport *CapturingTransport) {
	if ctx == nil {
		return
	}

	var sb strings.Builder
	sb.WriteString("\n=== ERROR ===\n")
	sb.WriteString(fmt.Sprintf("Timestamp: %s\n", time.Now().Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("Duration: %s\n", time.Since(ctx.StartTime).Round(time.Millisecond)))
	sb.WriteString(fmt.Sprintf("Error Type: %T\n", err))
	sb.WriteString(fmt.Sprintf("Error Message: %v\n", err))

	// Add captured HTTP data if available
	if transport != nil {
		reqBody, respBody, status, url := transport.GetLastCapture()
		sb.WriteString("\n=== HTTP CAPTURE ===\n")
		sb.WriteString(fmt.Sprintf("URL: %s\n", url))
		sb.WriteString(fmt.Sprintf("Status: %d\n", status))
		
		if len(reqBody) > 0 {
			sb.WriteString("\n--- Request Body ---\n")
			if len(reqBody) > 50000 {
				sb.Write(reqBody[:50000])
				sb.WriteString(fmt.Sprintf("\n... (truncated, total %d bytes)\n", len(reqBody)))
			} else {
				sb.Write(reqBody)
				sb.WriteString("\n")
			}
		}
		
		if len(respBody) > 0 {
			sb.WriteString("\n--- Response Body ---\n")
			if len(respBody) > 50000 {
				sb.Write(respBody[:50000])
				sb.WriteString(fmt.Sprintf("\n... (truncated, total %d bytes)\n", len(respBody)))
			} else {
				sb.Write(respBody)
				sb.WriteString("\n")
			}
		}
	}

	// Append to existing file
	f, fileErr := os.OpenFile(ctx.Path, os.O_APPEND|os.O_WRONLY, 0644)
	if fileErr != nil {
		L_warn("dump: failed to open dump file for append", "path", ctx.Path, "error", fileErr)
		return
	}
	f.WriteString(sb.String())
	f.Close()

	// Rename to _error.txt
	errorPath := strings.TrimSuffix(ctx.Path, ".txt") + "_error.txt"
	if renameErr := os.Rename(ctx.Path, errorPath); renameErr != nil {
		L_warn("dump: failed to rename to error file", "from", ctx.Path, "to", errorPath, "error", renameErr)
		errorPath = ctx.Path // Use original path in log
	}

	L_info("dump: LLM error captured", "path", errorPath, "source", fmt.Sprintf("%s:%d", ctx.CallerFile, ctx.CallerLine))

	// Cleanup old dumps
	cleanupDumps()
}

// FinishDumpSuccess handles successful requests - deletes dump or renames to _success.txt
func FinishDumpSuccess(ctx *DumpContext, keepOnSuccess bool) {
	if ctx == nil {
		return
	}

	if keepOnSuccess {
		// Rename to _success.txt
		successPath := strings.TrimSuffix(ctx.Path, ".txt") + "_success.txt"
		if err := os.Rename(ctx.Path, successPath); err != nil {
			L_warn("dump: failed to rename to success file", "from", ctx.Path, "to", successPath, "error", err)
		} else {
			L_debug("dump: success dump saved", "path", successPath)
		}
		cleanupDumps()
	} else {
		// Delete the dump file
		if err := os.Remove(ctx.Path); err != nil && !os.IsNotExist(err) {
			L_warn("dump: failed to delete dump file", "path", ctx.Path, "error", err)
		}
	}
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
