package memorysearch

import (
	"context"
	"encoding/json"
	"fmt"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/memory"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// Tool searches memory files semantically
type Tool struct {
	manager *memory.Manager
}

// NewTool creates a new memory search tool
func NewTool(manager *memory.Manager) *Tool {
	return &Tool{manager: manager}
}

func (t *Tool) Name() string {
	return "memory_search"
}

func (t *Tool) Description() string {
	return "Search MEMORY.md and memory/*.md files semantically. USE THIS when user asks 'what did we decide', 'what's my preference', or references stored decisions/context. Use to recall prior work, decisions, dates, people, preferences, or todos. Returns matching snippets with file path and line numbers."
}

func (t *Tool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "The search query. Can be natural language - semantic search will find related content even if exact words don't match.",
			},
			"maxResults": map[string]any{
				"type":        "integer",
				"description": "Maximum number of results to return. Default: 6.",
			},
			"minScore": map[string]any{
				"type":        "number",
				"description": "Minimum relevance score (0-1). Default: 0.35.",
			},
		},
		"required": []string{"query"},
	}
}

type memorySearchInput struct {
	Query      string  `json:"query"`
	MaxResults int     `json:"maxResults,omitempty"`
	MinScore   float64 `json:"minScore,omitempty"`
}

type memorySearchOutput struct {
	Results  []memory.SearchResult `json:"results"`
	Provider string                `json:"provider,omitempty"`
	Error    string                `json:"error,omitempty"`
}

func (t *Tool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var params memorySearchInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	if params.Query == "" {
		return nil, fmt.Errorf("query is required")
	}

	L_debug("memory_search: executing", "query", truncate(params.Query, 50), "maxResults", params.MaxResults)

	if t.manager == nil {
		L_warn("memory_search: manager not available")
		output := memorySearchOutput{
			Results: []memory.SearchResult{},
			Error:   "memory search is not enabled",
		}
		return marshalOutput(output)
	}

	results, err := t.manager.Search(ctx, params.Query, params.MaxResults, params.MinScore)
	if err != nil {
		L_error("memory_search: search failed", "error", err)
		output := memorySearchOutput{
			Results: []memory.SearchResult{},
			Error:   err.Error(),
		}
		return marshalOutput(output)
	}

	_, _, provider, _ := t.manager.Stats()

	L_info("memory_search: completed", "query", truncate(params.Query, 30), "results", len(results), "provider", provider)

	output := memorySearchOutput{
		Results:  results,
		Provider: provider,
	}

	return marshalOutput(output)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func marshalOutput(v any) (*types.ToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return types.TextResult(string(b)), nil
}
