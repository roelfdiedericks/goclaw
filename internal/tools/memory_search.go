package tools

import (
	"context"
	"encoding/json"
	"fmt"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/memory"
)

// MemorySearchTool searches memory files semantically
type MemorySearchTool struct {
	manager *memory.Manager
}

// NewMemorySearchTool creates a new memory search tool
func NewMemorySearchTool(manager *memory.Manager) *MemorySearchTool {
	return &MemorySearchTool{manager: manager}
}

func (t *MemorySearchTool) Name() string {
	return "memory_search"
}

func (t *MemorySearchTool) Description() string {
	return "Search MEMORY.md and memory/*.md files semantically. Use to recall prior work, decisions, dates, people, preferences, or todos. Returns matching snippets with file path and line numbers."
}

func (t *MemorySearchTool) Schema() map[string]any {
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

func (t *MemorySearchTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params memorySearchInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if params.Query == "" {
		return "", fmt.Errorf("query is required")
	}

	L_debug("memory_search: executing", "query", truncateQuery(params.Query, 50), "maxResults", params.MaxResults)

	// Check if manager is available
	if t.manager == nil {
		L_warn("memory_search: manager not available")
		output := memorySearchOutput{
			Results: []memory.SearchResult{},
			Error:   "memory search is not enabled",
		}
		return marshalOutput(output)
	}

	// Perform search
	results, err := t.manager.Search(ctx, params.Query, params.MaxResults, params.MinScore)
	if err != nil {
		L_error("memory_search: search failed", "error", err)
		output := memorySearchOutput{
			Results: []memory.SearchResult{},
			Error:   err.Error(),
		}
		return marshalOutput(output)
	}

	// Get provider info
	_, _, provider, _ := t.manager.Stats()

	L_info("memory_search: completed", "query", truncateQuery(params.Query, 30), "results", len(results), "provider", provider)

	output := memorySearchOutput{
		Results:  results,
		Provider: provider,
	}

	return marshalOutput(output)
}

func truncateQuery(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func marshalOutput(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
