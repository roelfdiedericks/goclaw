package memorygraph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// RecallTool is a simple memory search tool for use in extraction.
// Spacebot-compatible design with minimal parameters.
type RecallTool struct {
	manager *Manager
}

// NewRecallTool creates a new recall tool with explicit manager reference.
func NewRecallTool(mgr *Manager) *RecallTool {
	return &RecallTool{manager: mgr}
}

func (t *RecallTool) Name() string {
	return "memory_graph_recall"
}

func (t *RecallTool) Description() string {
	return "Search for related memories. Use before saving to find existing memories on a topic. Returns memories matching the query."
}

func (t *RecallTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query. Required for hybrid mode.",
			},
			"mode": map[string]any{
				"type":        "string",
				"enum":        []string{"hybrid", "recent", "important", "typed"},
				"default":     "hybrid",
				"description": "Search mode: hybrid (semantic+keyword), recent, important, typed (by type)",
			},
			"memory_type": map[string]any{
				"type":        "string",
				"enum":        []string{"identity", "fact", "preference", "decision", "event", "observation", "goal", "todo", "routine", "feedback", "anomaly", "correlation", "prediction"},
				"description": "Filter by type. Required for 'typed' mode.",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Maximum results (default: 10)",
			},
			"sort_by": map[string]any{
				"type":        "string",
				"enum":        []string{"recent", "importance", "most_accessed"},
				"description": "Sort order for non-hybrid modes.",
			},
		},
	}
}

// RecallParams defines input parameters for the recall tool.
type RecallParams struct {
	Query      string `json:"query,omitempty"`
	Mode       string `json:"mode,omitempty"`
	MemoryType string `json:"memory_type,omitempty"`
	MaxResults int    `json:"max_results,omitempty"`
	SortBy     string `json:"sort_by,omitempty"`
}

func (t *RecallTool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var params RecallParams
	if err := json.Unmarshal(input, &params); err != nil {
		return types.ErrorResult(fmt.Sprintf("invalid input: %v", err)), nil
	}

	// Default mode
	if params.Mode == "" {
		params.Mode = "hybrid"
	}

	// Validate
	if params.Mode == "hybrid" && params.Query == "" {
		return types.ErrorResult("query is required for hybrid mode"), nil
	}
	if params.Mode == "typed" && params.MemoryType == "" {
		return types.ErrorResult("memory_type is required for typed mode"), nil
	}

	// Default max results
	maxResults := params.MaxResults
	if maxResults <= 0 {
		maxResults = 10
	}

	// Get username from context - required for privacy isolation
	username, err := getUsernameFromContext(ctx)
	if err != nil {
		return types.ErrorResult(err.Error()), nil
	}

	L_debug("memory_graph_recall: executing",
		"mode", params.Mode,
		"query", truncateStr(params.Query, 50),
		"type", params.MemoryType,
		"username", username,
	)

	var results []SearchResult

	switch params.Mode {
	case "hybrid":
		opts := SearchOptions{
			Query:      params.Query,
			MaxResults: maxResults,
			Username:   username,
		}
		if params.MemoryType != "" {
			opts.Types = []Type{Type(params.MemoryType)}
		}
		results, err = t.manager.Search(ctx, opts)

	case "recent", "important", "most_accessed":
		q := t.manager.Query()
		if username != "" {
			q.Username(username)
		}
		if params.MemoryType != "" {
			q.Types(Type(params.MemoryType))
		}
		switch params.Mode {
		case "recent":
			q.OrderBy("occurred_at")
		case "important":
			q.OrderBy("importance")
		case "most_accessed":
			q.OrderBy("access_count")
		}
		q.Limit(maxResults)

		memories, qerr := t.manager.ExecuteQuery(q)
		if qerr != nil {
			err = qerr
		} else {
			results = make([]SearchResult, len(memories))
			for i, m := range memories {
				results[i] = SearchResult{Memory: *m, Score: m.Importance}
			}
		}

	case "typed":
		q := t.manager.Query().Types(Type(params.MemoryType))
		if username != "" {
			q.Username(username)
		}
		if params.SortBy != "" {
			switch params.SortBy {
			case "recent":
				q.OrderBy("occurred_at")
			case "importance":
				q.OrderBy("importance")
			case "most_accessed":
				q.OrderBy("access_count")
			}
		}
		q.Limit(maxResults)

		memories, qerr := t.manager.ExecuteQuery(q)
		if qerr != nil {
			err = qerr
		} else {
			results = make([]SearchResult, len(memories))
			for i, m := range memories {
				results[i] = SearchResult{Memory: *m, Score: m.Importance}
			}
		}

	default:
		return types.ErrorResult(fmt.Sprintf("unknown mode: %s", params.Mode)), nil
	}

	if err != nil {
		L_error("memory_graph_recall: search failed", "error", err)
		return types.ErrorResult(fmt.Sprintf("search failed: %v", err)), nil
	}

	// Touch accessed memories and log what was recalled
	for _, r := range results {
		_ = t.manager.TouchMemory(r.Memory.UUID)
		L_debug("memory_graph_recall: result",
			"id", r.Memory.UUID,
			"type", r.Memory.Type,
			"content", truncateStr(r.Memory.Content, 60),
		)
	}

	// Format output for LLM
	output := formatRecallResults(results)

	L_info("memory_graph_recall: completed",
		"mode", params.Mode,
		"results", len(results),
	)

	return types.TextResult(output), nil
}

func formatRecallResults(results []SearchResult) string {
	if len(results) == 0 {
		return "No memories found."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Recalled Memories (%d results)\n\n", len(results)))

	for i, r := range results {
		m := r.Memory
		sb.WriteString(fmt.Sprintf("%d. [%s] (id: %s, importance: %.2f",
			i+1, m.Type, m.UUID, m.Importance))

		if r.Score > 0 && r.Score != m.Importance {
			sb.WriteString(fmt.Sprintf(", relevance: %.2f", r.Score))
		}
		if m.Emotion != "" {
			sb.WriteString(fmt.Sprintf(", emotion: %s", m.Emotion))
		}
		sb.WriteString(")\n")
		sb.WriteString(fmt.Sprintf("   %s\n\n", m.Content))
	}

	return sb.String()
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
