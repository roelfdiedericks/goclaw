package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/transcript"
)

// TranscriptTool provides search and query access to conversation history
type TranscriptTool struct {
	manager *transcript.Manager
}

// NewTranscriptTool creates a new transcript tool
func NewTranscriptTool(manager *transcript.Manager) *TranscriptTool {
	return &TranscriptTool{manager: manager}
}

func (t *TranscriptTool) Name() string {
	return "transcript"
}

func (t *TranscriptTool) Description() string {
	return "Search and query conversation history. Actions: 'semantic' (natural language search), 'recent' (latest messages), 'search' (supports matchType: 'exact' for substring, 'semantic' for vector, 'hybrid' default), 'gaps' (time gaps/breaks), 'stats' (indexing status). Filters: source, excludeSources, humanOnly (exclude cron/heartbeat), after/before/lastDays (time range), role (user/assistant). Output includes source field."
}

func (t *TranscriptTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"semantic", "recent", "search", "gaps", "stats"},
				"description": "Action to perform: 'semantic' (natural language search on chunks), 'recent' (last N messages), 'search' (flexible search with matchType: exact/semantic/hybrid), 'gaps' (conversation time gaps), 'stats' (indexing status)",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Search query (required for 'semantic' and 'search' actions)",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum number of results to return (default: 10)",
			},
			"minHours": map[string]any{
				"type":        "number",
				"description": "For 'gaps' action: minimum gap duration in hours (default: 1)",
			},
			// Filter parameters
			"source": map[string]any{
				"type":        "string",
				"description": "Filter by message source (e.g., 'telegram', 'tui', 'http')",
			},
			"excludeSources": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Exclude messages from these sources (e.g., ['cron', 'heartbeat'])",
			},
			"humanOnly": map[string]any{
				"type":        "boolean",
				"description": "Exclude automated messages (cron, heartbeat). Shorthand for excludeSources.",
			},
			"after": map[string]any{
				"type":        "string",
				"description": "Filter messages after this date (ISO 8601 format, e.g., '2026-02-01')",
			},
			"before": map[string]any{
				"type":        "string",
				"description": "Filter messages before this date (ISO 8601 format)",
			},
			"lastDays": map[string]any{
				"type":        "integer",
				"description": "Filter to messages from the last N days",
			},
			"role": map[string]any{
				"type":        "string",
				"enum":        []string{"user", "assistant"},
				"description": "Filter by message role",
			},
			"matchType": map[string]any{
				"type":        "string",
				"enum":        []string{"exact", "semantic", "hybrid"},
				"description": "For 'search' action: 'exact' (substring match on messages), 'semantic' (vector search on chunks), 'hybrid' (both with exact boost, default)",
			},
		},
		"required": []string{"action"},
	}
}

type transcriptInput struct {
	Action   string  `json:"action"`
	Query    string  `json:"query"`
	Limit    int     `json:"limit"`
	MinHours float64 `json:"minHours"`

	// Filter parameters
	Source         string   `json:"source"`
	ExcludeSources []string `json:"excludeSources"`
	HumanOnly      bool     `json:"humanOnly"`
	After          string   `json:"after"`
	Before         string   `json:"before"`
	LastDays       int      `json:"lastDays"`
	Role           string   `json:"role"`

	// Search mode
	MatchType string `json:"matchType"` // "exact", "semantic", "hybrid" (default)
}

func (t *TranscriptTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params transcriptInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if params.Action == "" {
		return "", fmt.Errorf("action is required")
	}

	// Get user context for scoping
	sessionCtx := GetSessionContext(ctx)
	userID := ""
	isOwner := false
	if sessionCtx != nil && sessionCtx.User != nil {
		userID = sessionCtx.User.ID
		isOwner = sessionCtx.User.IsOwner()
	}

	L_debug("transcript: executing",
		"action", params.Action,
		"userID", userID,
		"isOwner", isOwner,
	)

	if t.manager == nil {
		return marshalOutput(map[string]string{
			"error": "transcript manager not available",
		})
	}

	switch params.Action {
	case "semantic":
		return t.executeSemantic(ctx, params, userID, isOwner)
	case "recent":
		return t.executeRecent(ctx, params, userID, isOwner)
	case "search":
		return t.executeSearch(ctx, params, userID, isOwner)
	case "gaps":
		return t.executeGaps(ctx, params, userID, isOwner)
	case "stats":
		return t.executeStats(ctx)
	default:
		return "", fmt.Errorf("unknown action: %s", params.Action)
	}
}

func (t *TranscriptTool) executeSemantic(ctx context.Context, params transcriptInput, userID string, isOwner bool) (string, error) {
	if params.Query == "" {
		return "", fmt.Errorf("query is required for semantic search")
	}

	opts := transcript.DefaultSearchOptions()
	if params.Limit > 0 {
		opts.MaxResults = params.Limit
	}

	results, err := t.manager.Search(ctx, params.Query, userID, isOwner, opts)
	if err != nil {
		L_error("transcript: semantic search failed", "error", err)
		return marshalOutput(map[string]any{
			"error":   err.Error(),
			"results": []any{},
		})
	}

	L_info("transcript: semantic search completed",
		"query", truncateQuery(params.Query, 30),
		"results", len(results),
	)

	return marshalOutput(map[string]any{
		"results": formatSearchResults(results),
		"count":   len(results),
	})
}

func (t *TranscriptTool) executeRecent(ctx context.Context, params transcriptInput, userID string, isOwner bool) (string, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 10
	}

	filter := buildQueryFilter(params)
	entries, err := t.manager.Recent(ctx, userID, isOwner, limit, filter)
	if err != nil {
		L_error("transcript: recent query failed", "error", err)
		return marshalOutput(map[string]any{
			"error":   err.Error(),
			"entries": []any{},
		})
	}

	return marshalOutput(map[string]any{
		"entries": formatRecentEntries(entries),
		"count":   len(entries),
	})
}

func (t *TranscriptTool) executeSearch(ctx context.Context, params transcriptInput, userID string, isOwner bool) (string, error) {
	if params.Query == "" {
		return "", fmt.Errorf("query is required for search")
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 10
	}

	matchType := params.MatchType
	if matchType == "" {
		matchType = "hybrid" // Default
	}

	L_debug("transcript: search",
		"query", truncateQuery(params.Query, 30),
		"matchType", matchType,
		"limit", limit,
	)

	switch matchType {
	case "exact":
		// Exact substring search on messages table
		filter := buildQueryFilter(params)
		entries, err := t.manager.ExactSearch(ctx, params.Query, userID, isOwner, limit, filter)
		if err != nil {
			L_error("transcript: exact search failed", "error", err)
			return marshalOutput(map[string]any{
				"error":   err.Error(),
				"results": []any{},
			})
		}

		L_info("transcript: exact search completed",
			"query", truncateQuery(params.Query, 30),
			"results", len(entries),
		)

		return marshalOutput(map[string]any{
			"results":   formatRecentEntries(entries), // Same format as recent
			"count":     len(entries),
			"matchType": "exact",
		})

	case "semantic":
		// Pure vector search on chunks
		opts := transcript.SearchOptions{
			MaxResults:    limit,
			MinScore:      0.3,
			VectorWeight:  1.0, // Vector only
			KeywordWeight: 0.0,
		}

		results, err := t.manager.Search(ctx, params.Query, userID, isOwner, opts)
		if err != nil {
			L_error("transcript: semantic search failed", "error", err)
			return marshalOutput(map[string]any{
				"error":   err.Error(),
				"results": []any{},
			})
		}

		L_info("transcript: semantic search completed",
			"query", truncateQuery(params.Query, 30),
			"results", len(results),
		)

		return marshalOutput(map[string]any{
			"results":   formatSearchResults(results),
			"count":     len(results),
			"matchType": "semantic",
		})

	default: // "hybrid"
		// Hybrid search with exact match boost
		opts := transcript.SearchOptions{
			MaxResults:      limit,
			MinScore:        0.1,
			VectorWeight:    0.5,
			KeywordWeight:   0.5,
			ExactBoost:      true, // Boost chunks containing exact query
			ExactBoostQuery: params.Query,
		}

		results, err := t.manager.Search(ctx, params.Query, userID, isOwner, opts)
		if err != nil {
			L_error("transcript: hybrid search failed", "error", err)
			return marshalOutput(map[string]any{
				"error":   err.Error(),
				"results": []any{},
			})
		}

		L_info("transcript: hybrid search completed",
			"query", truncateQuery(params.Query, 30),
			"results", len(results),
		)

		return marshalOutput(map[string]any{
			"results":   formatSearchResults(results),
			"count":     len(results),
			"matchType": "hybrid",
		})
	}
}

func (t *TranscriptTool) executeGaps(ctx context.Context, params transcriptInput, userID string, isOwner bool) (string, error) {
	minHours := params.MinHours
	if minHours <= 0 {
		minHours = 1.0
	}
	limit := params.Limit
	if limit <= 0 {
		limit = 10
	}

	filter := buildQueryFilter(params)
	gaps, err := t.manager.Gaps(ctx, userID, isOwner, minHours, limit, filter)
	if err != nil {
		L_error("transcript: gaps query failed", "error", err)
		return marshalOutput(map[string]any{
			"error": err.Error(),
			"gaps":  []any{},
		})
	}

	return marshalOutput(map[string]any{
		"gaps":  formatGapEntries(gaps),
		"count": len(gaps),
	})
}

func (t *TranscriptTool) executeStats(ctx context.Context) (string, error) {
	stats := t.manager.Stats()
	return marshalOutput(stats)
}

// buildQueryFilter creates a QueryFilter from transcript input parameters
func buildQueryFilter(params transcriptInput) *transcript.QueryFilter {
	filter := &transcript.QueryFilter{
		Source:         params.Source,
		ExcludeSources: params.ExcludeSources,
		HumanOnly:      params.HumanOnly,
		LastDays:       params.LastDays,
		Role:           params.Role,
	}

	// Parse time filters
	if params.After != "" {
		if t, err := time.Parse("2006-01-02", params.After); err == nil {
			filter.After = t
		} else if t, err := time.Parse(time.RFC3339, params.After); err == nil {
			filter.After = t
		}
	}
	if params.Before != "" {
		if t, err := time.Parse("2006-01-02", params.Before); err == nil {
			filter.Before = t
		} else if t, err := time.Parse(time.RFC3339, params.Before); err == nil {
			filter.Before = t
		}
	}

	return filter
}

// formatSearchResults formats search results for output
func formatSearchResults(results []transcript.SearchResult) []map[string]any {
	formatted := make([]map[string]any, len(results))
	for i, r := range results {
		formatted[i] = map[string]any{
			"content":   truncateContent(r.Content, 500),
			"timestamp": r.TimestampStart.Format(time.RFC3339),
			"score":     fmt.Sprintf("%.2f", r.Score),
			"matchType": r.MatchType,
		}
	}
	return formatted
}

// formatRecentEntries formats recent entries for output
func formatRecentEntries(entries []transcript.RecentEntry) []map[string]any {
	formatted := make([]map[string]any, len(entries))
	for i, e := range entries {
		entry := map[string]any{
			"timestamp": e.Timestamp.Format(time.RFC3339),
			"role":      e.Role,
			"preview":   e.Preview,
		}
		if e.Source != "" {
			entry["source"] = e.Source
		}
		formatted[i] = entry
	}
	return formatted
}

// formatGapEntries formats gap entries for output
func formatGapEntries(gaps []transcript.GapEntry) []map[string]any {
	formatted := make([]map[string]any, len(gaps))
	for i, g := range gaps {
		entry := map[string]any{
			"from":        g.From.Format(time.RFC3339),
			"to":          g.To.Format(time.RFC3339),
			"gapHours":    fmt.Sprintf("%.1f", g.GapHours),
			"lastMessage": g.LastMessage,
		}
		if g.Source != "" {
			entry["source"] = g.Source
		}
		formatted[i] = entry
	}
	return formatted
}

// truncateContent truncates content for display
func truncateContent(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// truncateQuery is defined in memory_search.go
