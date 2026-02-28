package memorygraph

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	mgraph "github.com/roelfdiedericks/goclaw/internal/memorygraph"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// QueryTool performs structured queries on the memory graph
type QueryTool struct{}

// NewQueryTool creates a new memory graph query tool
func NewQueryTool() *QueryTool {
	return &QueryTool{}
}

func (t *QueryTool) Name() string {
	return "memory_graph_query"
}

func (t *QueryTool) Description() string {
	return "Query the memory graph with structured filters. Use when you need to list memories by type, find related memories, or get specific memory details. For semantic search, use memory_graph_search instead."
}

func (t *QueryTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"get", "list", "related", "stats"},
				"description": "Action: 'get' for single memory by ID, 'list' for filtered list, 'related' for associated memories, 'stats' for statistics",
			},
			"id": map[string]any{
				"type":        "string",
				"description": "Memory UUID (required for 'get' and 'related' actions)",
			},
			"types": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Filter by memory types (for 'list' action)",
			},
			"min_importance": map[string]any{
				"type":        "number",
				"description": "Minimum importance threshold 0.0-1.0 (for 'list' action)",
			},
			"order_by": map[string]any{
				"type":        "string",
				"enum":        []string{"created_at", "updated_at", "importance", "access_count"},
				"description": "Sort field (for 'list' action). Default: created_at",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum results. Default: 20",
			},
		},
		"required": []string{"action"},
	}
}

type queryInput struct {
	Action        string   `json:"action"`
	ID            string   `json:"id,omitempty"`
	Types         []string `json:"types,omitempty"`
	MinImportance float32  `json:"min_importance,omitempty"`
	OrderBy       string   `json:"order_by,omitempty"`
	Limit         int      `json:"limit,omitempty"`
}

type queryOutput struct {
	Memory   *memoryOutput   `json:"memory,omitempty"`
	Memories []memoryOutput  `json:"memories,omitempty"`
	Stats    *statsOutput    `json:"stats,omitempty"`
	Error    string          `json:"error,omitempty"`
}

type memoryOutput struct {
	ID           string             `json:"id"`
	Content      string             `json:"content"`
	Type         string             `json:"type"`
	Importance   float32            `json:"importance"`
	Confidence   float32            `json:"confidence,omitempty"`
	CreatedAt    string             `json:"created_at"`
	UpdatedAt    string             `json:"updated_at"`
	AccessCount  int64              `json:"access_count"`
	Associations []associationInfo  `json:"associations,omitempty"`
}

type associationInfo struct {
	ID           string  `json:"id"`
	TargetID     string  `json:"target_id"`
	RelationType string  `json:"relation_type"`
	Weight       float32 `json:"weight"`
}

type statsOutput struct {
	TotalMemories     int            `json:"total_memories"`
	TotalAssociations int            `json:"total_associations"`
	WithEmbeddings    int            `json:"with_embeddings"`
	ByType            map[string]int `json:"by_type"`
}

func (t *QueryTool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var params queryInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	manager := mgraph.GetManager()
	if manager == nil {
		output := queryOutput{
			Error: "memory graph is not enabled",
		}
		return marshalOutput(output)
	}

	if params.Limit <= 0 {
		params.Limit = 20
	}

	L_debug("memory_graph_query: executing", "action", params.Action, "id", params.ID)

	output := queryOutput{}

	switch params.Action {
	case "get":
		if params.ID == "" {
			return nil, fmt.Errorf("id is required for 'get' action")
		}

		mem, err := manager.GetMemory(params.ID)
		if err != nil {
			output.Error = err.Error()
			return marshalOutput(output)
		}
		if mem == nil {
			output.Error = "memory not found"
			return marshalOutput(output)
		}

		// Get associations
		assocs, _ := manager.Store().GetAssociationsFrom(mem.UUID)

		output.Memory = memoryToOutput(mem, assocs)

	case "list":
		q := manager.Query()

		if len(params.Types) > 0 {
			typeList := make([]mgraph.Type, len(params.Types))
			for i, t := range params.Types {
				typeList[i] = mgraph.Type(t)
			}
			q.Types(typeList...)
		}

		if params.MinImportance > 0 {
			q.MinImportance(params.MinImportance)
		}

		if params.OrderBy != "" {
			q.OrderBy(params.OrderBy)
		}

		q.Limit(params.Limit)

		memories, err := manager.ExecuteQuery(q)
		if err != nil {
			output.Error = err.Error()
			return marshalOutput(output)
		}

		output.Memories = make([]memoryOutput, 0, len(memories))
		for _, mem := range memories {
			output.Memories = append(output.Memories, *memoryToOutput(mem, nil))
		}

	case "related":
		if params.ID == "" {
			return nil, fmt.Errorf("id is required for 'related' action")
		}

		memories, err := mgraph.GetRelatedMemories(manager.DB(), params.ID, 1, nil)
		if err != nil {
			output.Error = err.Error()
			return marshalOutput(output)
		}

		output.Memories = make([]memoryOutput, 0, len(memories))
		for _, mem := range memories {
			output.Memories = append(output.Memories, *memoryToOutput(mem, nil))
		}

	case "stats":
		stats, err := manager.Stats()
		if err != nil {
			output.Error = err.Error()
			return marshalOutput(output)
		}

		byType := make(map[string]int)
		for t, count := range stats.ByType {
			byType[string(t)] = count
		}

		output.Stats = &statsOutput{
			TotalMemories:     stats.TotalMemories,
			TotalAssociations: stats.TotalAssociations,
			WithEmbeddings:    stats.WithEmbeddings,
			ByType:            byType,
		}

	default:
		return nil, fmt.Errorf("unknown action: %s", params.Action)
	}

	L_info("memory_graph_query: completed", "action", params.Action)

	return marshalOutput(output)
}

func memoryToOutput(mem *mgraph.Memory, assocs []*mgraph.Association) *memoryOutput {
	out := &memoryOutput{
		ID:          mem.UUID,
		Content:     mem.Content,
		Type:        string(mem.Type),
		Importance:  mem.Importance,
		CreatedAt:   mem.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   mem.UpdatedAt.Format(time.RFC3339),
		AccessCount: mem.AccessCount,
	}

	if mem.Confidence != mgraph.ConfidenceNotApplicable {
		out.Confidence = mem.Confidence
	}

	if len(assocs) > 0 {
		out.Associations = make([]associationInfo, 0, len(assocs))
		for _, a := range assocs {
			out.Associations = append(out.Associations, associationInfo{
				ID:           a.ID,
				TargetID:     a.TargetID,
				RelationType: string(a.RelationType),
				Weight:       a.Weight,
			})
		}
	}

	return out
}
