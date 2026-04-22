package memorygraph

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// RoutineWithMetadata bundles a routine Memory with its structured metadata.
type RoutineWithMetadata struct {
	Memory *Memory
	Meta   *RoutineMetadata
}

// OccurrenceInstance is a single resolved occurrence of a routine on a specific date.
type OccurrenceInstance struct {
	Routine *RoutineWithMetadata
	Start   time.Time
}

// listStructuredRoutines returns every non-forgotten routine for the user that has a
// structured recurrence (Days + TimeStart populated). Used by day / range queries
// and the Today's Schedule bulletin section.
func (s *Store) listStructuredRoutines(username string) ([]*RoutineWithMetadata, error) {
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
		  AND m.username = ?
		  AND rm.days IS NOT NULL
		  AND rm.time_start IS NOT NULL
	`, username)
	if err != nil {
		return nil, fmt.Errorf("query structured routines: %w", err)
	}
	defer rows.Close()

	var out []*RoutineWithMetadata
	for rows.Next() {
		pair, scanErr := scanRoutineJoin(rows)
		if scanErr != nil {
			L_warn("memorygraph: scan structured routine failed", "error", scanErr)
			continue
		}
		out = append(out, pair)
	}
	return out, rows.Err()
}

// GetRoutinesForDay returns every structured routine that recurs on the given day name
// (canonical lowercase: "monday"..."sunday") for the user, ordered by TimeStart.
// Honours StartsOn / EndsOn / SkipDates against today's date in time.Local.
func (s *Store) GetRoutinesForDay(username, dayName string) ([]*RoutineWithMetadata, error) {
	normalized := dayNameNormalize(dayName)
	if normalized == "" {
		return nil, fmt.Errorf("invalid day name: %q", dayName)
	}

	all, err := s.listStructuredRoutines(username)
	if err != nil {
		return nil, err
	}

	today := time.Now().In(time.Local)
	var filtered []*RoutineWithMetadata
	for _, r := range all {
		if r.Meta == nil {
			continue
		}
		// Matches the requested day?
		match := false
		for _, d := range r.Meta.Days {
			if dayNameNormalize(d) == normalized {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		if !r.Meta.inBounds(today) {
			continue
		}
		filtered = append(filtered, r)
	}

	// Sort by time_start ascending (stable via simple selection sort; N small).
	for i := 0; i < len(filtered)-1; i++ {
		for j := i + 1; j < len(filtered); j++ {
			if filtered[j].Meta.TimeStart < filtered[i].Meta.TimeStart {
				filtered[i], filtered[j] = filtered[j], filtered[i]
			}
		}
	}
	return filtered, nil
}

// GetRoutinesInRange returns every structured routine for the user with at least one
// occurrence in [from, until), along with the resolved occurrence instants.
// Honours StartsOn / EndsOn / SkipDates.
func (s *Store) GetRoutinesInRange(username string, from, until time.Time) ([]OccurrenceInstance, error) {
	if !until.After(from) {
		return nil, nil
	}
	all, err := s.listStructuredRoutines(username)
	if err != nil {
		return nil, err
	}

	var out []OccurrenceInstance
	for _, r := range all {
		if r.Meta == nil {
			continue
		}
		for _, t := range r.Meta.OccurrencesIn(from, until) {
			out = append(out, OccurrenceInstance{Routine: r, Start: t})
		}
	}
	// Sort ascending by start time.
	for i := 0; i < len(out)-1; i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Start.Before(out[i].Start) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

// scanRoutineJoin scans a joined row (memories + routine_metadata) into a RoutineWithMetadata.
func scanRoutineJoin(rows *sql.Rows) (*RoutineWithMetadata, error) {
	m := &Memory{}
	var createdAt, updatedAt, lastAccessedAt string
	var happensAt, nextTriggerAt, source, sourceSession, sourceMessage, username, channel, chatID, emotion sql.NullString
	var occurredAt int64
	var embeddingBlob []byte
	var embeddingModel sql.NullString
	var forgotten int

	rm := &RoutineMetadata{}
	var triggerCron, triggerEvent, triggerCondition, actionEntity, actionExtra, lastTriggered sql.NullString
	var daysJSON, timeStart, timeEnd, location, person, startsOn, endsOn, skipDatesJSON sql.NullString
	var durationMinutes sql.NullInt64

	if err := rows.Scan(
		&m.ID, &m.UUID, &m.Content, &m.Type, &m.Importance, &m.Confidence,
		&createdAt, &updatedAt, &lastAccessedAt, &m.AccessCount,
		&happensAt, &nextTriggerAt, &source, &sourceSession, &sourceMessage,
		&username, &channel, &chatID, &emotion, &occurredAt, &forgotten, &embeddingBlob, &embeddingModel,
		&rm.MemoryUUID, &rm.TriggerType, &triggerCron, &triggerEvent, &triggerCondition,
		&rm.Action, &actionEntity, &actionExtra, &rm.Autonomy,
		&rm.Observations, &rm.Suggestions, &rm.Acceptances, &rm.Rejections, &rm.AutoRuns, &lastTriggered,
		&daysJSON, &timeStart, &timeEnd, &durationMinutes, &location, &person,
		&startsOn, &endsOn, &skipDatesJSON,
	); err != nil {
		return nil, err
	}

	m.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	m.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	m.LastAccessedAt, _ = time.Parse(time.RFC3339, lastAccessedAt)
	m.OccurredAt = time.Unix(occurredAt, 0)
	if happensAt.Valid {
		t, _ := time.Parse(time.RFC3339, happensAt.String)
		m.HappensAt = &t
	}
	if nextTriggerAt.Valid {
		t, _ := time.Parse(time.RFC3339, nextTriggerAt.String)
		m.NextTriggerAt = &t
	}
	m.Source = source.String
	m.SourceSession = sourceSession.String
	m.SourceMessage = sourceMessage.String
	m.Username = username.String
	m.Channel = channel.String
	m.ChatID = chatID.String
	m.Emotion = emotion.String
	m.Forgotten = intToBool(forgotten)
	m.EmbeddingModel = embeddingModel.String

	rm.TriggerCron = triggerCron.String
	rm.TriggerEvent = triggerEvent.String
	rm.TriggerCondition = triggerCondition.String
	rm.ActionEntity = actionEntity.String
	rm.ActionExtra = actionExtra.String
	if lastTriggered.Valid {
		t, _ := time.Parse(time.RFC3339, lastTriggered.String)
		rm.LastTriggeredAt = &t
	}
	if daysJSON.Valid && daysJSON.String != "" {
		var days []string
		_ = json.Unmarshal([]byte(daysJSON.String), &days)
		rm.Days = days
	}
	rm.TimeStart = timeStart.String
	rm.TimeEnd = timeEnd.String
	if durationMinutes.Valid {
		d := int(durationMinutes.Int64)
		rm.DurationMinutes = &d
	}
	rm.Location = location.String
	rm.Person = person.String
	if startsOn.Valid && startsOn.String != "" {
		if t, err := time.ParseInLocation("2006-01-02", startsOn.String, time.Local); err == nil {
			rm.StartsOn = &t
		}
	}
	if endsOn.Valid && endsOn.String != "" {
		if t, err := time.ParseInLocation("2006-01-02", endsOn.String, time.Local); err == nil {
			rm.EndsOn = &t
		}
	}
	if skipDatesJSON.Valid && skipDatesJSON.String != "" {
		var skip []string
		_ = json.Unmarshal([]byte(skipDatesJSON.String), &skip)
		rm.SkipDates = skip
	}

	return &RoutineWithMetadata{Memory: m, Meta: rm}, nil
}
