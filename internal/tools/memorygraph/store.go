package memorygraph

import (
	"context"
	"encoding/json"
	"fmt"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	mgraph "github.com/roelfdiedericks/goclaw/internal/memorygraph"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// StoreTool stores memories and associations in the graph
type StoreTool struct{}

// NewStoreTool creates a new memory graph store tool
func NewStoreTool() *StoreTool {
	return &StoreTool{}
}

func (t *StoreTool) Name() string {
	return "memory_graph_store"
}

func (t *StoreTool) Description() string {
	return "Store a new memory or update an existing one in the structured memory graph. Use to record facts, preferences, decisions, events, goals, routines, or other persistent information."
}

func (t *StoreTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"content": map[string]any{
				"type":        "string",
				"description": "The memory content. Should be a clear, standalone statement.",
			},
			"type": map[string]any{
				"type":        "string",
				"enum":        []string{"identity", "fact", "preference", "decision", "event", "observation", "goal", "todo", "routine", "feedback", "anomaly", "correlation", "prediction"},
				"description": "Type of memory: identity (who the user is), fact, preference, decision, event, observation, goal, todo, routine (pattern), feedback, anomaly, correlation, prediction",
			},
			"importance": map[string]any{
				"type":        "number",
				"description": "Importance from 0.0 to 1.0. Higher = more important to remember. Default varies by type.",
			},
			"confidence": map[string]any{
				"type":        "number",
				"description": "For pattern types (routine, correlation, prediction): confidence level 0.0-1.0. Default: -1 (not applicable).",
			},
			"related_to": map[string]any{
				"type":        "string",
				"description": "Optional UUID of a related memory to create an association.",
			},
			"relation_type": map[string]any{
				"type":        "string",
				"enum":        []string{"related_to", "updates", "contradicts", "caused_by", "result_of", "part_of", "triggers", "reinforces", "weakens", "violated", "predicts", "confirmed", "overrides"},
				"description": "Type of relation to the related memory. Default: related_to",
			},
			"update_id": map[string]any{
				"type":        "string",
				"description": "If updating an existing memory, provide its UUID here. Content and other fields will be updated.",
			},
		},
		"required": []string{"content", "type"},
	}
}

type storeInput struct {
	Content      string  `json:"content"`
	Type         string  `json:"type"`
	Importance   float32 `json:"importance,omitempty"`
	Confidence   float32 `json:"confidence,omitempty"`
	RelatedTo    string  `json:"related_to,omitempty"`
	RelationType string  `json:"relation_type,omitempty"`
	UpdateID     string  `json:"update_id,omitempty"`
}

type storeOutput struct {
	ID            string `json:"id"`
	AssociationID string `json:"association_id,omitempty"`
	Updated       bool   `json:"updated,omitempty"`
	Error         string `json:"error,omitempty"`
}

func (t *StoreTool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var params storeInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	if params.Content == "" {
		return nil, fmt.Errorf("content is required")
	}
	if params.Type == "" {
		return nil, fmt.Errorf("type is required")
	}

	manager := mgraph.GetManager()
	if manager == nil {
		output := storeOutput{
			Error: "memory graph is not enabled",
		}
		return marshalOutput(output)
	}

	L_debug("memory_graph_store: executing",
		"type", params.Type,
		"contentLen", len(params.Content),
		"updateID", params.UpdateID,
	)

	output := storeOutput{}

	// Check if updating existing memory
	if params.UpdateID != "" {
		existing, err := manager.GetMemory(params.UpdateID)
		if err != nil || existing == nil {
			output.Error = fmt.Sprintf("memory not found: %s", params.UpdateID)
			return marshalOutput(output)
		}

		// Update fields
		existing.Content = params.Content
		existing.Type = mgraph.Type(params.Type)
		if params.Importance > 0 {
			existing.Importance = params.Importance
		}
		if params.Confidence != 0 {
			existing.Confidence = params.Confidence
		}

		if err := manager.UpdateMemory(ctx, existing, true); err != nil {
			L_error("memory_graph_store: update failed", "error", err)
			output.Error = err.Error()
			return marshalOutput(output)
		}

		output.ID = existing.UUID
		output.Updated = true
		L_info("memory_graph_store: updated", "id", existing.UUID, "type", params.Type)
	} else {
		// Create new memory
		mem := &mgraph.Memory{
			Content:    params.Content,
			Type:       mgraph.Type(params.Type),
			Importance: params.Importance,
			Confidence: params.Confidence,
			Source:     "agent",
		}

		// Set default confidence if not provided
		if mem.Confidence == 0 {
			mem.Confidence = mgraph.ConfidenceNotApplicable
		}

		if err := manager.CreateMemory(ctx, mem); err != nil {
			L_error("memory_graph_store: create failed", "error", err)
			output.Error = err.Error()
			return marshalOutput(output)
		}

		output.ID = mem.UUID
		L_info("memory_graph_store: created", "id", mem.UUID, "type", params.Type)

		// Create association if requested
		if params.RelatedTo != "" {
			relType := mgraph.RelationType(params.RelationType)
			if relType == "" {
				relType = mgraph.RelationRelatedTo
			}

			assoc := &mgraph.Association{
				SourceID:     mem.UUID,
				TargetID:     params.RelatedTo,
				RelationType: relType,
			}

			if err := manager.CreateAssociation(assoc); err != nil {
				L_warn("memory_graph_store: association failed", "error", err)
			} else {
				output.AssociationID = assoc.ID
				L_debug("memory_graph_store: association created",
					"id", assoc.ID,
					"source", mem.UUID,
					"target", params.RelatedTo,
					"type", relType,
				)
			}
		}
	}

	return marshalOutput(output)
}
