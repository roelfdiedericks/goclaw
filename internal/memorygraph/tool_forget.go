package memorygraph

import (
	"context"
	"encoding/json"
	"fmt"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// ForgetTool allows soft-deleting memories.
type ForgetTool struct {
	manager *Manager
}

// NewForgetTool creates a new forget tool with explicit manager reference.
func NewForgetTool(mgr *Manager) *ForgetTool {
	return &ForgetTool{manager: mgr}
}

func (t *ForgetTool) Name() string {
	return "memory_graph_forget"
}

func (t *ForgetTool) Description() string {
	return "Mark a memory as forgotten (soft delete). The memory is excluded from searches but retained for audit. Use to remove incorrect, outdated, or unwanted memories."
}

func (t *ForgetTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{
				"type":        "string",
				"description": "Memory UUID to forget (required)",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "Reason for forgetting this memory (logged for audit)",
			},
		},
		"required": []string{"id"},
	}
}

// ForgetParams defines input parameters for the forget tool.
type ForgetParams struct {
	ID     string `json:"id"`
	Reason string `json:"reason,omitempty"`
}

func (t *ForgetTool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var params ForgetParams
	if err := json.Unmarshal(input, &params); err != nil {
		return types.ErrorResult(fmt.Sprintf("invalid input: %v", err)), nil
	}

	if params.ID == "" {
		return types.ErrorResult("id is required"), nil
	}

	// Get username from context - required for privacy isolation
	username, err := getUsernameFromContext(ctx)
	if err != nil {
		return types.ErrorResult(err.Error()), nil
	}

	// Fetch existing memory to verify ownership and get details
	mem, err := t.manager.Store().GetMemory(params.ID)
	if err != nil {
		return types.ErrorResult(fmt.Sprintf("failed to fetch memory: %v", err)), nil
	}
	if mem == nil {
		return types.ErrorResult(fmt.Sprintf("memory not found: %s", params.ID)), nil
	}

	// Verify ownership
	if mem.Username != "" && mem.Username != username {
		L_warn("memory_graph_forget: access denied",
			"memoryUser", mem.Username,
			"requestUser", username,
			"memoryID", params.ID,
		)
		return types.ErrorResult("access denied: memory belongs to another user"), nil
	}

	// Check if already forgotten
	if mem.Forgotten {
		return types.TextResult(fmt.Sprintf("Memory %s is already forgotten", params.ID)), nil
	}

	L_info("memory_graph_forget: forgetting",
		"id", params.ID,
		"type", mem.Type,
		"content", truncateStr(mem.Content, 50),
		"reason", params.Reason,
	)

	// Soft delete
	if err := t.manager.Store().ForgetMemory(params.ID); err != nil {
		L_error("memory_graph_forget: failed", "error", err, "id", params.ID)
		return types.ErrorResult(fmt.Sprintf("forget failed: %v", err)), nil
	}

	L_info("memory_graph_forget: completed",
		"id", params.ID,
		"user", username,
	)

	result := fmt.Sprintf("Forgotten memory %s [%s]: %q", params.ID, mem.Type, truncateStr(mem.Content, 50))
	if params.Reason != "" {
		result += fmt.Sprintf("\nReason: %s", params.Reason)
	}

	return types.TextResult(result), nil
}
