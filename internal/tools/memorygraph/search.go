package memorygraph

import (
	"context"
	"encoding/json"
	"fmt"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	mgraph "github.com/roelfdiedericks/goclaw/internal/memorygraph"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// SearchTool performs hybrid semantic search on the memory graph
type SearchTool struct{}

// NewSearchTool creates a new memory graph search tool
func NewSearchTool() *SearchTool {
	return &SearchTool{}
}

func (t *SearchTool) Name() string {
	return "memory_graph_search"
}

func (t *SearchTool) Description() string {
	return "Search the structured memory graph using hybrid semantic search. Combines vector similarity, keyword matching, graph traversal, and recency. Use when you need to recall facts, preferences, decisions, events, routines, or any stored knowledge about the user."
}

func (t *SearchTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Natural language search query. Semantic search will find related content.",
			},
			"types": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Filter by memory types: identity, fact, preference, decision, event, observation, goal, todo, routine, feedback, anomaly, correlation, prediction",
			},
			"context_memory": map[string]any{
				"type":        "string",
				"description": "Optional UUID of a related memory to use for graph-based search expansion.",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Maximum results to return. Default: 10.",
			},
		},
		"required": []string{"query"},
	}
}

type searchInput struct {
	Query         string   `json:"query"`
	Types         []string `json:"types,omitempty"`
	ContextMemory string   `json:"context_memory,omitempty"`
	MaxResults    int      `json:"max_results,omitempty"`
}

type searchOutput struct {
	Results []searchResultOutput `json:"results"`
	Error   string               `json:"error,omitempty"`
}

type searchResultOutput struct {
	ID         string             `json:"id"`
	Content    string             `json:"content"`
	Type       string             `json:"type"`
	Importance float32            `json:"importance"`
	Score      float32            `json:"score"`
	CreatedAt  string             `json:"created_at"`
	Sources    map[string]float32 `json:"sources,omitempty"`
}

func (t *SearchTool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var params searchInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	if params.Query == "" {
		return nil, fmt.Errorf("query is required")
	}

	manager := mgraph.GetManager()
	if manager == nil {
		output := searchOutput{
			Results: []searchResultOutput{},
			Error:   "memory graph is not enabled",
		}
		return marshalOutput(output)
	}

	// Build search options
	opts := mgraph.SearchOptions{
		Query:         params.Query,
		ContextMemory: params.ContextMemory,
		MaxResults:    params.MaxResults,
	}

	// Convert type strings to Type
	for _, t := range params.Types {
		opts.Types = append(opts.Types, mgraph.Type(t))
	}

	L_debug("memory_graph_search: executing", "query", truncate(params.Query, 50), "types", params.Types)

	results, err := manager.Search(ctx, opts)
	if err != nil {
		L_error("memory_graph_search: search failed", "error", err)
		output := searchOutput{
			Results: []searchResultOutput{},
			Error:   err.Error(),
		}
		return marshalOutput(output)
	}

	// Touch memories that were accessed
	for _, r := range results {
		_ = manager.TouchMemory(r.Memory.UUID)
	}

	// Convert to output format
	output := searchOutput{
		Results: make([]searchResultOutput, 0, len(results)),
	}

	for _, r := range results {
		output.Results = append(output.Results, searchResultOutput{
			ID:         r.Memory.UUID,
			Content:    r.Memory.Content,
			Type:       string(r.Memory.Type),
			Importance: r.Memory.Importance,
			Score:      r.Score,
			CreatedAt:  r.Memory.CreatedAt.Format("2006-01-02"),
			Sources:    r.SourceScores,
		})
	}

	L_info("memory_graph_search: completed", "query", truncate(params.Query, 30), "results", len(results))

	return marshalOutput(output)
}
