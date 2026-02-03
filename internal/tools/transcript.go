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
	return "Search and query conversation history. Use 'semantic' for natural language search, 'recent' for latest messages, 'search' for keyword search, 'gaps' for time gaps (sleep detection), 'stats' for indexing status."
}

func (t *TranscriptTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"semantic", "recent", "search", "gaps", "stats"},
				"description": "Action to perform: 'semantic' (natural language search), 'recent' (last N messages), 'search' (keyword search), 'gaps' (conversation time gaps), 'stats' (indexing status)",
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
		},
		"required": []string{"action"},
	}
}

type transcriptInput struct {
	Action   string  `json:"action"`
	Query    string  `json:"query"`
	Limit    int     `json:"limit"`
	MinHours float64 `json:"minHours"`
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

	entries, err := t.manager.Recent(ctx, userID, isOwner, limit)
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

	// For keyword search, use lower vector weight
	opts := transcript.SearchOptions{
		MaxResults:    10,
		MinScore:      0.1,
		VectorWeight:  0.3,
		KeywordWeight: 0.7,
	}
	if params.Limit > 0 {
		opts.MaxResults = params.Limit
	}

	results, err := t.manager.Search(ctx, params.Query, userID, isOwner, opts)
	if err != nil {
		L_error("transcript: keyword search failed", "error", err)
		return marshalOutput(map[string]any{
			"error":   err.Error(),
			"results": []any{},
		})
	}

	L_info("transcript: keyword search completed",
		"query", truncateQuery(params.Query, 30),
		"results", len(results),
	)

	return marshalOutput(map[string]any{
		"results": formatSearchResults(results),
		"count":   len(results),
	})
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

	gaps, err := t.manager.Gaps(ctx, userID, isOwner, minHours, limit)
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
		formatted[i] = map[string]any{
			"timestamp": e.Timestamp.Format(time.RFC3339),
			"role":      e.Role,
			"preview":   e.Preview,
		}
	}
	return formatted
}

// formatGapEntries formats gap entries for output
func formatGapEntries(gaps []transcript.GapEntry) []map[string]any {
	formatted := make([]map[string]any, len(gaps))
	for i, g := range gaps {
		formatted[i] = map[string]any{
			"from":        g.From.Format(time.RFC3339),
			"to":          g.To.Format(time.RFC3339),
			"gapHours":    fmt.Sprintf("%.1f", g.GapHours),
			"lastMessage": g.LastMessage,
		}
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

func truncateQuery(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
