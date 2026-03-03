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

// QueryTool is a full-featured memory query tool for general agent use.
// Supports all filters, time ranges, graph traversal, and associations.
type QueryTool struct {
	manager *Manager
}

// NewQueryTool creates a new query tool with explicit manager reference.
func NewQueryTool(mgr *Manager) *QueryTool {
	return &QueryTool{manager: mgr}
}

func (t *QueryTool) Name() string {
	return "memory_graph_query"
}

func (t *QueryTool) Description() string {
	return "Query the memory graph with full filtering options. Supports semantic search, time ranges, importance/confidence thresholds, emotional context, graph traversal, and more. Use for complex queries about stored memories."
}

func (t *QueryTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query. Required for hybrid mode.",
			},
			"mode": map[string]any{
				"type":        "string",
				"enum":        []string{"hybrid", "recent", "important", "typed", "related"},
				"default":     "hybrid",
				"description": "Query mode: hybrid (semantic), recent, important, typed (by type), related (graph traversal)",
			},
			"memory_type": map[string]any{
				"type":        "string",
				"enum":        []string{"identity", "fact", "preference", "decision", "event", "observation", "goal", "todo", "routine", "feedback", "anomaly", "correlation", "prediction"},
				"description": "Filter by type. Required for 'typed' mode.",
			},
			"min_importance": map[string]any{
				"type":        "number",
				"description": "Minimum importance threshold (0.0-1.0)",
			},
			"min_confidence": map[string]any{
				"type":        "number",
				"description": "Minimum confidence threshold (0.0-1.0) for pattern types",
			},
			"emotion": map[string]any{
				"type":        "string",
				"description": "Filter by emotional context: frustrated, excited, stressed, relieved, etc.",
			},
			"source": map[string]any{
				"type":        "string",
				"description": "Filter by source: 'user stated', 'inferred', 'observed', 'agent'",
			},
			"since": map[string]any{
				"type":        "string",
				"description": "Only memories after this time. ISO timestamp or relative: '24h', '7d', '30d'",
			},
			"before": map[string]any{
				"type":        "string",
				"description": "Only memories before this time. ISO timestamp or relative.",
			},
			"related_to": map[string]any{
				"type":        "string",
				"description": "Memory UUID for graph traversal (required for 'related' mode)",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Maximum results (default: 10, max: 50)",
			},
			"sort_by": map[string]any{
				"type":        "string",
				"enum":        []string{"recent", "importance", "most_accessed"},
				"description": "Sort order",
			},
		"include_associations": map[string]any{
			"type":        "boolean",
			"description": "Include association details in results",
		},
		"detail_level": map[string]any{
			"type":        "string",
			"enum":        []string{"summary", "standard", "full"},
			"default":     "standard",
			"description": "Output detail level: summary (minimal), standard (includes provenance), full (all fields including access stats)",
		},
	},
}
}

// QueryParams defines input parameters for the full query tool.
type QueryParams struct {
	Query               string   `json:"query,omitempty"`
	Mode                string   `json:"mode,omitempty"`
	MemoryType          string   `json:"memory_type,omitempty"`
	MinImportance       *float32 `json:"min_importance,omitempty"`
	MinConfidence       *float32 `json:"min_confidence,omitempty"`
	Emotion             string   `json:"emotion,omitempty"`
	Source              string   `json:"source,omitempty"`
	Since               string   `json:"since,omitempty"`
	Before              string   `json:"before,omitempty"`
	RelatedTo           string   `json:"related_to,omitempty"`
	MaxResults          int      `json:"max_results,omitempty"`
	SortBy              string   `json:"sort_by,omitempty"`
	IncludeAssociations bool     `json:"include_associations,omitempty"`
	DetailLevel         string   `json:"detail_level,omitempty"`
}

func (t *QueryTool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var params QueryParams
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
	if params.Mode == "related" && params.RelatedTo == "" {
		return types.ErrorResult("related_to is required for related mode"), nil
	}

	// Default max results
	maxResults := params.MaxResults
	if maxResults <= 0 {
		maxResults = 10
	}
	if maxResults > 50 {
		maxResults = 50
	}

	// Get username from context - required for privacy isolation
	username, err := getUsernameFromContext(ctx)
	if err != nil {
		return types.ErrorResult(err.Error()), nil
	}

	// Parse time filters
	var sinceTime, beforeTime *time.Time
	if params.Since != "" {
		if t, err := parseTimeFilter(params.Since); err == nil {
			sinceTime = &t
		}
	}
	if params.Before != "" {
		if t, err := parseTimeFilter(params.Before); err == nil {
			beforeTime = &t
		}
	}

	L_debug("memory_graph_query: executing",
		"mode", params.Mode,
		"query", truncateStr(params.Query, 50),
		"type", params.MemoryType,
		"emotion", params.Emotion,
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
		if err == nil {
			results = filterResults(results, params, sinceTime, beforeTime)
		}

	case "related":
		memories, rerr := GetRelatedMemories(t.manager.DB(), params.RelatedTo, 1, nil)
		if rerr != nil {
			err = rerr
		} else {
			results = make([]SearchResult, len(memories))
			for i, m := range memories {
				results[i] = SearchResult{Memory: *m, Score: m.Importance}
			}
			results = filterResults(results, params, sinceTime, beforeTime)
		}

	default:
		// Build structured query for recent, important, typed modes
		q := t.manager.Query()

		if username != "" {
			q.Username(username)
		}
		if params.MemoryType != "" {
			q.Types(Type(params.MemoryType))
		}
		if params.MinImportance != nil {
			q.MinImportance(*params.MinImportance)
		}
		if params.MinConfidence != nil {
			q.MinConfidence(*params.MinConfidence)
		}
		if sinceTime != nil {
			q.SinceOccurred(*sinceTime)
		}
		if beforeTime != nil {
			q.UntilOccurred(*beforeTime)
		}

		// Sort order
		switch params.Mode {
		case "recent":
			q.OrderBy("occurred_at")
		case "important":
			q.OrderBy("importance")
		default:
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
		}

		// Get extra results for post-filtering by emotion/source
		extraLimit := maxResults
		if params.Emotion != "" || params.Source != "" {
			extraLimit = maxResults * 3
		}
		q.Limit(extraLimit)

		memories, qerr := t.manager.ExecuteQuery(q)
		if qerr != nil {
			err = qerr
		} else {
			results = make([]SearchResult, 0, len(memories))
			for _, m := range memories {
				// Post-filter by emotion
				if params.Emotion != "" && m.Emotion != params.Emotion {
					continue
				}
				// Post-filter by source
				if params.Source != "" && m.Source != params.Source {
					continue
				}
				results = append(results, SearchResult{Memory: *m, Score: m.Importance})
				if len(results) >= maxResults {
					break
				}
			}
		}
	}

	if err != nil {
		L_error("memory_graph_query: failed", "error", err)
		return types.ErrorResult(fmt.Sprintf("query failed: %v", err)), nil
	}

	// Touch accessed memories
	for _, r := range results {
		_ = t.manager.TouchMemory(r.Memory.UUID)
	}

	// Get associations if requested
	var associations map[string][]*Association
	if params.IncludeAssociations {
		associations = make(map[string][]*Association)
		for _, r := range results {
			assocs, _ := t.manager.Store().GetAssociationsFrom(r.Memory.UUID)
			if len(assocs) > 0 {
				associations[r.Memory.UUID] = assocs
			}
		}
	}

	// Default detail level
	detailLevel := params.DetailLevel
	if detailLevel == "" {
		detailLevel = "standard"
	}

	// Format output for LLM
	output := formatQueryResults(results, associations, detailLevel)

	L_info("memory_graph_query: completed",
		"mode", params.Mode,
		"results", len(results),
		"detail", detailLevel,
	)

	return types.TextResult(output), nil
}

func parseTimeFilter(s string) (time.Time, error) {
	// Try relative format first
	if len(s) > 1 {
		var duration time.Duration
		switch s[len(s)-1] {
		case 'h':
			if hours, err := parseInt(s[:len(s)-1]); err == nil {
				duration = time.Duration(hours) * time.Hour
				return time.Now().Add(-duration), nil
			}
		case 'd':
			if days, err := parseInt(s[:len(s)-1]); err == nil {
				duration = time.Duration(days) * 24 * time.Hour
				return time.Now().Add(-duration), nil
			}
		}
	}

	// Try ISO format
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}

	// Try date only
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}

	return time.Time{}, fmt.Errorf("invalid time format: %s", s)
}

func parseInt(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

func filterResults(results []SearchResult, params QueryParams, since, before *time.Time) []SearchResult {
	filtered := make([]SearchResult, 0, len(results))

	for _, r := range results {
		m := r.Memory

		// Filter by min importance
		if params.MinImportance != nil && m.Importance < *params.MinImportance {
			continue
		}

		// Filter by min confidence
		if params.MinConfidence != nil && m.Confidence != ConfidenceNotApplicable && m.Confidence < *params.MinConfidence {
			continue
		}

		// Filter by emotion
		if params.Emotion != "" && m.Emotion != params.Emotion {
			continue
		}

		// Filter by source
		if params.Source != "" && m.Source != params.Source {
			continue
		}

		// Filter by time (use occurred_at)
		if since != nil && m.OccurredAt.Before(*since) {
			continue
		}
		if before != nil && m.OccurredAt.After(*before) {
			continue
		}

		filtered = append(filtered, r)
	}

	return filtered
}

func formatQueryResults(results []SearchResult, associations map[string][]*Association, detailLevel string) string {
	if len(results) == 0 {
		return "No memories found."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Query Results (%d memories)\n\n", len(results)))

	for i, r := range results {
		m := r.Memory

		// Line 1: Type, ID, importance, relevance score
		sb.WriteString(fmt.Sprintf("%d. [%s] (id: %s, importance: %.2f",
			i+1, m.Type, m.UUID, m.Importance))

		if r.Score > 0 && r.Score != m.Importance {
			sb.WriteString(fmt.Sprintf(", relevance: %.2f", r.Score))
		}
		if m.Confidence != ConfidenceNotApplicable {
			sb.WriteString(fmt.Sprintf(", confidence: %.2f", m.Confidence))
		}
		sb.WriteString(")\n")

		// Line 2: Content
		sb.WriteString(fmt.Sprintf("   %s\n", m.Content))

		// Standard and Full: provenance and temporal info
		if detailLevel == "standard" || detailLevel == "full" {
			// Line 3: Source and message IDs (for tracing)
			var provenance []string
			if m.Source != "" {
				provenance = append(provenance, fmt.Sprintf("source: %s", m.Source))
			}
			if m.SourceMessage != "" {
				provenance = append(provenance, fmt.Sprintf("message: %s", m.SourceMessage))
			}
			if m.Channel != "" {
				provenance = append(provenance, fmt.Sprintf("channel: %s", m.Channel))
			}
			if len(provenance) > 0 {
				sb.WriteString(fmt.Sprintf("   %s\n", strings.Join(provenance, " | ")))
			}

			// Line 4: Temporal info
			var temporal []string
			if !m.OccurredAt.IsZero() {
				temporal = append(temporal, fmt.Sprintf("occurred: %s", m.OccurredAt.Format("2006-01-02")))
			}
			if !m.CreatedAt.IsZero() {
				temporal = append(temporal, fmt.Sprintf("created: %s", m.CreatedAt.Format("2006-01-02")))
			}
			if m.Emotion != "" {
				temporal = append(temporal, fmt.Sprintf("emotion: %s", m.Emotion))
			}
			if len(temporal) > 0 {
				sb.WriteString(fmt.Sprintf("   %s\n", strings.Join(temporal, " | ")))
			}
		}

		// Full only: additional details
		if detailLevel == "full" {
			var extra []string
			if m.SourceSession != "" {
				extra = append(extra, fmt.Sprintf("session: %s", m.SourceSession))
			}
			if m.ChatID != "" {
				extra = append(extra, fmt.Sprintf("chat: %s", m.ChatID))
			}
			if len(extra) > 0 {
				sb.WriteString(fmt.Sprintf("   %s\n", strings.Join(extra, " | ")))
			}

			// Access stats
			var stats []string
			stats = append(stats, fmt.Sprintf("accessed: %d times", m.AccessCount))
			if !m.LastAccessedAt.IsZero() {
				stats = append(stats, fmt.Sprintf("last: %s", m.LastAccessedAt.Format("2006-01-02 15:04")))
			}
			if !m.UpdatedAt.IsZero() && m.UpdatedAt != m.CreatedAt {
				stats = append(stats, fmt.Sprintf("updated: %s", m.UpdatedAt.Format("2006-01-02")))
			}
			if m.Forgotten {
				stats = append(stats, "FORGOTTEN")
			}
			sb.WriteString(fmt.Sprintf("   %s\n", strings.Join(stats, " | ")))
		}

		// Summary only: just emotion inline if present
		if detailLevel == "summary" && m.Emotion != "" {
			sb.WriteString(fmt.Sprintf("   emotion: %s\n", m.Emotion))
		}

		// Show associations if available (all levels)
		if assocs, ok := associations[m.UUID]; ok && len(assocs) > 0 {
			sb.WriteString("   associations:\n")
			for _, a := range assocs {
				sb.WriteString(fmt.Sprintf("     → %s: %s\n", a.RelationType, a.TargetID))
			}
		}

		sb.WriteString("\n")
	}

	return sb.String()
}
