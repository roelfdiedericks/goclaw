package memorygraph

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// DueRoutine is a routine whose next_trigger_at has elapsed and which is eligible
// to be woken by the trigger poller. It combines the Memory + RoutineMetadata so
// the poller can enforce bounds / skip_dates / re-compute next_trigger_at cheaply.
type DueRoutine struct {
	Memory  *Memory
	Meta    *RoutineMetadata
	DueAt   time.Time
	OwnerID string // username from the memory row
}

// ListDueRoutines returns routines (non-forgotten, trigger_type = "time") whose
// next_trigger_at is <= now. The caller is responsible for bounds/skip_dates
// enforcement and the grace-window check.
func (s *Store) ListDueRoutines(now time.Time) ([]*DueRoutine, error) {
	rows, err := s.db.Query(`
		SELECT m.id, m.uuid, m.content, m.memory_type, m.importance, m.confidence,
			m.created_at, m.updated_at, m.last_accessed_at, m.access_count,
			m.happens_at, m.next_trigger_at, m.source, m.source_session, m.source_message,
			m.username, m.channel, m.chat_id, m.emotion, m.occurred_at, m.forgotten, m.embedding, m.embedding_model,
			rm.memory_uuid, rm.trigger_type, rm.trigger_cron, rm.trigger_event, rm.trigger_condition,
			rm.action, rm.action_entity, rm.action_extra, rm.autonomy,
			rm.observations, rm.suggestions, rm.acceptances, rm.rejections, rm.auto_runs, rm.last_triggered_at,
			rm.days, rm.time_start, rm.time_end, rm.duration_minutes, rm.location, rm.person,
			rm.starts_on, rm.ends_on, rm.skip_dates
		FROM memories m
		JOIN routine_metadata rm ON rm.memory_uuid = m.uuid
		WHERE m.forgotten = 0
		  AND m.memory_type = 'routine'
		  AND rm.trigger_type = 'time'
		  AND m.next_trigger_at IS NOT NULL
		  AND m.next_trigger_at <= ?
	`, now.Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("query due routines: %w", err)
	}
	defer rows.Close()

	var out []*DueRoutine
	for rows.Next() {
		pair, scanErr := scanRoutineJoin(rows)
		if scanErr != nil || pair == nil || pair.Memory == nil || pair.Meta == nil {
			continue
		}
		if pair.Memory.NextTriggerAt == nil {
			continue
		}
		out = append(out, &DueRoutine{
			Memory:  pair.Memory,
			Meta:    pair.Meta,
			DueAt:   *pair.Memory.NextTriggerAt,
			OwnerID: pair.Memory.Username,
		})
	}
	return out, rows.Err()
}

// ClaimTrigger atomically records that a memory trigger will fire for the given
// memory/scheduled_for pair. Returns (true, nil) if the claim was granted (this
// caller is responsible for firing), (false, nil) if the UNIQUE constraint
// rejected the insert (another worker already claimed this instant), or an
// error on unexpected failure.
func (s *Store) ClaimTrigger(memoryUUID string, scheduledFor time.Time, username, sessionKey string) (bool, error) {
	now := time.Now().Format(time.RFC3339)
	res, err := s.db.Exec(`
		INSERT OR IGNORE INTO memory_triggers_fired (
			memory_uuid, scheduled_for, fired_at, username, session_key
		) VALUES (?, ?, ?, ?, ?)
	`, memoryUUID, scheduledFor.UTC().Format(time.RFC3339), now, username, sessionKey)
	if err != nil {
		return false, fmt.Errorf("claim trigger: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim trigger rowcount: %w", err)
	}
	return affected > 0, nil
}

// MarkTriggerOutcome updates the outcome + run_id columns on an existing claim row.
// Safe to call multiple times; uses (memory_uuid, scheduled_for) as the key.
func (s *Store) MarkTriggerOutcome(memoryUUID string, scheduledFor time.Time, outcome, runID string) error {
	_, err := s.db.Exec(`
		UPDATE memory_triggers_fired
		SET outcome = ?, run_id = ?
		WHERE memory_uuid = ? AND scheduled_for = ?
	`, outcome, runID, memoryUUID, scheduledFor.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("mark trigger outcome: %w", err)
	}
	return nil
}

// SetNextTriggerAt updates the next_trigger_at column on a memory (used by the
// poller to recompute after firing or skipping a trigger).
func (s *Store) SetNextTriggerAt(memoryUUID string, next *time.Time) error {
	var nextStr *string
	if next != nil {
		v := next.Format(time.RFC3339)
		nextStr = &v
	}
	_, err := s.db.Exec(`
		UPDATE memories SET next_trigger_at = ?, updated_at = ? WHERE uuid = ?
	`, nextStr, time.Now().Format(time.RFC3339), memoryUUID)
	if err != nil {
		return fmt.Errorf("set next_trigger_at: %w", err)
	}
	return nil
}

// TriggerFired describes one memory-trigger firing (historical audit record).
// Content is populated only when the query joins memories.content (e.g. via
// QueryTriggersFired); callers that don't need the join leave it empty.
type TriggerFired struct {
	ID           int64
	MemoryUUID   string
	ScheduledFor time.Time
	FiredAt      time.Time
	Username     string
	SessionKey   string
	Outcome      string
	RunID        string
	Content      string
}

// TriggerQueryParams filters the trigger audit log. All fields are optional
// except Limit (defaults to 20, capped at 50).
type TriggerQueryParams struct {
	MemoryUUID string
	Username   string
	Since      *time.Time
	Before     *time.Time
	Outcome    string
	Limit      int
}

// QueryTriggersFired returns fire-audit rows joined with memories.content,
// ordered by fired_at DESC. All filter fields are optional.
func (s *Store) QueryTriggersFired(params TriggerQueryParams) ([]*TriggerFired, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}

	var conds []string
	var args []any
	if params.MemoryUUID != "" {
		conds = append(conds, "f.memory_uuid = ?")
		args = append(args, params.MemoryUUID)
	}
	if params.Username != "" {
		conds = append(conds, "f.username = ?")
		args = append(args, params.Username)
	}
	if params.Since != nil {
		conds = append(conds, "f.fired_at >= ?")
		args = append(args, params.Since.UTC().Format(time.RFC3339))
	}
	if params.Before != nil {
		conds = append(conds, "f.fired_at <= ?")
		args = append(args, params.Before.UTC().Format(time.RFC3339))
	}
	if params.Outcome != "" {
		conds = append(conds, "f.outcome = ?")
		args = append(args, params.Outcome)
	}

	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}

	// #nosec G201 -- where clause is built from fixed internal condition strings; user values parameterized via args.
	query := fmt.Sprintf(`
		SELECT f.id, f.memory_uuid, f.scheduled_for, f.fired_at, f.username, f.session_key,
			COALESCE(f.outcome, ''), COALESCE(f.run_id, ''), COALESCE(m.content, '')
		FROM memory_triggers_fired f
		LEFT JOIN memories m ON m.uuid = f.memory_uuid
		%s
		ORDER BY f.fired_at DESC
		LIMIT ?
	`, where)
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query trigger fired: %w", err)
	}
	defer rows.Close()

	var out []*TriggerFired
	for rows.Next() {
		t := &TriggerFired{}
		var scheduledFor, firedAt string
		var outcome, runID, content sql.NullString
		if err := rows.Scan(&t.ID, &t.MemoryUUID, &scheduledFor, &firedAt, &t.Username, &t.SessionKey, &outcome, &runID, &content); err != nil {
			continue
		}
		t.ScheduledFor, _ = time.Parse(time.RFC3339, scheduledFor)
		t.FiredAt, _ = time.Parse(time.RFC3339, firedAt)
		t.Outcome = outcome.String
		t.RunID = runID.String
		t.Content = content.String
		out = append(out, t)
	}
	return out, rows.Err()
}

// RecentTriggerFired returns up to `limit` recent trigger-fire rows for audit /
// debugging. Ordered by fired_at DESC. Thin wrapper around QueryTriggersFired
// retained for existing callers.
func (s *Store) RecentTriggerFired(limit int) ([]*TriggerFired, error) {
	return s.QueryTriggersFired(TriggerQueryParams{Limit: limit})
}

// TriggersFiredForUserOnDate returns all fire rows for the given user whose
// fired_at falls within the local-day window for the given date. Grouped by
// memory_uuid; each slice is sorted ascending by scheduled_for so callers can
// pick the entry that matches the occurrence they are rendering.
//
// The slice-per-UUID shape future-proofs the helper for V2 multi-daily
// routines; today's single-time_start model emits at most one entry per UUID.
func (s *Store) TriggersFiredForUserOnDate(username string, date time.Time) (map[string][]*TriggerFired, error) {
	dayStart := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
	dayEnd := dayStart.Add(24 * time.Hour)

	rows, err := s.db.Query(`
		SELECT id, memory_uuid, scheduled_for, fired_at, username, session_key,
			COALESCE(outcome, ''), COALESCE(run_id, '')
		FROM memory_triggers_fired
		WHERE username = ?
		  AND fired_at >= ?
		  AND fired_at < ?
	`, username, dayStart.UTC().Format(time.RFC3339), dayEnd.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("query trigger fired for date: %w", err)
	}
	defer rows.Close()

	out := make(map[string][]*TriggerFired)
	for rows.Next() {
		t := &TriggerFired{}
		var scheduledFor, firedAt string
		var outcome, runID sql.NullString
		if err := rows.Scan(&t.ID, &t.MemoryUUID, &scheduledFor, &firedAt, &t.Username, &t.SessionKey, &outcome, &runID); err != nil {
			continue
		}
		t.ScheduledFor, _ = time.Parse(time.RFC3339, scheduledFor)
		t.FiredAt, _ = time.Parse(time.RFC3339, firedAt)
		t.Outcome = outcome.String
		t.RunID = runID.String
		out[t.MemoryUUID] = append(out[t.MemoryUUID], t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for uuid := range out {
		sort.Slice(out[uuid], func(i, j int) bool {
			return out[uuid][i].ScheduledFor.Before(out[uuid][j].ScheduledFor)
		})
	}
	return out, nil
}
