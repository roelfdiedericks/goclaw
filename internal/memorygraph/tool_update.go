package memorygraph

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// UpdateTool allows modifying existing memories.
type UpdateTool struct {
	manager *Manager
}

// NewUpdateTool creates a new update tool with explicit manager reference.
func NewUpdateTool(mgr *Manager) *UpdateTool {
	return &UpdateTool{manager: mgr}
}

func (t *UpdateTool) Name() string {
	return "memory_graph_update"
}

func (t *UpdateTool) Description() string {
	return "Update an existing memory. Use to correct mistakes, adjust importance, modify content, or reschedule happens_at for a dated event, plan, or deadline. Requires the memory UUID."
}

func (t *UpdateTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{
				"type":        "string",
				"description": "Memory UUID to update (required)",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "New content (if changing)",
			},
			"importance": map[string]any{
				"type":        "number",
				"description": "New importance value 0.0-1.0 (if changing)",
			},
			"confidence": map[string]any{
				"type":        "number",
				"description": "New confidence value 0.0-1.0 (if changing)",
			},
			"emotion": map[string]any{
				"type":        "string",
				"description": "New emotional context (if changing)",
			},
			"memory_type": map[string]any{
				"type":        "string",
				"enum":        []string{"identity", "fact", "preference", "decision", "event", "observation", "goal", "todo", "routine", "feedback", "anomaly", "correlation", "prediction"},
				"description": "New memory type (if changing)",
			},
			"happens_at": map[string]any{
				"type":        "string",
				"description": "New scheduled time for an event, deadline, or plan. Format: ISO date or RFC3339. Pass an empty string to clear an existing schedule.",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "Reason for the update (logged for audit)",
			},
		},
		"required": []string{"id"},
	}
}

// UpdateParams defines input parameters for the update tool.
type UpdateParams struct {
	ID         string   `json:"id"`
	Content    *string  `json:"content,omitempty"`
	Importance *float32 `json:"importance,omitempty"`
	Confidence *float32 `json:"confidence,omitempty"`
	Emotion    *string  `json:"emotion,omitempty"`
	MemoryType *string  `json:"memory_type,omitempty"`
	HappensAt  *string  `json:"happens_at,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}

func (t *UpdateTool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var params UpdateParams
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

	// Fetch existing memory
	mem, err := t.manager.Store().GetMemory(params.ID)
	if err != nil {
		return types.ErrorResult(fmt.Sprintf("failed to fetch memory: %v", err)), nil
	}
	if mem == nil {
		return types.ErrorResult(fmt.Sprintf("memory not found: %s", params.ID)), nil
	}

	// Verify ownership
	if mem.Username != "" && mem.Username != username {
		L_warn("memory_graph_update: access denied",
			"memoryUser", mem.Username,
			"requestUser", username,
			"memoryID", params.ID,
		)
		return types.ErrorResult("access denied: memory belongs to another user"), nil
	}

	// Track what changed
	var changes []string
	contentChanged := false

	if params.Content != nil && *params.Content != mem.Content {
		changes = append(changes, fmt.Sprintf("content: %q → %q", truncateStr(mem.Content, 30), truncateStr(*params.Content, 30)))
		mem.Content = *params.Content
		contentChanged = true
	}
	if params.Importance != nil && *params.Importance != mem.Importance {
		changes = append(changes, fmt.Sprintf("importance: %.2f → %.2f", mem.Importance, *params.Importance))
		mem.Importance = *params.Importance
	}
	if params.Confidence != nil && *params.Confidence != mem.Confidence {
		changes = append(changes, fmt.Sprintf("confidence: %.2f → %.2f", mem.Confidence, *params.Confidence))
		mem.Confidence = *params.Confidence
	}
	if params.Emotion != nil && *params.Emotion != mem.Emotion {
		changes = append(changes, fmt.Sprintf("emotion: %q → %q", mem.Emotion, *params.Emotion))
		mem.Emotion = *params.Emotion
	}
	if params.MemoryType != nil && Type(*params.MemoryType) != mem.Type {
		changes = append(changes, fmt.Sprintf("type: %s → %s", mem.Type, *params.MemoryType))
		mem.Type = Type(*params.MemoryType)
	}
	if params.HappensAt != nil {
		switch {
		case *params.HappensAt == "" && mem.HappensAt != nil:
			changes = append(changes, fmt.Sprintf("happens_at: %s → cleared", mem.HappensAt.Format(time.RFC3339)))
			mem.HappensAt = nil
		case *params.HappensAt != "":
			happensAt, parseErr := parseMemoryToolTime(*params.HappensAt)
			if parseErr != nil {
				return types.ErrorResult(fmt.Sprintf("invalid happens_at: %v", parseErr)), nil
			}
			if mem.HappensAt == nil || !mem.HappensAt.Equal(happensAt) {
				before := "unset"
				if mem.HappensAt != nil {
					before = mem.HappensAt.Format(time.RFC3339)
				}
				changes = append(changes, fmt.Sprintf("happens_at: %s → %s", before, happensAt.Format(time.RFC3339)))
				mem.HappensAt = &happensAt
			}
		}
	}

	if len(changes) == 0 {
		return types.TextResult(fmt.Sprintf("No changes to memory %s", params.ID)), nil
	}

	L_info("memory_graph_update: updating",
		"id", params.ID,
		"changes", len(changes),
		"reason", params.Reason,
	)

	// Update the memory (regenerate embedding if content changed)
	if err := t.manager.UpdateMemory(ctx, mem, contentChanged); err != nil {
		L_error("memory_graph_update: failed", "error", err, "id", params.ID)
		return types.ErrorResult(fmt.Sprintf("update failed: %v", err)), nil
	}

	L_info("memory_graph_update: completed",
		"id", params.ID,
		"user", username,
		"contentChanged", contentChanged,
	)

	// Invalidate the appropriate bulletin cache based on memory type
	if username != "" {
		if isContextBulletinType(mem.Type) {
			t.manager.InvalidateContextBulletinCache(username)
		}
		if !isContextBulletinType(mem.Type) || mem.HappensAt != nil {
			t.manager.InvalidateMemoryBulletinCache(username)
		}
	}

	result := fmt.Sprintf("Updated memory %s:\n", params.ID)
	for _, c := range changes {
		result += fmt.Sprintf("  • %s\n", c)
	}
	if params.Reason != "" {
		result += fmt.Sprintf("Reason: %s", params.Reason)
	}

	return types.TextResult(result), nil
}
