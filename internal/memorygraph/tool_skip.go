package memorygraph

import (
	"context"
	"encoding/json"
	"fmt"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// SkipTool explicitly logs when the LLM decides not to store something.
// This provides visibility into skip decisions for debugging.
type SkipTool struct{}

// NewSkipTool creates a new skip tool.
func NewSkipTool() *SkipTool {
	return &SkipTool{}
}

func (t *SkipTool) Name() string {
	return "memory_graph_skip"
}

func (t *SkipTool) Description() string {
	return "Explicitly skip storing something with explanation. Use to document why information is not worth persisting."
}

func (t *SkipTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"content": map[string]any{
				"type":        "string",
				"description": "Brief description of what was considered for storage",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "Why it's not worth storing (e.g., 'transient', 'already exists', 'not about user')",
			},
		},
		"required": []string{"content", "reason"},
	}
}

// SkipParams defines input parameters for the skip tool.
type SkipParams struct {
	Content string `json:"content"`
	Reason  string `json:"reason"`
}

func (t *SkipTool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var params SkipParams
	if err := json.Unmarshal(input, &params); err != nil {
		return types.ErrorResult(fmt.Sprintf("invalid input: %v", err)), nil
	}

	// Validate required fields
	if params.Content == "" {
		return types.ErrorResult("content is required"), nil
	}
	if params.Reason == "" {
		return types.ErrorResult("reason is required"), nil
	}

	// Log the skip decision for visibility
	L_info("memory_graph_skip: skipped",
		"content", truncateStr(params.Content, 80),
		"reason", params.Reason,
	)

	return types.TextResult(fmt.Sprintf("Skipped: %s (reason: %s)", truncateStr(params.Content, 50), params.Reason)), nil
}
