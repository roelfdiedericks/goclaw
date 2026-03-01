package memorygraph

import (
	"context"
	"encoding/json"
	"fmt"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// StoreTool saves memories with optional associations to the memory graph.
type StoreTool struct {
	manager *Manager
}

// NewStoreTool creates a new store tool with explicit manager reference.
func NewStoreTool(mgr *Manager) *StoreTool {
	return &StoreTool{manager: mgr}
}

func (t *StoreTool) Name() string {
	return "memory_graph_store"
}

func (t *StoreTool) Description() string {
	return "Save a memory to the graph. Use after recalling related memories. Supports linking to existing memories via associations."
}

func (t *StoreTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"content": map[string]any{
				"type":        "string",
				"description": "The memory content - clear, standalone statement",
			},
			"memory_type": map[string]any{
				"type":        "string",
				"enum":        []string{"identity", "fact", "preference", "decision", "event", "observation", "goal", "todo", "routine", "feedback", "anomaly", "correlation", "prediction"},
				"description": "Type of memory",
			},
			"importance": map[string]any{
				"type":        "number",
				"description": "Importance 0.0-1.0. Uses type default if omitted",
			},
			"confidence": map[string]any{
				"type":        "number",
				"description": "Confidence 0.0-1.0. For pattern types (routine, correlation, prediction)",
			},
			"emotion": map[string]any{
				"type":        "string",
				"description": "Emotional context: frustrated, relieved, stressed, excited, etc.",
			},
			"source": map[string]any{
				"type":        "string",
				"description": "Source: 'user stated', 'inferred', 'observed'",
			},
			"associations": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"target_id": map[string]any{
							"type":        "string",
							"description": "UUID of memory from recall",
						},
						"relation_type": map[string]any{
							"type":        "string",
							"enum":        []string{"updates", "contradicts", "related_to", "part_of", "caused_by", "result_of", "triggers", "reinforces", "weakens", "violated", "predicts", "confirmed", "overrides"},
							"description": "How this memory relates to the target",
						},
					},
					"required": []string{"target_id", "relation_type"},
				},
				"description": "Links to existing memories",
			},
		},
		"required": []string{"content", "memory_type"},
	}
}

// StoreParams defines input parameters for the store tool.
type StoreParams struct {
	Content      string             `json:"content"`
	MemoryType   string             `json:"memory_type"`
	Importance   *float32           `json:"importance,omitempty"`
	Confidence   *float32           `json:"confidence,omitempty"`
	Emotion      string             `json:"emotion,omitempty"`
	Source       string             `json:"source,omitempty"`
	Associations []AssociationInput `json:"associations,omitempty"`
}

// AssociationInput represents an association to create.
type AssociationInput struct {
	TargetID     string `json:"target_id"`
	RelationType string `json:"relation_type"`
}

func (t *StoreTool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var params StoreParams
	if err := json.Unmarshal(input, &params); err != nil {
		return types.ErrorResult(fmt.Sprintf("invalid input: %v", err)), nil
	}

	// Validate required fields
	if params.Content == "" {
		return types.ErrorResult("content is required"), nil
	}
	if params.MemoryType == "" {
		return types.ErrorResult("memory_type is required"), nil
	}

	// Get username from context if available
	username := ""
	if u, ok := ctx.Value(ContextKeyUsername).(string); ok {
		username = u
	}

	L_debug("memory_graph_store: executing",
		"type", params.MemoryType,
		"contentLen", len(params.Content),
		"associations", len(params.Associations),
		"username", username,
	)

	// Build memory
	mem := &Memory{
		Content:  params.Content,
		Type:     Type(params.MemoryType),
		Username: username,
		Source:   params.Source,
		Emotion:  params.Emotion,
	}

	// Set importance (use default if not provided)
	if params.Importance != nil {
		mem.Importance = *params.Importance
	}

	// Set confidence
	if params.Confidence != nil {
		mem.Confidence = *params.Confidence
	} else {
		mem.Confidence = ConfidenceNotApplicable
	}

	// Default source
	if mem.Source == "" {
		mem.Source = "extraction"
	}

	// Create the memory
	if err := t.manager.CreateMemory(ctx, mem); err != nil {
		L_error("memory_graph_store: create failed", "error", err)
		return types.ErrorResult(fmt.Sprintf("failed to save: %v", err)), nil
	}

	// Create associations
	associationsCreated := 0
	for _, assocInput := range params.Associations {
		if assocInput.TargetID == "" || assocInput.RelationType == "" {
			continue
		}

		assoc := &Association{
			SourceID:     mem.UUID,
			TargetID:     assocInput.TargetID,
			RelationType: RelationType(assocInput.RelationType),
		}

		if err := t.manager.CreateAssociation(assoc); err != nil {
			L_warn("memory_graph_store: association failed",
				"target", assocInput.TargetID,
				"type", assocInput.RelationType,
				"error", err,
			)
		} else {
			associationsCreated++
			L_debug("memory_graph_store: association created",
				"source", mem.UUID,
				"target", assocInput.TargetID,
				"type", assocInput.RelationType,
			)
		}
	}

	L_info("memory_graph_store: created",
		"id", mem.UUID,
		"type", params.MemoryType,
		"associations", associationsCreated,
	)

	// Return the new memory's UUID
	result := fmt.Sprintf("Saved memory %s [%s] %q", mem.UUID, params.MemoryType, truncateStr(params.Content, 50))
	if associationsCreated > 0 {
		result += fmt.Sprintf(" with %d association(s)", associationsCreated)
	}

	return types.TextResult(result), nil
}
