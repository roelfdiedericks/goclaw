package edit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/sandbox"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// Tool performs surgical text replacement in files
type Tool struct {
	workingDir    string
	workspaceRoot string
}

// NewTool creates a new edit tool
// workingDir is used for both relative path resolution and as the sandbox boundary
func NewTool(workingDir string) *Tool {
	return &Tool{
		workingDir:    workingDir,
		workspaceRoot: workingDir,
	}
}

func (t *Tool) Name() string {
	return "edit"
}

func (t *Tool) Description() string {
	return "Replace text in a file. Finds the exact old_string and replaces it with new_string. The old_string must be unique in the file."
}

func (t *Tool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The path to the file to edit. Can be absolute or relative to working directory.",
			},
			"old_string": map[string]any{
				"type":        "string",
				"description": "The exact text to find and replace. Must be unique in the file.",
			},
			"new_string": map[string]any{
				"type":        "string",
				"description": "The text to replace old_string with.",
			},
		},
		"required": []string{"path", "old_string", "new_string"},
	}
}

type editInput struct {
	Path      string `json:"path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

func (t *Tool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var params editInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	// Validate path is not empty
	if params.Path == "" {
		return nil, fmt.Errorf("path is required")
	}

	// Validate old_string is not empty
	if params.OldString == "" {
		L_warn("edit tool: empty old_string")
		return nil, fmt.Errorf("old_string cannot be empty")
	}

	// Check if user has sandbox disabled
	sandboxed := true
	if sessCtx := types.GetSessionContext(ctx); sessCtx != nil && sessCtx.User != nil {
		sandboxed = sessCtx.User.Sandbox
	}

	L_debug("edit tool: editing file", "path", params.Path, "oldLen", len(params.OldString), "newLen", len(params.NewString), "sandboxed", sandboxed)

	var resolved string
	var content []byte
	var err error

	if sandboxed {
		// Validate path (sandbox check)
		resolved, err = sandbox.ValidatePath(params.Path, t.workingDir, t.workspaceRoot)
		if err != nil {
			L_warn("edit tool: path validation failed", "path", params.Path, "error", err)
			return nil, err
		}

		// Read file using sandbox-validated path
		content, err = sandbox.ReadFile(params.Path, t.workingDir, t.workspaceRoot)
	} else {
		// No sandbox: resolve relative paths from workingDir, allow any absolute path
		resolved = params.Path
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(t.workingDir, resolved)
		}
		content, err = os.ReadFile(resolved)
	}
	if err != nil {
		L_warn("edit tool: failed to read", "path", params.Path, "error", err)
		return nil, err
	}

	text := string(content)

	// Check that old_string exists and is unique
	count := strings.Count(text, params.OldString)
	if count == 0 {
		L_warn("edit tool: old_string not found", "path", params.Path)
		return nil, fmt.Errorf("old_string not found in file")
	}
	if count > 1 {
		L_warn("edit tool: old_string not unique", "path", params.Path, "occurrences", count)
		return nil, fmt.Errorf("old_string is not unique (found %d occurrences). Please provide more context to make it unique", count)
	}

	// Perform replacement
	newText := strings.Replace(text, params.OldString, params.NewString, 1)

	// Write back atomically (preserves permissions)
	if sandboxed {
		err = sandbox.AtomicWriteFile(resolved, []byte(newText), 0600)
	} else {
		err = os.WriteFile(resolved, []byte(newText), 0600)
	}
	if err != nil {
		L_error("edit tool: failed to write", "path", params.Path, "error", err)
		return nil, err
	}

	L_info("edit tool: file edited", "path", params.Path, "sizeBefore", len(text), "sizeAfter", len(newText))
	return types.TextResult(fmt.Sprintf("Successfully edited %s", params.Path)), nil
}
