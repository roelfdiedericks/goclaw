package memoryget

import (
	"context"
	"encoding/json"
	"fmt"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/memory"
)

// Tool reads memory file content
type Tool struct {
	manager *memory.Manager
}

// NewTool creates a new memory get tool
func NewTool(manager *memory.Manager) *Tool {
	return &Tool{manager: manager}
}

func (t *Tool) Name() string {
	return "memory_get"
}

func (t *Tool) Description() string {
	return "Read content from MEMORY.md, memory/*.md, or configured extra paths. Use after memory_search to read full context around a match. Paths are workspace-relative."
}

func (t *Tool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the memory file (e.g., 'MEMORY.md', 'memory/2026-02-01.md'). Must be within allowed memory paths.",
			},
			"from": map[string]any{
				"type":        "integer",
				"description": "Optional: Start reading from this line number (1-indexed).",
			},
			"lines": map[string]any{
				"type":        "integer",
				"description": "Optional: Number of lines to read. Omit to read the entire file.",
			},
		},
		"required": []string{"path"},
	}
}

type memoryGetInput struct {
	Path  string `json:"path"`
	From  int    `json:"from,omitempty"`
	Lines int    `json:"lines,omitempty"`
}

type memoryGetOutput struct {
	Path  string `json:"path"`
	Text  string `json:"text"`
	Error string `json:"error,omitempty"`
}

func (t *Tool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params memoryGetInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if params.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	L_debug("memory_get: executing", "path", params.Path, "from", params.From, "lines", params.Lines)

	if t.manager == nil {
		L_warn("memory_get: manager not available")
		output := memoryGetOutput{
			Path:  params.Path,
			Error: "memory search is not enabled",
		}
		return marshalOutput(output)
	}

	text, err := t.manager.ReadFile(params.Path, params.From, params.Lines)
	if err != nil {
		L_warn("memory_get: read failed", "path", params.Path, "error", err)
		output := memoryGetOutput{
			Path:  params.Path,
			Error: err.Error(),
		}
		return marshalOutput(output)
	}

	L_info("memory_get: completed", "path", params.Path, "bytes", len(text))

	output := memoryGetOutput{
		Path: params.Path,
		Text: text,
	}

	return marshalOutput(output)
}

func marshalOutput(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
