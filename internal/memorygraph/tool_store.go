package memorygraph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	return "Save a memory to the graph. Use after recalling related memories. Supports linking to existing memories via associations. Note: Todos appear automatically in the Context Bulletin - no scheduling needed."
}

func (t *StoreTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"content": map[string]any{
				"type":        "string",
				"description": "The memory content - clear, standalone statement. Include a target date for todo/reminders/events/etc.",
			},
			"memory_type": map[string]any{
				"type":        "string",
				"enum":        []string{"identity", "fact", "preference", "decision", "event", "observation", "goal", "todo", "routine", "feedback", "anomaly", "correlation", "prediction"},
				"description": "Type of memory",
			},
			"importance": map[string]any{
				"type":        "number",
				"description": "0.0-1.0. Usually omit - system assigns sensible defaults based on memory_type. Only set if explicitly very important (0.9+) or trivial (0.2-).",
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
			"occurred_at": map[string]any{
				"type":        "string",
				"description": "When this memory was formed. Cannot be in the future. For past events ('yesterday', 'last week'), calculate using conversation date as reference. For todos with target dates, put the date in content instead. Format: ISO date or RFC3339. Usually omit - defaults to conversation timestamp.",
			},
			"reasoning": map[string]any{
				"type":        "string",
				"description": "Brief explanation of why this memory is worth storing (required for debugging)",
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
		"required": []string{"content", "memory_type", "reasoning"},
	}
}

// StoreParams defines input parameters for the store tool.
type StoreParams struct {
	Content      string             `json:"content"`
	MemoryType   string             `json:"memory_type"`
	Reasoning    string             `json:"reasoning"`
	Importance   *float32           `json:"importance,omitempty"`
	Confidence   *float32           `json:"confidence,omitempty"`
	Emotion      string             `json:"emotion,omitempty"`
	Source       string             `json:"source,omitempty"`
	OccurredAt   string             `json:"occurred_at,omitempty"`
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

	// Get username from context - required for privacy isolation
	username, err := getUsernameFromContext(ctx)
	if err != nil {
		return types.ErrorResult(err.Error()), nil
	}

	// Get additional context from extraction loop or gateway session
	var messageIDs []string
	var sessionKey, channel string

	// First try extraction loop context keys
	if ids, ok := ctx.Value(ContextKeyMessageIDs).([]string); ok {
		messageIDs = ids
	}
	if sk, ok := ctx.Value(ContextKeySessionKey).(string); ok {
		sessionKey = sk
	}
	if ch, ok := ctx.Value(ContextKeyChannel).(string); ok {
		channel = ch
	}

	// Fallback to gateway SessionContext for agent-driven extraction
	if sessCtx := types.GetSessionContext(ctx); sessCtx != nil {
		if len(messageIDs) == 0 && len(sessCtx.CurrentMessageIDs) > 0 {
			messageIDs = sessCtx.CurrentMessageIDs
		}
		if channel == "" && sessCtx.Channel != "" {
			channel = sessCtx.Channel
		}
	}

	// Log the store decision with content and reasoning for visibility
	L_info("memory_graph_store: storing",
		"type", params.MemoryType,
		"content", truncateStr(params.Content, 80),
		"reasoning", params.Reasoning,
	)

	L_debug("memory_graph_store: executing",
		"type", params.MemoryType,
		"contentLen", len(params.Content),
		"associations", len(params.Associations),
		"username", username,
	)

	// Build memory with provenance
	mem := &Memory{
		Content:       params.Content,
		Type:          Type(params.MemoryType),
		Username:      username,
		Source:        params.Source,
		Emotion:       params.Emotion,
		SourceSession: sessionKey,
		Channel:       channel,
	}

	// Set source message IDs if available (comma-separated)
	if len(messageIDs) > 0 {
		mem.SourceMessage = strings.Join(messageIDs, ",")
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

	// Set occurred_at - parse from params or use context default
	if params.OccurredAt != "" {
		// Try RFC3339 first, then date-only
		if t, err := time.Parse(time.RFC3339, params.OccurredAt); err == nil {
			mem.OccurredAt = t
		} else if t, err := time.Parse("2006-01-02", params.OccurredAt); err == nil {
			mem.OccurredAt = t
		} else {
			L_warn("memory_graph_store: invalid occurred_at format", "value", params.OccurredAt)
		}
	}
	// If still zero, use context default timestamp
	if mem.OccurredAt.IsZero() {
		if ts, ok := ctx.Value(ContextKeyDefaultTimestamp).(time.Time); ok {
			mem.OccurredAt = ts
		}
		// CreateMemory will default to time.Now() if still zero
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

	// Mark messages as extracted to prevent duplicate background extraction
	if len(messageIDs) > 0 {
		for _, msgID := range messageIDs {
			_ = setIngestionState(t.manager.DB(), &IngestionState{
				SourceType:  "agent",
				SourcePath:  msgID,
				ContentHash: HashContent(params.Content),
				IngestedAt:  time.Now(),
				MemoryCount: 1,
			})
		}
		L_debug("memory_graph_store: marked ingestion state", "messageIDs", messageIDs)
	}

	// Invalidate the appropriate bulletin cache based on memory type
	if username != "" {
		if isContextBulletinType(Type(params.MemoryType)) {
			t.manager.InvalidateContextBulletinCache(username)
		} else {
			t.manager.InvalidateMemoryBulletinCache(username)
		}
	}

	// Return the new memory's UUID
	result := fmt.Sprintf("Saved memory %s [%s] %q", mem.UUID, params.MemoryType, truncateStr(params.Content, 50))
	if associationsCreated > 0 {
		result += fmt.Sprintf(" with %d association(s)", associationsCreated)
	}

	return types.TextResult(result), nil
}
