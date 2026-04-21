package transcript

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/session"
	transcriptpkg "github.com/roelfdiedericks/goclaw/internal/transcript"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// Tool provides search and query access to conversation history.
type Tool struct {
	manager    *transcriptpkg.Manager
	store      session.Store
	compactor  *session.CompactionManager
	lcmEnabled bool
}

// NewTool creates a new transcript tool. The compactor is optional; when nil
// the `stats` action still returns transcript indexer counters but omits the
// LCM DAG/config/preset/semantics blocks.
func NewTool(manager *transcriptpkg.Manager, store session.Store, compactor *session.CompactionManager, lcmEnabled bool) *Tool {
	return &Tool{manager: manager, store: store, compactor: compactor, lcmEnabled: lcmEnabled}
}

func (t *Tool) Name() string {
	return "transcript"
}

func (t *Tool) Description() string {
	return "Search and recall from conversation transcripts, including compacted history. Actions: semantic (vector search over message embeddings), recent (latest N messages), search (supports matchType exact|semantic|hybrid), gaps (conversation breaks), stats (transcript indexer counters AND full LCM/DAG state: active preset, effective config, preset catalog with descriptions, un-parented backlog, next condense tick, and a field glossary + drift-signal semantics the agent can use to diagnose recall behavior and suggest preset tuning to the user), get_messages (fetch messages by ID), grep_summaries (FTS5 search across compacted summaries; FTS5 defaults to AND matching, so keep queries short and use 1-3 distinctive terms or one quoted phrase; sort recency|relevance|hybrid), describe (inspect one compacted summary by ID: kind, depth, lineage, content), expand (drill into compacted summaries' children and raw source messages, token-capped, accepts summaryIds or query; pending summaries return raw messages). Prefer grep_summaries -> describe -> expand for compacted history. Keep summary IDs out of user-facing prose unless asked. Call `stats` before making LCM tuning suggestions so suggestions are grounded in live state and the real preset catalog."
}

func (t *Tool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"semantic", "recent", "search", "gaps", "stats", "get_messages", "grep_summaries", "describe", "expand"},
				"description": "Action to perform: semantic/recent/search/gaps/stats/get_messages, plus compacted-history recall actions grep_summaries, describe, expand",
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
			"message_ids": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "For 'get_messages' action: array of message IDs to retrieve (from memory source_message field)",
			},
			"summaryId": map[string]any{
				"type":        "string",
				"description": "For 'describe' action: one summary ID from context, in the form sum_<id>",
			},
			"summaryIds": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "For 'expand' action: one or more summary IDs from context, in the form sum_<id>",
			},
			"tokenCap": map[string]any{
				"type":        "integer",
				"description": "For 'expand' action: maximum token budget for returned expansion text (default 4000)",
			},
			"maxDepth": map[string]any{
				"type":        "integer",
				"description": "For 'expand' action: maximum child-summary recursion depth (default 3)",
			},
			"includeMessages": map[string]any{
				"type":        "boolean",
				"description": "For 'expand' action: include raw source messages for non-pending summaries (default false)",
			},
			"sort": map[string]any{
				"type":        "string",
				"enum":        []string{"recency", "relevance", "hybrid"},
				"description": "For 'grep_summaries' action: result ordering (default recency)",
			},
			"mode": map[string]any{
				"type":        "string",
				"enum":        []string{"full_text", "regex"},
				"description": "For 'grep_summaries' action: full_text uses FTS5, regex runs Go regexp over summaries (default full_text)",
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

	// For get_messages action
	MessageIDs []string `json:"message_ids"`

	// For LCM actions
	SummaryID       string   `json:"summaryId"`
	SummaryIDs      []string `json:"summaryIds"`
	TokenCap        int      `json:"tokenCap"`
	MaxDepth        int      `json:"maxDepth"`
	IncludeMessages bool     `json:"includeMessages"`
	Sort            string   `json:"sort"`
	Mode            string   `json:"mode"`
}

func (t *Tool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var params transcriptInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	if params.Action == "" {
		return nil, fmt.Errorf("action is required")
	}

	// Get user context for scoping
	sessionCtx := types.GetSessionContext(ctx)
	userID := ""
	transcriptScope := "own" // Default to own (restrictive)
	sessionKey := session.PrimarySession
	if sessionCtx != nil && sessionCtx.User != nil {
		userID = sessionCtx.User.ID
		// Use TranscriptScope from session context if set, otherwise fall back to owner check
		if sessionCtx.TranscriptScope != "" {
			transcriptScope = sessionCtx.TranscriptScope
		} else if sessionCtx.User.IsOwner() {
			transcriptScope = "all" // Legacy: owner gets all access
		}
	}
	if sessionCtx != nil && sessionCtx.SessionKey != "" {
		sessionKey = sessionCtx.SessionKey
	}

	// Convert scope to isOwner for existing code (all = owner-like access)
	isOwner := transcriptScope == "all"

	L_debug("transcript: executing",
		"action", params.Action,
		"userID", userID,
		"transcriptScope", transcriptScope,
		"isOwner", isOwner,
	)

	if t.manager == nil {
		result, _ := marshalOutput(map[string]string{
			"error": "transcript manager not available",
		})
		return types.TextResult(result), nil
	}

	var result string
	var err error

	switch params.Action {
	case "semantic":
		result, err = t.executeSemantic(ctx, params, userID, isOwner)
	case "recent":
		result, err = t.executeRecent(ctx, params, userID, isOwner)
	case "search":
		result, err = t.executeSearch(ctx, params, userID, isOwner)
	case "gaps":
		result, err = t.executeGaps(ctx, params, userID, isOwner)
	case "stats":
		result, err = t.executeStats(ctx, sessionKey)
	case "get_messages":
		result, err = t.executeGetMessages(ctx, params, userID, isOwner)
	case "grep_summaries":
		result, err = t.executeGrepSummaries(ctx, params, sessionKey)
	case "describe":
		result, err = t.executeDescribe(ctx, params, sessionKey)
	case "expand":
		result, err = t.executeExpand(ctx, params, sessionKey, userID, isOwner)
	default:
		return nil, fmt.Errorf("unknown action: %s", params.Action)
	}

	if err != nil {
		return nil, err
	}
	return types.TextResult(result), nil
}

func (t *Tool) executeSemantic(ctx context.Context, params transcriptInput, userID string, isOwner bool) (string, error) {
	if params.Query == "" {
		return "", fmt.Errorf("query is required for semantic search")
	}

	opts := transcriptpkg.DefaultSearchOptions()
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
		"query", truncate(params.Query, 30),
		"results", len(results),
	)

	return marshalOutput(map[string]any{
		"results": formatSearchResults(results),
		"count":   len(results),
	})
}

func (t *Tool) executeRecent(ctx context.Context, params transcriptInput, userID string, isOwner bool) (string, error) {
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

func (t *Tool) executeSearch(ctx context.Context, params transcriptInput, userID string, isOwner bool) (string, error) {
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
		"query", truncate(params.Query, 30),
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
			"query", truncate(params.Query, 30),
			"results", len(entries),
		)

		return marshalOutput(map[string]any{
			"results":   formatRecentEntries(entries), // Same format as recent
			"count":     len(entries),
			"matchType": "exact",
		})

	case "semantic":
		// Pure vector search on chunks
		opts := transcriptpkg.SearchOptions{
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
			"query", truncate(params.Query, 30),
			"results", len(results),
		)

		return marshalOutput(map[string]any{
			"results":   formatSearchResults(results),
			"count":     len(results),
			"matchType": "semantic",
		})

	default: // "hybrid"
		// Hybrid search with exact match boost
		opts := transcriptpkg.SearchOptions{
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
			"query", truncate(params.Query, 30),
			"results", len(results),
		)

		return marshalOutput(map[string]any{
			"results":   formatSearchResults(results),
			"count":     len(results),
			"matchType": "hybrid",
		})
	}
}

func (t *Tool) executeGaps(ctx context.Context, params transcriptInput, userID string, isOwner bool) (string, error) {
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

func (t *Tool) executeStats(ctx context.Context, sessionKey string) (string, error) {
	transcriptStats := t.manager.Stats()

	var dagStats *session.CompactionDAGStats
	if t.compactor != nil && sessionKey != "" {
		if stats, err := t.compactor.BuildDAGStatsForSession(ctx, sessionKey); err == nil {
			dagStats = &stats
		} else {
			L_warn("transcript: BuildDAGStatsForSession failed for stats action",
				"sessionKey", sessionKey, "error", err)
		}
	}

	payload := assembleLCMStatsPayload(transcriptStats, t.compactor, dagStats)
	return marshalOutput(payload)
}

// assembleLCMStatsPayload is the pure composition step: it takes pre-fetched
// transcript indexer stats and (optional) DAG stats, plus the manager, and
// builds the composite JSON-friendly map the agent sees. Split out from
// executeStats so it is cheaply unit-testable without real stores.
func assembleLCMStatsPayload(
	transcriptStats transcriptpkg.TranscriptStats,
	compactor *session.CompactionManager,
	dagStats *session.CompactionDAGStats,
) map[string]any {
	payload := map[string]any{
		"totalChunks":             transcriptStats.TotalChunks,
		"chunksWithEmbeddings":    transcriptStats.ChunksWithEmbeddings,
		"chunksNeedingEmbeddings": transcriptStats.ChunksNeedingEmbeddings,
		"pendingMessages":         transcriptStats.PendingMessages,
		"chunksIndexedSession":    transcriptStats.ChunksIndexedSession,
		"lastSync":                transcriptStats.LastSync.Format(time.RFC3339),
		"provider":                transcriptStats.Provider,
	}

	lcmEnabled := compactor != nil && compactor.IsLCMEnabled()
	lcmBlock := map[string]any{
		"enabled": lcmEnabled,
	}
	if compactor != nil {
		lcmBlock["activePreset"] = compactor.Preset()
		lcmBlock["config"] = compactor.LCMConfigSnapshot()
	}
	if dagStats != nil {
		lcmBlock["dag"] = renderDAGStats(*dagStats)
	}
	payload["lcm"] = lcmBlock

	payload["presets"] = renderPresetCatalog(session.LCMPresetCatalog())
	payload["semantics"] = map[string]any{
		"injectionModes":  session.LCMInjectionModeDescriptions,
		"fieldGlossary":   session.LCMFieldGlossary,
		"catchUpBehavior": session.LCMCatchUpBehaviorDescription,
		"driftSignals":    session.LCMDriftSignals,
	}
	return payload
}

func renderDAGStats(s session.CompactionDAGStats) map[string]any {
	unparentedByDepth := map[string]int{}
	for d, n := range s.UnparentedCondensedByDepth {
		unparentedByDepth[fmt.Sprintf("%d", d)] = n
	}
	condensedByDepth := map[string]int{}
	for d, n := range s.CondensedByDepth {
		condensedByDepth[fmt.Sprintf("%d", d)] = n
	}
	nextTick := map[string]any{
		"batchSize":   s.NextBatchSize,
		"newDepth":    s.NextBatchNewDepth,
		"description": describeNextTick(s),
	}
	return map[string]any{
		"leaves":                     s.Leaves,
		"condensed":                  s.Condensed,
		"condensedByDepth":           condensedByDepth,
		"unparentedLeaves":           s.UnparentedLeaves,
		"unparentedCondensedByDepth": unparentedByDepth,
		"maxDepth":                   s.MaxDepth,
		"pending":                    s.Pending,
		"ftsRows":                    s.FTSRows,
		"nextTick":                   nextTick,
	}
}

func describeNextTick(s session.CompactionDAGStats) string {
	if s.NextBatchSize <= 0 {
		return "idle: no un-parented candidates meet fanout threshold"
	}
	unit := "leaves"
	if s.NextBatchNewDepth > 1 {
		unit = fmt.Sprintf("depth-%d condensed nodes", s.NextBatchNewDepth-1)
	}
	return fmt.Sprintf("condense %d %s -> depth-%d", s.NextBatchSize, unit, s.NextBatchNewDepth)
}

func renderPresetCatalog(presets []session.LCMPresetDef) []map[string]any {
	out := make([]map[string]any, 0, len(presets))
	for _, p := range presets {
		out = append(out, map[string]any{
			"name":        p.Name,
			"label":       p.Label,
			"description": p.Description,
			"fields": map[string]any{
				"summaryInjectionMode":     p.SummaryInjectionMode,
				"maxInjectedSummaryTokens": p.MaxInjectedSummaryTokens,
				"summaryMaxOverageFactor":  p.SummaryMaxOverageFactor,
				"freshTailCount":           p.FreshTailCount,
				"freshTailMaxTokens":       p.FreshTailMaxTokens,
				"leafMinFanout":            p.LeafMinFanout,
				"condensedMinFanout":       p.CondensedMinFanout,
				"incrementalMaxDepth":      p.IncrementalMaxDepth,
				"leafTargetTokens":         p.LeafTargetTokens,
				"condensedTargetTokens":    p.CondensedTargetTokens,
			},
		})
	}
	return out
}

func (t *Tool) executeGetMessages(ctx context.Context, params transcriptInput, userID string, isOwner bool) (string, error) {
	if len(params.MessageIDs) == 0 {
		return "", fmt.Errorf("message_ids is required for get_messages action")
	}

	L_debug("transcript: get_messages",
		"ids", len(params.MessageIDs),
		"userID", userID,
	)

	messages, err := t.manager.GetMessagesByIDs(ctx, params.MessageIDs, userID, isOwner)
	if err != nil {
		L_error("transcript: get_messages failed", "error", err)
		return marshalOutput(map[string]any{
			"error":    err.Error(),
			"messages": []any{},
		})
	}

	L_info("transcript: get_messages completed",
		"requested", len(params.MessageIDs),
		"found", len(messages),
	)

	// Format messages for output
	formatted := make([]map[string]any, len(messages))
	for i, msg := range messages {
		formatted[i] = map[string]any{
			"id":        msg.ID,
			"timestamp": msg.Timestamp.Format(time.RFC3339),
			"role":      msg.Role,
			"content":   msg.Content,
		}
		if msg.Source != "" {
			formatted[i]["source"] = msg.Source
		}
		if msg.SessionKey != "" {
			formatted[i]["session"] = msg.SessionKey
		}
	}

	return marshalOutput(map[string]any{
		"messages":  formatted,
		"count":     len(messages),
		"requested": len(params.MessageIDs),
	})
}

func (t *Tool) executeGrepSummaries(ctx context.Context, params transcriptInput, sessionKey string) (string, error) {
	if !t.lcmEnabled || t.store == nil {
		return t.lcmDisabledResult()
	}
	if params.Query == "" {
		return "", fmt.Errorf("query is required for grep_summaries")
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}
	mode := session.CompactionSearchMode(params.Mode)
	if mode == "" {
		mode = session.CompactionSearchModeFTS
	}
	sort := session.CompactionSearchSort(params.Sort)
	if sort == "" {
		sort = session.CompactionSearchSortRecency
	}

	results, err := t.store.SearchCompactionsFTS(ctx, sessionKey, params.Query, limit, mode, sort)
	if err != nil {
		return marshalOutput(map[string]any{
			"error":   err.Error(),
			"results": []any{},
		})
	}

	formatted := make([]map[string]any, 0, len(results))
	for _, result := range results {
		entry := map[string]any{
			"id":        session.FormatSummaryID(result.Compaction.ID),
			"kind":      result.Compaction.Kind,
			"depth":     result.Compaction.Depth,
			"timestamp": result.Compaction.Timestamp.Format(time.RFC3339),
			"preview":   truncateContent(result.Compaction.Summary, 400),
			"matchType": result.MatchSource,
		}
		if result.Relevance != 0 {
			entry["score"] = fmt.Sprintf("%.4f", result.Relevance)
		}
		formatted = append(formatted, entry)
	}

	return marshalOutput(map[string]any{
		"results": resultsOrEmpty(formatted),
		"count":   len(formatted),
		"query":   params.Query,
		"mode":    mode,
		"sort":    sort,
	})
}

func (t *Tool) executeDescribe(ctx context.Context, params transcriptInput, sessionKey string) (string, error) {
	if !t.lcmEnabled || t.store == nil {
		return t.lcmDisabledResult()
	}

	summaryID := params.SummaryID
	if summaryID == "" && len(params.SummaryIDs) > 0 {
		summaryID = params.SummaryIDs[0]
	}
	if summaryID == "" {
		return "", fmt.Errorf("summaryId is required for describe")
	}

	id, err := session.ParseSummaryID(summaryID)
	if err != nil {
		return "", err
	}
	comp, err := t.store.GetCompaction(ctx, id)
	if err != nil {
		return "", err
	}
	if comp == nil || comp.SessionKey != sessionKey {
		return "", fmt.Errorf("summary not found: %s", summaryID)
	}

	children := make([]string, 0, len(comp.ChildCompactionIDs))
	for _, childID := range comp.ChildCompactionIDs {
		children = append(children, session.FormatSummaryID(childID))
	}

	result := map[string]any{
		"id":                session.FormatSummaryID(comp.ID),
		"kind":              session.CompactionKindOrLeaf(comp.Kind),
		"depth":             comp.Depth,
		"timestamp":         comp.Timestamp.Format(time.RFC3339),
		"summary":           comp.Summary,
		"needsSummaryRetry": comp.NeedsSummaryRetry,
		"sourceMessages":    len(comp.SourceMessageIDs),
		"children":          children,
		"firstKeptEntryID":  comp.FirstKeptEntryID,
	}
	if comp.EarliestMessageAt != nil {
		result["earliestAt"] = comp.EarliestMessageAt.UTC().Format(time.RFC3339)
	}
	if comp.LatestMessageAt != nil {
		result["latestAt"] = comp.LatestMessageAt.UTC().Format(time.RFC3339)
	}
	if len(comp.SourceMessageIDs) > 0 {
		result["messageRange"] = fmt.Sprintf("%s..%s",
			session.FormatMessageID(comp.SourceMessageIDs[0]),
			session.FormatMessageID(comp.SourceMessageIDs[len(comp.SourceMessageIDs)-1]),
		)
	}
	return marshalOutput(result)
}

func (t *Tool) executeExpand(ctx context.Context, params transcriptInput, sessionKey, userID string, isOwner bool) (string, error) {
	if !t.lcmEnabled || t.store == nil {
		return t.lcmDisabledResult()
	}

	tokenCap := params.TokenCap
	if tokenCap <= 0 {
		tokenCap = 4000
	}
	maxDepth := params.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 3
	}

	targets, err := t.resolveExpandTargets(ctx, params, sessionKey)
	if err != nil {
		return "", err
	}
	if len(targets) == 0 {
		return "", fmt.Errorf("no summaries matched")
	}

	estimator := session.GetTokenEstimator()
	remaining := tokenCap
	var b strings.Builder
	truncated := false
	fellBackToRaw := false
	seen := make(map[string]bool, len(targets))

	appendText := func(text string) {
		if truncated || strings.TrimSpace(text) == "" {
			return
		}
		tokens := estimator.EstimateTokens(text)
		if tokens <= remaining {
			b.WriteString(text)
			remaining -= tokens
			return
		}
		maxChars := remaining * 4
		if maxChars > len(text) {
			maxChars = len(text)
		}
		if maxChars > 0 {
			b.WriteString(text[:maxChars])
			b.WriteString("\n[truncated]\n")
		}
		truncated = true
		remaining = 0
	}

	appendText("Expanded compacted history:\n\n")
	for _, comp := range targets {
		if comp.NeedsSummaryRetry && len(comp.SourceMessageIDs) > 0 {
			fellBackToRaw = true
			L_warn("transcript: expand falling back to raw messages", "compactionID", comp.ID, "reason", "pending_summary")
		}
		t.renderExpandedCompaction(ctx, appendText, sessionKey, comp, maxDepth, params.IncludeMessages, userID, isOwner, seen)
		if truncated {
			break
		}
	}

	if truncated {
		L_warn("transcript: expand result truncated", "requestedTokens", tokenCap, "returnedTokens", tokenCap-remaining)
	}

	return marshalOutput(map[string]any{
		"summaryIds":      prefixedCompactionIDs(targets),
		"tokenCap":        tokenCap,
		"returnedTokens":  tokenCap - remaining,
		"truncated":       truncated,
		"pendingFallback": fellBackToRaw,
		"text":            strings.TrimSpace(b.String()),
	})
}

func (t *Tool) resolveExpandTargets(ctx context.Context, params transcriptInput, sessionKey string) ([]session.StoredCompaction, error) {
	if len(params.SummaryIDs) > 0 {
		targets := make([]session.StoredCompaction, 0, len(params.SummaryIDs))
		for _, rawID := range params.SummaryIDs {
			id, err := session.ParseSummaryID(rawID)
			if err != nil {
				return nil, err
			}
			comp, err := t.store.GetCompaction(ctx, id)
			if err != nil {
				return nil, err
			}
			if comp != nil && comp.SessionKey == sessionKey {
				targets = append(targets, *comp)
			}
		}
		return targets, nil
	}
	if params.SummaryID != "" {
		id, err := session.ParseSummaryID(params.SummaryID)
		if err != nil {
			return nil, err
		}
		comp, err := t.store.GetCompaction(ctx, id)
		if err != nil {
			return nil, err
		}
		if comp == nil || comp.SessionKey != sessionKey {
			return nil, nil
		}
		return []session.StoredCompaction{*comp}, nil
	}
	if params.Query == "" {
		return nil, fmt.Errorf("expand requires summaryIds, summaryId, or query")
	}

	results, err := t.store.SearchCompactionsFTS(ctx, sessionKey, params.Query, 1, session.CompactionSearchModeFTS, session.CompactionSearchSortRecency)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}
	return []session.StoredCompaction{results[0].Compaction}, nil
}

func (t *Tool) renderExpandedCompaction(
	ctx context.Context,
	appendText func(string),
	sessionKey string,
	comp session.StoredCompaction,
	maxDepth int,
	includeMessages bool,
	userID string,
	isOwner bool,
	seen map[string]bool,
) {
	if seen[comp.ID] || maxDepth < 0 {
		return
	}
	seen[comp.ID] = true

	appendText(fmt.Sprintf("[summary %s kind=%s depth=%d pending=%t]\n", session.FormatSummaryID(comp.ID), session.CompactionKindOrLeaf(comp.Kind), comp.Depth, comp.NeedsSummaryRetry))
	if comp.EarliestMessageAt != nil || comp.LatestMessageAt != nil {
		earliest := ""
		latest := ""
		if comp.EarliestMessageAt != nil {
			earliest = comp.EarliestMessageAt.UTC().Format(time.RFC3339)
		}
		if comp.LatestMessageAt != nil {
			latest = comp.LatestMessageAt.UTC().Format(time.RFC3339)
		}
		appendText(fmt.Sprintf("window: %s -> %s\n", earliest, latest))
	}
	appendText(comp.Summary + "\n\n")

	shouldIncludeMessages := includeMessages || comp.NeedsSummaryRetry
	if shouldIncludeMessages && len(comp.SourceMessageIDs) > 0 {
		messages, err := t.store.GetMessagesByIDs(ctx, sessionKey, comp.SourceMessageIDs)
		if err == nil && len(messages) > 0 {
			appendText("source messages:\n")
			for _, msg := range messages {
				if !isOwner && msg.UserID != "" && msg.UserID != userID {
					continue
				}
				content := msg.Content
				if msg.Role == "tool_result" && msg.ToolResult != "" {
					content = msg.ToolResult
				}
				appendText(fmt.Sprintf("- [%s] %s %s: %s\n",
					session.FormatMessageID(msg.ID),
					msg.Role,
					msg.Timestamp.UTC().Format(time.RFC3339),
					truncateContent(content, 800),
				))
			}
			appendText("\n")
		}
	} else if len(comp.SourceMessageIDs) > 0 {
		appendText(fmt.Sprintf("source messages: %d (%s..%s)\n\n",
			len(comp.SourceMessageIDs),
			session.FormatMessageID(comp.SourceMessageIDs[0]),
			session.FormatMessageID(comp.SourceMessageIDs[len(comp.SourceMessageIDs)-1]),
		))
	}

	if maxDepth == 0 || len(comp.ChildCompactionIDs) == 0 {
		return
	}
	children, err := t.store.GetCompactionChildren(ctx, comp.ID)
	if err != nil || len(children) == 0 {
		return
	}
	appendText("children:\n")
	for _, child := range children {
		appendText(fmt.Sprintf("- %s\n", session.FormatSummaryID(child.ID)))
	}
	appendText("\n")
	for _, child := range children {
		t.renderExpandedCompaction(ctx, appendText, sessionKey, child, maxDepth-1, includeMessages, userID, isOwner, seen)
	}
}

func (t *Tool) lcmDisabledResult() (string, error) {
	return "", fmt.Errorf("LCM disabled: grep_summaries, describe, and expand are unavailable")
}

// buildQueryFilter creates a QueryFilter from transcript input parameters
func buildQueryFilter(params transcriptInput) *transcriptpkg.QueryFilter {
	filter := &transcriptpkg.QueryFilter{
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
func formatSearchResults(results []transcriptpkg.SearchResult) []map[string]any {
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
func formatRecentEntries(entries []transcriptpkg.RecentEntry) []map[string]any {
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
func formatGapEntries(gaps []transcriptpkg.GapEntry) []map[string]any {
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

// truncate truncates a string for display
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func resultsOrEmpty(results []map[string]any) []map[string]any {
	if results == nil {
		return []map[string]any{}
	}
	return results
}

func prefixedCompactionIDs(compactions []session.StoredCompaction) []string {
	ids := make([]string, 0, len(compactions))
	for _, comp := range compactions {
		ids = append(ids, session.FormatSummaryID(comp.ID))
	}
	return ids
}

// marshalOutput marshals output with indentation
func marshalOutput(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
