package tools

import (
	"context"
	"encoding/json"
	"fmt"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/sandbox"
)

// WriteTool writes content to files
type WriteTool struct {
	workingDir    string
	workspaceRoot string
}

// NewWriteTool creates a new write tool
// workingDir is used for both relative path resolution and as the sandbox boundary
func NewWriteTool(workingDir string) *WriteTool {
	return &WriteTool{
		workingDir:    workingDir,
		workspaceRoot: workingDir, // Sandbox boundary is the workspace
	}
}

func (t *WriteTool) Name() string {
	return "write"
}

func (t *WriteTool) Description() string {
	return "Write content to a file. Creates the file if it doesn't exist, or overwrites if it does. Creates parent directories as needed."
}

func (t *WriteTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The path to the file to write. Can be absolute or relative to working directory.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The content to write to the file.",
			},
		},
		"required": []string{"path", "content"},
	}
}

type writeInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (t *WriteTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params writeInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if params.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	L_debug("write tool: writing file", "path", params.Path, "bytes", len(params.Content))

	// Validate path and write atomically (sandbox validation + atomic write)
	if err := sandbox.WriteFileValidated(params.Path, t.workingDir, t.workspaceRoot, []byte(params.Content), 0644); err != nil {
		L_error("write tool failed", "path", params.Path, "error", err)
		return "", err
	}

	L_info("write tool: file written", "path", params.Path, "bytes", len(params.Content))
	return fmt.Sprintf("Successfully wrote %d bytes to %s", len(params.Content), params.Path), nil
}
