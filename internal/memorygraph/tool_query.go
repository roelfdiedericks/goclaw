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
	return "Query the memory graph. Modes: " +
		"hybrid (default) - semantic+keyword search over memory content, requires query; " +
		"recent / important - ordering-only, optional filters; " +
		"typed - by memory_type (routine, fact, goal, etc.), requires memory_type; " +
		"related - graph traversal from a seed memory, requires related_to UUID; " +
		"triggers - audit log of memory-trigger fires (when routines nudged, stayed silent, were skipped, or errored). " +
		"Use triggers to answer \"did I remind the user?\" or debug missed nudges. " +
		"Triggers filters: memory_uuid, outcome, since, before, max_results. Hybrid/recurrence filters are ignored in triggers mode. " +
		"Routine results in hybrid/recent/important/typed/related modes include a recurrence: line with cadence, location, person, and next occurrence when available."
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
				"enum":        []string{"hybrid", "recent", "important", "typed", "related", "triggers"},
				"default":     "hybrid",
				"description": "Query mode. hybrid: semantic+keyword search (requires query). recent/important: ordering-only. typed: filter by memory_type. related: graph traversal from related_to. triggers: audit log of memory-trigger fires (use filters memory_uuid, outcome, since, before). Hybrid/recurrence filters are ignored in triggers mode.",
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
				"description": "Only memories whose occurred_at is after this time. ISO timestamp or relative: '24h', '7d', '30d'. In triggers mode, filters on fired_at instead.",
			},
			"before": map[string]any{
				"type":        "string",
				"description": "Only memories whose occurred_at is before this time. ISO timestamp or relative. In triggers mode, filters on fired_at instead.",
			},
			"happens_after": map[string]any{
				"type":        "string",
				"description": "Only memories with happens_at after this time. Use for upcoming scheduled items. Format: ISO timestamp or date.",
			},
			"happens_before": map[string]any{
				"type":        "string",
				"description": "Only memories with happens_at before this time. Use for scheduled items within an explicit upper bound. Format: ISO timestamp or date.",
			},
			"happens_within": map[string]any{
				"type":        "string",
				"description": "Only memories with happens_at between now and this future window, for example '24h', '7d', or '30d'.",
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
				"enum":        []string{"recent", "importance", "most_accessed", "scheduled"},
				"description": "Sort order. 'scheduled' orders by happens_at for dated events and deadlines.",
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
			"recurs_on_day": map[string]any{
				"type":        "string",
				"description": "Only routines that recur on this day name (lowercase full name: \"monday\"..\"sunday\"; short forms / ISO numbers 1..7 also accepted). Routine-scoped: non-routine memories are excluded when this filter is set. Ignored in triggers mode.",
			},
			"recurs_on_days": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Only routines that recur on any of the listed days. Routine-scoped; non-routines excluded when set. Ignored in triggers mode.",
			},
			"recurs_today": map[string]any{
				"type":        "boolean",
				"description": "Only routines that recur on the current day (server-local tz). Routine-scoped; non-routines excluded when set. Ignored in triggers mode.",
			},
			"recurs_at_time": map[string]any{
				"type":        "string",
				"description": "Only routines whose [time_start, time_end] overlaps the given window. Format: \"HH:MM-HH:MM\" (server-local tz). Routine-scoped. Ignored in triggers mode.",
			},
			"next_occurrence_within": map[string]any{
				"type":        "string",
				"description": "Only routines whose next occurrence falls within the given future window (e.g. \"24h\", \"7d\"). Routine-scoped; honours starts_on / ends_on / skip_dates. Ignored in triggers mode.",
			},
			"involves_person": map[string]any{
				"type":        "string",
				"description": "Only routines whose person field equals this value (case-sensitive). Routine-scoped. Ignored in triggers mode.",
			},
			"memory_uuid": map[string]any{
				"type":        "string",
				"description": "Memory UUID. In triggers mode, scopes the audit log to fires of this memory.",
			},
			"outcome": map[string]any{
				"type": "string",
				"enum": []string{"fired", "silent", "missed_grace", "error"},
				"description": "Filter triggers mode by outcome. Values: " +
					"fired (agent ran and produced output - the common success case); " +
					"silent (agent ran but returned SILENT_OK - chose not to speak); " +
					"missed_grace (poller skipped firing because scheduled time was older than the MissedGrace window - agent did NOT run); " +
					"error (invocation failed). " +
					"Only applies when mode=triggers; ignored otherwise.",
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
	HappensAfter        string   `json:"happens_after,omitempty"`
	HappensBefore       string   `json:"happens_before,omitempty"`
	HappensWithin       string   `json:"happens_within,omitempty"`
	RelatedTo           string   `json:"related_to,omitempty"`
	MaxResults          int      `json:"max_results,omitempty"`
	SortBy              string   `json:"sort_by,omitempty"`
	IncludeAssociations bool     `json:"include_associations,omitempty"`
	DetailLevel         string   `json:"detail_level,omitempty"`

	// Routine recurrence filters. All routine-scoped: when any is set,
	// non-routine memories are excluded from results.
	RecursOnDay          string   `json:"recurs_on_day,omitempty"`
	RecursOnDays         []string `json:"recurs_on_days,omitempty"`
	RecursToday          bool     `json:"recurs_today,omitempty"`
	RecursAtTime         string   `json:"recurs_at_time,omitempty"`
	NextOccurrenceWithin string   `json:"next_occurrence_within,omitempty"`
	InvolvesPerson       string   `json:"involves_person,omitempty"`

	// Triggers-mode filters. MemoryUUID is also usable for direct UUID lookups
	// in other modes (future extension); Outcome is triggers-mode only.
	MemoryUUID string `json:"memory_uuid,omitempty"`
	Outcome    string `json:"outcome,omitempty"`
}

// hasRecurrenceFilter reports whether any routine-recurrence filter is populated.
func (q QueryParams) hasRecurrenceFilter() bool {
	return q.RecursOnDay != "" ||
		len(q.RecursOnDays) > 0 ||
		q.RecursToday ||
		q.RecursAtTime != "" ||
		q.NextOccurrenceWithin != "" ||
		q.InvolvesPerson != ""
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
	if params.Mode == "triggers" && params.Outcome != "" {
		switch params.Outcome {
		case "fired", "silent", "missed_grace", "error":
		default:
			return types.ErrorResult(fmt.Sprintf("invalid outcome: %s (expected fired|silent|missed_grace|error)", params.Outcome)), nil
		}
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

	// Triggers mode: audit log of memory-trigger fires. Diverges from the rest
	// of the query pipeline because it hits a different table and renders a
	// distinct output shape.
	if params.Mode == "triggers" {
		return t.executeTriggersMode(params, username, maxResults)
	}

	// Parse time filters
	var sinceTime, beforeTime *time.Time
	var happensAfter, happensBefore *time.Time
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
	if params.HappensAfter != "" {
		if t, err := parseMemoryToolTime(params.HappensAfter); err == nil {
			happensAfter = &t
		}
	}
	if params.HappensBefore != "" {
		if t, err := parseMemoryToolTime(params.HappensBefore); err == nil {
			happensBefore = &t
		}
	}
	if params.HappensWithin != "" {
		if duration, err := parseFutureWindow(params.HappensWithin); err == nil {
			now := time.Now()
			if happensAfter == nil {
				happensAfter = &now
			}
			end := now.Add(duration)
			if happensBefore == nil || end.Before(*happensBefore) {
				happensBefore = &end
			}
		}
	}

	L_debug("memory_graph_query: executing",
		"mode", params.Mode,
		"query", truncateStr(params.Query, 50),
		"type", params.MemoryType,
		"emotion", params.Emotion,
		"happensAfter", params.HappensAfter,
		"happensBefore", params.HappensBefore,
		"happensWithin", params.HappensWithin,
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
			results = filterResults(results, params, sinceTime, beforeTime, happensAfter, happensBefore)
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
			results = filterResults(results, params, sinceTime, beforeTime, happensAfter, happensBefore)
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
		if happensAfter != nil {
			q.SinceHappens(*happensAfter)
		}
		if happensBefore != nil {
			q.UntilHappens(*happensBefore)
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
				case "scheduled":
					q.OrderBy("happens_at").Ascending().ThenBy("importance", true)
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

	// Apply routine-recurrence filters (routine-scoped; excludes non-routines).
	if params.hasRecurrenceFilter() {
		filtered, ferr := filterRoutineRecurrence(t.manager.Store(), results, params)
		if ferr != nil {
			L_warn("memory_graph_query: recurrence filter failed", "error", ferr)
		} else {
			results = filtered
		}
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

	// Batch-fetch routine metadata for any routine results so the formatter
	// can surface recurrence cadence + next occurrence inline.
	routineMetas := collectRoutineMetas(t.manager.Store(), results)

	// Format output for LLM
	output := formatQueryResults(results, associations, detailLevel, routineMetas)

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

func filterResults(results []SearchResult, params QueryParams, since, before, happensAfter, happensBefore *time.Time) []SearchResult {
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
		if happensAfter != nil || happensBefore != nil {
			if m.HappensAt == nil {
				continue
			}
			if happensAfter != nil && m.HappensAt.Before(*happensAfter) {
				continue
			}
			if happensBefore != nil && m.HappensAt.After(*happensBefore) {
				continue
			}
		}

		filtered = append(filtered, r)
	}

	return filtered
}

func formatQueryResults(results []SearchResult, associations map[string][]*Association, detailLevel string, routineMetas map[string]*RoutineMetadata) string {
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

		// Routine recurrence line (standard + full). Summary keeps output terse.
		if (detailLevel == "standard" || detailLevel == "full") && m.Type == TypeRoutine {
			if meta := routineMetas[m.UUID]; meta != nil {
				if rec := formatRoutineRecurrenceLine(meta, m.NextTriggerAt, detailLevel == "full"); rec != "" {
					sb.WriteString("   ")
					sb.WriteString(rec)
					sb.WriteByte('\n')
				}
			}
		}

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
			if m.HappensAt != nil {
				temporal = append(temporal, fmt.Sprintf("happens: %s", formatMemoryTime(*m.HappensAt)))
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
		if detailLevel == "summary" && m.HappensAt != nil {
			sb.WriteString(fmt.Sprintf("   happens: %s\n", formatMemoryTime(*m.HappensAt)))
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

func parseFutureWindow(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid future window: %s", s)
	}

	var multiplier time.Duration
	switch s[len(s)-1] {
	case 'h':
		multiplier = time.Hour
	case 'd':
		multiplier = 24 * time.Hour
	default:
		return 0, fmt.Errorf("invalid future window unit: %s", s)
	}

	value, err := parseInt(s[:len(s)-1])
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid future window: %s", s)
	}
	return time.Duration(value) * multiplier, nil
}

func formatMemoryTime(t time.Time) string {
	if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 && t.Nanosecond() == 0 {
		return t.Format("2006-01-02")
	}
	return t.Format(time.RFC3339)
}

// collectRoutineMetas batch-fetches RoutineMetadata for any routine memories in
// results. Non-routine entries are skipped. A nil/empty map is returned on
// empty input or on fetch error (non-fatal — formatter simply omits the
// recurrence line).
func collectRoutineMetas(store *Store, results []SearchResult) map[string]*RoutineMetadata {
	if store == nil || len(results) == 0 {
		return nil
	}
	var uuids []string
	for _, r := range results {
		if r.Memory.Type == TypeRoutine {
			uuids = append(uuids, r.Memory.UUID)
		}
	}
	if len(uuids) == 0 {
		return nil
	}
	metas, err := store.GetRoutineMetadataMulti(uuids)
	if err != nil {
		L_warn("routine metadata batch fetch failed", "error", err, "count", len(uuids))
		return nil
	}
	return metas
}

// formatRoutineRecurrenceLine renders a single "recurrence: ..." line for a
// routine result. Returns "" when the metadata carries no structured cadence
// (legacy routine without days/time_start). The full flag adds bounds + skip
// dates for the "full" detail level.
func formatRoutineRecurrenceLine(meta *RoutineMetadata, nextTriggerAt *time.Time, full bool) string {
	if meta == nil {
		return ""
	}
	if len(meta.Days) == 0 && meta.TimeStart == "" {
		return ""
	}

	var parts []string

	var cadence string
	if len(meta.Days) > 0 {
		cadence = strings.Join(shortDayNames(meta.Days), ",")
	}
	if meta.TimeStart != "" {
		tr := meta.TimeStart
		if meta.TimeEnd != "" {
			tr = tr + "-" + meta.TimeEnd
		}
		if cadence != "" {
			cadence = cadence + " @ " + tr
		} else {
			cadence = tr
		}
	}
	if meta.Location != "" {
		cadence = cadence + " @ " + meta.Location
	}
	if cadence != "" {
		parts = append(parts, "recurrence: "+cadence)
	}

	if meta.Person != "" {
		parts = append(parts, "person: "+meta.Person)
	}

	// Next-occurrence / ended handling.
	ended := meta.EndsOn != nil && meta.EndsOn.Before(dateOnly(time.Now()))
	if nextTriggerAt != nil && !nextTriggerAt.IsZero() {
		local := nextTriggerAt.Local()
		parts = append(parts, fmt.Sprintf("next: %s %s", local.Weekday().String()[:3], local.Format("2006-01-02 15:04")))
	} else if ended && meta.EndsOn != nil {
		parts = append(parts, fmt.Sprintf("(ended %s)", meta.EndsOn.Format("2006-01-02")))
	}

	if full {
		if meta.StartsOn != nil || meta.EndsOn != nil {
			var b []string
			if meta.StartsOn != nil {
				b = append(b, "starts "+meta.StartsOn.Format("2006-01-02"))
			}
			if meta.EndsOn != nil {
				b = append(b, "ends "+meta.EndsOn.Format("2006-01-02"))
			}
			parts = append(parts, "bounds: "+strings.Join(b, " / "))
		}
		if len(meta.SkipDates) > 0 {
			parts = append(parts, "skip: "+strings.Join(meta.SkipDates, ", "))
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " | ")
}

// shortDayNames abbreviates full weekday names ("monday" -> "Mon") for compact
// cadence rendering. Unknown entries are passed through unchanged.
func shortDayNames(days []string) []string {
	out := make([]string, 0, len(days))
	for _, d := range days {
		switch strings.ToLower(d) {
		case "monday":
			out = append(out, "Mon")
		case "tuesday":
			out = append(out, "Tue")
		case "wednesday":
			out = append(out, "Wed")
		case "thursday":
			out = append(out, "Thu")
		case "friday":
			out = append(out, "Fri")
		case "saturday":
			out = append(out, "Sat")
		case "sunday":
			out = append(out, "Sun")
		default:
			out = append(out, d)
		}
	}
	return out
}

// filterRoutineRecurrence applies the routine-scoped recurrence filters. Only
// routine memories survive when any recurrence filter is set. Each surviving
// routine must satisfy all supplied filters (AND semantics). Metadata-lookup
// failures are treated as non-matches rather than errors.
func filterRoutineRecurrence(store *Store, results []SearchResult, params QueryParams) ([]SearchResult, error) {
	if !params.hasRecurrenceFilter() {
		return results, nil
	}

	// Normalize day filters once.
	wantDay := dayNameNormalize(params.RecursOnDay)
	if params.RecursOnDay != "" && wantDay == "" {
		return nil, fmt.Errorf("recurs_on_day %q: not a valid day name", params.RecursOnDay)
	}
	wantAnyDay := normalizeDays(params.RecursOnDays)
	if len(params.RecursOnDays) > 0 && len(wantAnyDay) == 0 {
		return nil, fmt.Errorf("recurs_on_days %v: no valid day names", params.RecursOnDays)
	}

	var todayName string
	if params.RecursToday {
		now := time.Now().In(time.Local)
		todayName = dayNameNormalize(now.Weekday().String())
	}

	// Parse optional time window "HH:MM-HH:MM" (server-local).
	var windowStartH, windowStartM, windowEndH, windowEndM int
	windowSet := false
	if params.RecursAtTime != "" {
		parts := strings.SplitN(params.RecursAtTime, "-", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("recurs_at_time %q: want \"HH:MM-HH:MM\"", params.RecursAtTime)
		}
		sh, sm, err := parseClockTime(parts[0])
		if err != nil {
			return nil, fmt.Errorf("recurs_at_time start: %v", err)
		}
		eh, em, err := parseClockTime(parts[1])
		if err != nil {
			return nil, fmt.Errorf("recurs_at_time end: %v", err)
		}
		windowStartH, windowStartM = sh, sm
		windowEndH, windowEndM = eh, em
		windowSet = true
	}

	// Parse next_occurrence_within relative window.
	var nextWithin time.Duration
	if params.NextOccurrenceWithin != "" {
		d, err := parseFutureWindow(params.NextOccurrenceWithin)
		if err != nil {
			return nil, fmt.Errorf("next_occurrence_within: %v", err)
		}
		nextWithin = d
	}

	out := make([]SearchResult, 0, len(results))
	for _, r := range results {
		if r.Memory.Type != TypeRoutine {
			continue
		}
		meta, err := store.GetRoutineMetadata(r.Memory.UUID)
		if err != nil || meta == nil {
			continue
		}

		// recurs_on_day
		if wantDay != "" {
			hit := false
			for _, d := range meta.Days {
				if dayNameNormalize(d) == wantDay {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
		}

		// recurs_on_days (any overlap)
		if len(wantAnyDay) > 0 {
			have := map[string]struct{}{}
			for _, d := range meta.Days {
				if n := dayNameNormalize(d); n != "" {
					have[n] = struct{}{}
				}
			}
			hit := false
			for _, w := range wantAnyDay {
				if _, ok := have[w]; ok {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
		}

		// recurs_today
		if todayName != "" {
			hit := false
			for _, d := range meta.Days {
				if dayNameNormalize(d) == todayName {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
		}

		// recurs_at_time: overlap [time_start, time_end] with [window_start, window_end].
		if windowSet {
			if strings.TrimSpace(meta.TimeStart) == "" {
				continue
			}
			rsH, rsM, err := parseClockTime(meta.TimeStart)
			if err != nil {
				continue
			}
			// Effective routine end: time_end if set, else time_start + duration_minutes,
			// else treat as a single instant (end == start).
			reH, reM := rsH, rsM
			if strings.TrimSpace(meta.TimeEnd) != "" {
				if h, m, err := parseClockTime(meta.TimeEnd); err == nil {
					reH, reM = h, m
				}
			} else if meta.DurationMinutes != nil && *meta.DurationMinutes > 0 {
				totalMin := rsH*60 + rsM + *meta.DurationMinutes
				reH = totalMin / 60
				if reH > 23 {
					reH = 23
				}
				reM = totalMin % 60
			}
			routineStart := rsH*60 + rsM
			routineEnd := reH*60 + reM
			windowStart := windowStartH*60 + windowStartM
			windowEnd := windowEndH*60 + windowEndM
			// Overlap iff start <= otherEnd && otherStart <= end.
			if routineStart > windowEnd || windowStart > routineEnd {
				continue
			}
		}

		// next_occurrence_within
		if nextWithin > 0 {
			next := meta.NextOccurrence(time.Now())
			if next.IsZero() || time.Until(next) > nextWithin {
				continue
			}
		}

		// involves_person
		if params.InvolvesPerson != "" && meta.Person != params.InvolvesPerson {
			continue
		}

		out = append(out, r)
	}
	return out, nil
}

// executeTriggersMode handles mode=triggers by querying memory_triggers_fired.
// Since/Before filter fired_at (not occurred_at). Hybrid/recurrence filters
// are intentionally ignored.
func (t *QueryTool) executeTriggersMode(params QueryParams, username string, maxResults int) (*types.ToolResult, error) {
	qp := TriggerQueryParams{
		MemoryUUID: params.MemoryUUID,
		Username:   username,
		Outcome:    params.Outcome,
		Limit:      maxResults,
	}
	if params.Since != "" {
		if ts, err := parseTimeFilter(params.Since); err == nil {
			qp.Since = &ts
		}
	}
	if params.Before != "" {
		if ts, err := parseTimeFilter(params.Before); err == nil {
			qp.Before = &ts
		}
	}

	L_debug("memory_graph_query: triggers mode",
		"memory_uuid", params.MemoryUUID,
		"outcome", params.Outcome,
		"since", params.Since,
		"before", params.Before,
		"limit", qp.Limit,
		"username", username,
	)

	rows, err := t.manager.Store().QueryTriggersFired(qp)
	if err != nil {
		L_error("memory_graph_query: triggers query failed", "error", err)
		return types.ErrorResult(fmt.Sprintf("triggers query failed: %v", err)), nil
	}

	output := formatTriggersFired(rows)
	L_info("memory_graph_query: triggers completed", "results", len(rows))
	return types.TextResult(output), nil
}

// formatTriggersFired renders the audit-log output. Lag is non-negative by
// construction: the poller only considers rows where next_trigger_at <= now
// and stamps fired_at at claim time, so fired_at >= scheduled_for.
func formatTriggersFired(rows []*TriggerFired) string {
	if len(rows) == 0 {
		return "No trigger fires found."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Trigger Fires (%d)\n\n", len(rows)))

	for i, r := range rows {
		content := r.Content
		if content == "" {
			content = "(memory not found)"
		}

		sb.WriteString(fmt.Sprintf("%d. %s (id: %s)\n", i+1, content, r.MemoryUUID))

		scheduled := r.ScheduledFor.Local().Format("2006-01-02 15:04")
		fired := r.FiredAt.Local().Format("2006-01-02 15:04:05")
		lag := r.FiredAt.Sub(r.ScheduledFor)

		parts := []string{
			fmt.Sprintf("scheduled: %s", scheduled),
			fmt.Sprintf("fired: %s (lag %s)", fired, humaniseLagShort(lag)),
		}
		if r.Outcome != "" {
			parts = append(parts, fmt.Sprintf("outcome: %s", r.Outcome))
		}
		if r.RunID != "" {
			parts = append(parts, fmt.Sprintf("run: %s", r.RunID))
		}
		sb.WriteString("   ")
		sb.WriteString(strings.Join(parts, " | "))
		sb.WriteByte('\n')
	}

	return sb.String()
}

// humaniseLagShort prints a compact "2s", "45s", "3m", "1h05m" style string for
// trigger lag output. Negative values shouldn't happen by construction, but
// format them as "-<value>" if they ever do.
func humaniseLagShort(d time.Duration) string {
	if d < 0 {
		return "-" + humaniseLagShort(-d)
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	hours := int(d.Hours())
	mins := int(d.Minutes()) - hours*60
	if mins == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh%02dm", hours, mins)
}
