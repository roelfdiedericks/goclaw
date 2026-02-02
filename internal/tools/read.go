package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/sandbox"
)

// ReadTool reads file contents
type ReadTool struct {
	workingDir    string
	workspaceRoot string
}

// NewReadTool creates a new read tool
// workingDir is used for both relative path resolution and as the sandbox boundary
func NewReadTool(workingDir string) *ReadTool {
	return &ReadTool{
		workingDir:    workingDir,
		workspaceRoot: workingDir, // Sandbox boundary is the workspace
	}
}

func (t *ReadTool) Name() string {
	return "read"
}

func (t *ReadTool) Description() string {
	return "Read the contents of a file. Returns the file contents as text."
}

func (t *ReadTool) Schema() map[string]any {
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

func (t *ReadTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params readInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	L_debug("read tool: reading file", "path", params.Path, "startLine", params.StartLine, "endLine", params.EndLine)

	// Validate path and read file (sandbox validation)
	content, err := sandbox.ReadFile(params.Path, t.workingDir, t.workspaceRoot)
	if err != nil {
		L_warn("read tool: failed", "path", params.Path, "error", err)
		return "", err
	}

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
			return "", fmt.Errorf("start_line %d exceeds file length %d", start, len(lines))
		}
		// Convert to 0-indexed
		text = strings.Join(lines[start-1:end], "\n")
		L_info("read tool: file read", "path", params.Path, "lines", fmt.Sprintf("%d-%d/%d", start, end, totalLines), "bytes", len(text))
	} else {
		L_info("read tool: file read", "path", params.Path, "lines", totalLines, "bytes", len(text))
	}

	return text, nil
}
