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

// StoreTool saves memories with optional associations to the memory graph.
type StoreTool struct {
	manager *Manager
}

// NewStoreTool creates a new store tool with explicit manager reference.
func NewStoreTool(mgr *Manager) *StoreTool {
	return &StoreTool{manager: mgr}
}

func (t *StoreTool) Name() string {
	return "memory_graph_store"
}

func (t *StoreTool) Description() string {
	return "Save a memory to the graph. Use after recalling related memories. Supports linking to existing memories via associations, including structured happens_at scheduling for events, deadlines, and plans."
}

func (t *StoreTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"content": map[string]any{
				"type":        "string",
				"description": "The memory content - clear, standalone statement. You can mention timing in prose for readability, but use happens_at for structured event/deadline timing.",
			},
			"memory_type": map[string]any{
				"type":        "string",
				"enum":        []string{"identity", "fact", "preference", "decision", "event", "observation", "goal", "todo", "routine", "feedback", "anomaly", "correlation", "prediction"},
				"description": "Type of memory",
			},
			"importance": map[string]any{
				"type":        "number",
				"description": "0.0-1.0. Usually omit - system assigns sensible defaults based on memory_type. Only set if explicitly very important (0.9+) or trivial (0.2-).",
			},
			"confidence": map[string]any{
				"type":        "number",
				"description": "0.0-1.0. For pattern types only (routine, correlation, prediction). Use 0.7+ for clear patterns, 0.5 for uncertain, lower for speculative.",
			},
			"emotion": map[string]any{
				"type":        "string",
				"description": "Emotional context: frustrated, relieved, stressed, excited, etc.",
			},
			"source": map[string]any{
				"type":        "string",
				"description": "Source: 'user stated', 'inferred', 'observed'",
			},
			"occurred_at": map[string]any{
				"type":        "string",
				"description": "When this memory was observed/recorded, or when a past event happened. Cannot be in the future during normal use. Format: ISO date or RFC3339. Usually omit - defaults to the conversation timestamp.",
			},
			"happens_at": map[string]any{
				"type":        "string",
				"description": "When a scheduled event, plan, appointment, or deadline is set to happen. This may be in the future or the past. Use this for structured timing of todos/events/goals instead of relying only on prose in content. Format: ISO date or RFC3339.",
			},
			"reasoning": map[string]any{
				"type":        "string",
				"description": "Brief explanation of why this memory is worth storing (required for debugging)",
			},
			"associations": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"target_id": map[string]any{
							"type":        "string",
							"description": "UUID of memory from recall",
						},
						"relation_type": map[string]any{
							"type":        "string",
							"enum":        []string{"updates", "contradicts", "related_to", "part_of", "caused_by", "result_of", "triggers", "reinforces", "weakens", "violated", "predicts", "confirmed", "overrides"},
							"description": "How this memory relates to the target",
						},
					},
					"required": []string{"target_id", "relation_type"},
				},
				"description": "Links to existing memories",
			},
			"recurrence": map[string]any{
				"type":        "object",
				"description": "Structured recurrence for routine memories. Only applies when memory_type=\"routine\". All times in server-local tz. When set, trigger_cron is derived automatically from days + time_start; the memory will surface in Today's Schedule and wake the agent at each occurrence.",
				"properties": map[string]any{
					"days": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Lowercase full day names the routine recurs on: e.g. [\"monday\", \"wednesday\", \"friday\"]. Short forms (mon, tue) and ISO numbers (1..7) are also accepted.",
					},
					"time_start": map[string]any{
						"type":        "string",
						"description": "Start time in 24h \"HH:MM\" local. Required alongside days for trigger_cron derivation.",
					},
					"time_end": map[string]any{
						"type":        "string",
						"description": "Optional end time \"HH:MM\" local. If omitted, duration_minutes may be used instead.",
					},
					"duration_minutes": map[string]any{
						"type":        "integer",
						"description": "Optional duration in minutes. Alternative to time_end.",
					},
					"location": map[string]any{
						"type":        "string",
						"description": "Optional location (e.g. \"office\", \"Carrefour\").",
					},
					"person": map[string]any{
						"type":        "string",
						"description": "Optional person the routine involves (e.g. \"Bob\").",
					},
					"starts_on": map[string]any{
						"type":        "string",
						"description": "Optional inclusive start date \"YYYY-MM-DD\". Bounds enforced at fire time, not in cron.",
					},
					"ends_on": map[string]any{
						"type":        "string",
						"description": "Optional inclusive end date \"YYYY-MM-DD\". After this date the routine no longer fires.",
					},
					"skip_dates": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Optional list of \"YYYY-MM-DD\" dates to skip (holidays, travel, etc.).",
					},
					"autonomy": map[string]any{
						"type":        "string",
						"enum":        []string{"observe", "suggest", "confirm", "auto", "silent"},
						"description": "Optional autonomy level for this routine. Defaults to \"observe\".",
					},
				},
			},
		},
		"required": []string{"content", "memory_type", "reasoning"},
	}
}

// StoreParams defines input parameters for the store tool.
type StoreParams struct {
	Content      string             `json:"content"`
	MemoryType   string             `json:"memory_type"`
	Reasoning    string             `json:"reasoning"`
	Importance   *float32           `json:"importance,omitempty"`
	Confidence   *float32           `json:"confidence,omitempty"`
	Emotion      string             `json:"emotion,omitempty"`
	Source       string             `json:"source,omitempty"`
	OccurredAt   string             `json:"occurred_at,omitempty"`
	HappensAt    string             `json:"happens_at,omitempty"`
	Associations []AssociationInput `json:"associations,omitempty"`
	Recurrence   *RecurrenceInput   `json:"recurrence,omitempty"`
}

// AssociationInput represents an association to create.
type AssociationInput struct {
	TargetID     string `json:"target_id"`
	RelationType string `json:"relation_type"`
}

// RecurrenceInput captures the optional structured recurrence sub-object for
// routine memories. Empty fields are treated as "not set". Dates are parsed
// as YYYY-MM-DD in server local tz.
type RecurrenceInput struct {
	Days            []string `json:"days,omitempty"`
	TimeStart       string   `json:"time_start,omitempty"`
	TimeEnd         string   `json:"time_end,omitempty"`
	DurationMinutes *int     `json:"duration_minutes,omitempty"`
	Location        string   `json:"location,omitempty"`
	Person          string   `json:"person,omitempty"`
	StartsOn        string   `json:"starts_on,omitempty"`
	EndsOn          string   `json:"ends_on,omitempty"`
	SkipDates       []string `json:"skip_dates,omitempty"`
	Autonomy        string   `json:"autonomy,omitempty"`
}

func (t *StoreTool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var params StoreParams
	if err := json.Unmarshal(input, &params); err != nil {
		return types.ErrorResult(fmt.Sprintf("invalid input: %v", err)), nil
	}

	// Validate required fields
	if params.Content == "" {
		return types.ErrorResult("content is required"), nil
	}
	if params.MemoryType == "" {
		return types.ErrorResult("memory_type is required"), nil
	}

	// Get username from context - required for privacy isolation
	username, err := getUsernameFromContext(ctx)
	if err != nil {
		return types.ErrorResult(err.Error()), nil
	}

	// Get additional context from extraction loop or gateway session
	var messageIDs []string
	var sessionKey, channel string

	// First try extraction loop context keys
	if ids, ok := ctx.Value(ContextKeyMessageIDs).([]string); ok {
		messageIDs = ids
	}
	if sk, ok := ctx.Value(ContextKeySessionKey).(string); ok {
		sessionKey = sk
	}
	if ch, ok := ctx.Value(ContextKeyChannel).(string); ok {
		channel = ch
	}

	// Fallback to gateway SessionContext for agent-driven extraction
	if sessCtx := types.GetSessionContext(ctx); sessCtx != nil {
		if len(messageIDs) == 0 && len(sessCtx.CurrentMessageIDs) > 0 {
			messageIDs = sessCtx.CurrentMessageIDs
		}
		if channel == "" && sessCtx.Channel != "" {
			channel = sessCtx.Channel
		}
	}

	// Log the store decision with content and reasoning for visibility
	L_info("memory_graph_store: storing",
		"type", params.MemoryType,
		"content", truncateStr(params.Content, 80),
		"reasoning", params.Reasoning,
	)

	L_debug("memory_graph_store: executing",
		"type", params.MemoryType,
		"contentLen", len(params.Content),
		"associations", len(params.Associations),
		"username", username,
	)

	// Build memory with provenance
	mem := &Memory{
		Content:       params.Content,
		Type:          Type(params.MemoryType),
		Username:      username,
		Source:        params.Source,
		Emotion:       params.Emotion,
		SourceSession: sessionKey,
		Channel:       channel,
	}

	// Set source message IDs if available (comma-separated)
	if len(messageIDs) > 0 {
		mem.SourceMessage = strings.Join(messageIDs, ",")
	}

	// Set importance (use default if not provided)
	if params.Importance != nil {
		mem.Importance = *params.Importance
	}

	// Set confidence
	if params.Confidence != nil {
		mem.Confidence = *params.Confidence
	} else {
		mem.Confidence = ConfidenceNotApplicable
	}

	// Set occurred_at - parse from params or use context default.
	if params.OccurredAt != "" {
		if t, err := parseMemoryToolTime(params.OccurredAt); err == nil {
			if t.After(time.Now()) {
				return types.ErrorResult("occurred_at cannot be in the future; use happens_at for scheduled events or deadlines"), nil
			}
			mem.OccurredAt = t
		} else {
			L_warn("memory_graph_store: invalid occurred_at format", "value", params.OccurredAt)
		}
	}
	// If still zero, use context default timestamp
	if mem.OccurredAt.IsZero() {
		if ts, ok := ctx.Value(ContextKeyDefaultTimestamp).(time.Time); ok {
			mem.OccurredAt = ts
		}
		// CreateMemory will default to time.Now() if still zero
	}

	if params.HappensAt != "" {
		if t, err := parseMemoryToolTime(params.HappensAt); err == nil {
			mem.HappensAt = &t
		} else {
			L_warn("memory_graph_store: invalid happens_at format", "value", params.HappensAt)
		}
	}

	// Default source
	if mem.Source == "" {
		mem.Source = "extraction"
	}

	// Pre-validate recurrence before creating the memory so we can bail out
	// cleanly without leaving an orphan memory row if recurrence is malformed.
	if params.Recurrence != nil && params.MemoryType != string(TypeRoutine) {
		return types.ErrorResult("recurrence is only valid for memory_type=\"routine\""), nil
	}
	var routineMeta *RoutineMetadata
	if params.Recurrence != nil {
		meta, errMsg := buildRoutineMetadata(params.Recurrence)
		if errMsg != "" {
			return types.ErrorResult(errMsg), nil
		}
		// Loop-avoidance guard: when this turn originates from a memory trigger,
		// refuse to store a routine whose next occurrence is <5m away, to prevent
		// runaway fire-create-fire loops.
		purpose := ""
		if sc := types.GetSessionContext(ctx); sc != nil {
			purpose = sc.Purpose
		}
		if purpose == "" {
			if p, ok := ctx.Value(ContextKeyPurpose).(string); ok {
				purpose = p
			}
		}
		if purpose == "memtrigger" {
			if next := meta.NextOccurrence(time.Now()); !next.IsZero() && time.Until(next) < 5*time.Minute {
				L_warn("memory_graph_store: loop guard blocked memtrigger-originated routine",
					"next_occurrence", next, "content", truncateStr(params.Content, 80))
				return types.ErrorResult("memtrigger-originated routines cannot schedule within 5 minutes of now (loop guard)"), nil
			}
		}
		routineMeta = meta
	}

	// Create the memory
	if err := t.manager.CreateMemory(ctx, mem); err != nil {
		L_error("memory_graph_store: create failed", "error", err)
		return types.ErrorResult(fmt.Sprintf("failed to save: %v", err)), nil
	}

	// Persist routine metadata if recurrence was supplied.
	if routineMeta != nil {
		routineMeta.MemoryUUID = mem.UUID
		store := t.manager.Store()
		if err := store.SetRoutineMetadata(routineMeta); err != nil {
			L_error("memory_graph_store: SetRoutineMetadata failed", "error", err, "uuid", mem.UUID)
		} else {
			L_info("memory_graph_store: routine metadata saved",
				"uuid", mem.UUID,
				"days", routineMeta.Days,
				"time_start", routineMeta.TimeStart,
				"trigger_cron", routineMeta.TriggerCron,
			)
			// Compute an initial next_trigger_at from the derived cron so the
			// poller picks it up without waiting for the next updateTriggers sweep.
			if next := routineMeta.NextOccurrence(time.Now()); !next.IsZero() {
				if err := store.SetNextTriggerAt(mem.UUID, &next); err != nil {
					L_warn("memory_graph_store: set next_trigger_at failed", "error", err, "uuid", mem.UUID)
				}
			}
		}
	}

	// Create associations
	associationsCreated := 0
	for _, assocInput := range params.Associations {
		if assocInput.TargetID == "" || assocInput.RelationType == "" {
			continue
		}

		assoc := &Association{
			SourceID:     mem.UUID,
			TargetID:     assocInput.TargetID,
			RelationType: RelationType(assocInput.RelationType),
		}

		if err := t.manager.CreateAssociation(assoc); err != nil {
			L_warn("memory_graph_store: association failed",
				"target", assocInput.TargetID,
				"type", assocInput.RelationType,
				"error", err,
			)
		} else {
			associationsCreated++
			L_debug("memory_graph_store: association created",
				"source", mem.UUID,
				"target", assocInput.TargetID,
				"type", assocInput.RelationType,
			)
		}
	}

	L_info("memory_graph_store: created",
		"id", mem.UUID,
		"type", params.MemoryType,
		"associations", associationsCreated,
	)

	// Mark messages as extracted to prevent duplicate background extraction
	if len(messageIDs) > 0 {
		for _, msgID := range messageIDs {
			_ = setIngestionState(t.manager.DB(), &IngestionState{
				SourceType:  "agent",
				SourcePath:  msgID,
				ContentHash: HashContent(params.Content),
				IngestedAt:  time.Now(),
				MemoryCount: 1,
			})
		}
		L_debug("memory_graph_store: marked ingestion state", "messageIDs", messageIDs)
	}

	// Invalidate the appropriate bulletin cache based on memory type
	if username != "" {
		if isContextBulletinType(Type(params.MemoryType)) {
			t.manager.InvalidateContextBulletinCache(username)
		}
		if !isContextBulletinType(Type(params.MemoryType)) || mem.HappensAt != nil {
			t.manager.InvalidateMemoryBulletinCache(username)
		}
	}

	// Return the new memory's UUID
	result := fmt.Sprintf("Saved memory %s [%s] %q", mem.UUID, params.MemoryType, truncateStr(params.Content, 50))
	if associationsCreated > 0 {
		result += fmt.Sprintf(" with %d association(s)", associationsCreated)
	}

	return types.TextResult(result), nil
}

func parseMemoryToolTime(value string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", value)
}

// buildRoutineMetadata validates a RecurrenceInput and produces a RoutineMetadata
// ready for SetRoutineMetadata. Returns ("", nil) on success. On validation
// failure, returns a user-facing error message in the second return and a nil meta.
// trigger_cron is derived from days + time_start when both are present.
// MemoryUUID is left blank for the caller to fill in after CreateMemory.
func buildRoutineMetadata(rec *RecurrenceInput) (*RoutineMetadata, string) {
	if rec == nil {
		return nil, ""
	}
	normalizedDays := normalizeDays(rec.Days)
	if len(rec.Days) > 0 && len(normalizedDays) == 0 {
		return nil, fmt.Sprintf("recurrence.days had no recognisable day names; got %v", rec.Days)
	}
	if rec.TimeStart != "" {
		if _, _, err := parseClockTime(rec.TimeStart); err != nil {
			return nil, fmt.Sprintf("recurrence.time_start %q: %v", rec.TimeStart, err)
		}
	}
	if rec.TimeEnd != "" {
		if _, _, err := parseClockTime(rec.TimeEnd); err != nil {
			return nil, fmt.Sprintf("recurrence.time_end %q: %v", rec.TimeEnd, err)
		}
	}
	var startsOn, endsOn *time.Time
	if rec.StartsOn != "" {
		t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(rec.StartsOn), time.Local)
		if err != nil {
			return nil, fmt.Sprintf("recurrence.starts_on %q: want YYYY-MM-DD", rec.StartsOn)
		}
		startsOn = &t
	}
	if rec.EndsOn != "" {
		t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(rec.EndsOn), time.Local)
		if err != nil {
			return nil, fmt.Sprintf("recurrence.ends_on %q: want YYYY-MM-DD", rec.EndsOn)
		}
		endsOn = &t
	}
	if startsOn != nil && endsOn != nil && endsOn.Before(*startsOn) {
		return nil, "recurrence.ends_on is before starts_on"
	}
	for _, d := range rec.SkipDates {
		if _, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(d), time.Local); err != nil {
			return nil, fmt.Sprintf("recurrence.skip_dates entry %q: want YYYY-MM-DD", d)
		}
	}
	cron, err := deriveTriggerCron(normalizedDays, rec.TimeStart)
	if err != nil {
		return nil, fmt.Sprintf("recurrence: %v", err)
	}

	autonomy := strings.TrimSpace(rec.Autonomy)
	if autonomy == "" {
		autonomy = "observe"
	}

	meta := &RoutineMetadata{
		TriggerType:     "time",
		TriggerCron:     cron,
		Autonomy:        autonomy,
		Days:            normalizedDays,
		TimeStart:       strings.TrimSpace(rec.TimeStart),
		TimeEnd:         strings.TrimSpace(rec.TimeEnd),
		DurationMinutes: rec.DurationMinutes,
		Location:        strings.TrimSpace(rec.Location),
		Person:          strings.TrimSpace(rec.Person),
		StartsOn:        startsOn,
		EndsOn:          endsOn,
		SkipDates:       rec.SkipDates,
	}
	return meta, ""
}
