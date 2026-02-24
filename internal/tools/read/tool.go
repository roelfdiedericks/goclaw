package read

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/sandbox"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// Tool reads file contents
type Tool struct {
	workingDir    string
	workspaceRoot string
}

// NewTool creates a new read tool
// workingDir is used for both relative path resolution and as the sandbox boundary
func NewTool(workingDir string) *Tool {
	return &Tool{
		workingDir:    workingDir,
		workspaceRoot: workingDir,
	}
}

func (t *Tool) Name() string {
	return "read"
}

func (t *Tool) Description() string {
	return "Read the contents of a file. For text files, returns the file contents. For images (jpeg, png, gif, webp), returns {\"images\": [\"path\"]} and you can see the image in context - describe what you see and show it to the user with {{media:path}}."
}

func (t *Tool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The path to the file to read. Can be absolute or relative to working directory.",
			},
			"start_line": map[string]any{
				"type":        "integer",
				"description": "Optional: Start reading from this line number (1-indexed).",
			},
			"end_line": map[string]any{
				"type":        "integer",
				"description": "Optional: Stop reading at this line number (inclusive).",
			},
		},
		"required": []string{"path"},
	}
}

type readInput struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
}

func (t *Tool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var params readInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	// Check if user has sandbox disabled
	sandboxed := true
	if sessCtx := types.GetSessionContext(ctx); sessCtx != nil && sessCtx.User != nil {
		sandboxed = sessCtx.User.Sandbox
	}

	L_debug("read tool: reading file", "path", params.Path, "startLine", params.StartLine, "endLine", params.EndLine, "sandboxed", sandboxed)

	var content []byte
	var resolvedPath string
	var err error

	if sandboxed {
		// Validate path and read file (sandbox validation)
		content, err = sandbox.ReadFile(params.Path, t.workingDir, t.workspaceRoot)
		// Resolve path for image reference
		resolvedPath = params.Path
		if !filepath.IsAbs(resolvedPath) {
			resolvedPath = filepath.Join(t.workingDir, resolvedPath)
		}
	} else {
		// No sandbox: resolve relative paths from workingDir, allow any absolute path
		resolvedPath = params.Path
		if !filepath.IsAbs(resolvedPath) {
			resolvedPath = filepath.Join(t.workingDir, resolvedPath)
		}
		content, err = os.ReadFile(resolvedPath)
	}
	if err != nil {
		L_warn("read tool: failed", "path", params.Path, "error", err)
		return nil, err
	}

	// Detect MIME type from content (not extension)
	mimeType := media.DetectMIME(content)

	// If it's a supported image type, return as image reference with JSON
	if media.IsSupported(mimeType) {
		L_info("read tool: image detected", "path", params.Path, "mimeType", mimeType, "bytes", len(content))
		// Return JSON with images array for consistency with other image tools
		jsonResult, _ := json.Marshal(map[string]any{
			"images": []string{params.Path},
		})
		return types.ImageRefResult(resolvedPath, mimeType, string(jsonResult)), nil
	}

	// Otherwise treat as text
	text := string(content)
	totalLines := len(strings.Split(text, "\n"))

	// Handle line range if specified
	if params.StartLine > 0 || params.EndLine > 0 {
		lines := strings.Split(text, "\n")
		start := params.StartLine
		if start < 1 {
			start = 1
		}
		end := params.EndLine
		if end < 1 || end > len(lines) {
			end = len(lines)
		}
		if start > len(lines) {
			return nil, fmt.Errorf("start_line %d exceeds file length %d", start, len(lines))
		}
		// Convert to 0-indexed
		text = strings.Join(lines[start-1:end], "\n")
		L_info("read tool: file read", "path", params.Path, "lines", fmt.Sprintf("%d-%d/%d", start, end, totalLines), "bytes", len(text))
	} else {
		L_info("read tool: file read", "path", params.Path, "lines", totalLines, "bytes", len(text))
	}

	return types.TextResult(text), nil
}
