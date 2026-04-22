package memorygraph

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Store handles CRUD operations for memories and associations
type Store struct {
	db *sql.DB
}

// NewStore creates a new memory store
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// generateULID creates a new ULID
func generateULID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

// CreateMemory inserts a new memory and returns the created memory with ID/UUID
func (s *Store) CreateMemory(m *Memory) error {
	if m.UUID == "" {
		m.UUID = generateULID()
	}

	now := time.Now()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = now
	}
	if m.LastAccessedAt.IsZero() {
		m.LastAccessedAt = now
	}
	if m.OccurredAt.IsZero() {
		m.OccurredAt = now
	}
	if m.Importance == 0 {
		if defaultImp, ok := DefaultImportance[m.Type]; ok {
			m.Importance = defaultImp
		} else {
			m.Importance = 0.5
		}
	}
	if m.Confidence == 0 {
		m.Confidence = ConfidenceNotApplicable
	}

	var embeddingBlob []byte
	if len(m.Embedding) > 0 {
		embeddingBlob, _ = json.Marshal(m.Embedding)
	}

	var nextTrigger *string
	var happensAt *string
	if m.NextTriggerAt != nil {
		t := m.NextTriggerAt.Format(time.RFC3339)
		nextTrigger = &t
	}
	if m.HappensAt != nil {
		t := m.HappensAt.Format(time.RFC3339)
		happensAt = &t
	}

	result, err := s.db.Exec(`
		INSERT INTO memories (
			uuid, content, memory_type, importance, confidence,
			created_at, updated_at, last_accessed_at, access_count,
			happens_at, next_trigger_at, source, source_session, source_message,
			username, channel, chat_id, emotion, occurred_at, forgotten, embedding, embedding_model
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		m.UUID, m.Content, m.Type, m.Importance, m.Confidence,
		m.CreatedAt.Format(time.RFC3339), m.UpdatedAt.Format(time.RFC3339),
		m.LastAccessedAt.Format(time.RFC3339), m.AccessCount,
		happensAt, nextTrigger, m.Source, m.SourceSession, m.SourceMessage,
		m.Username, m.Channel, m.ChatID, m.Emotion, m.OccurredAt.Unix(),
		boolToInt(m.Forgotten), embeddingBlob, m.EmbeddingModel,
	)
	if err != nil {
		return fmt.Errorf("insert memory: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get last insert id: %w", err)
	}
	m.ID = id

	L_debug("memorygraph: created memory", "uuid", m.UUID, "type", m.Type)
	return nil
}

// GetMemory retrieves a memory by UUID
func (s *Store) GetMemory(uuid string) (*Memory, error) {
	row := s.db.QueryRow(`
		SELECT id, uuid, content, memory_type, importance, confidence,
			created_at, updated_at, last_accessed_at, access_count,
			happens_at, next_trigger_at, source, source_session, source_message,
			username, channel, chat_id, emotion, occurred_at, forgotten, embedding, embedding_model
		FROM memories WHERE uuid = ?
	`, uuid)

	return scanMemory(row)
}

// GetMemoryByID retrieves a memory by internal ID
func (s *Store) GetMemoryByID(id int64) (*Memory, error) {
	row := s.db.QueryRow(`
		SELECT id, uuid, content, memory_type, importance, confidence,
			created_at, updated_at, last_accessed_at, access_count,
			happens_at, next_trigger_at, source, source_session, source_message,
			username, channel, chat_id, emotion, occurred_at, forgotten, embedding, embedding_model
		FROM memories WHERE id = ?
	`, id)

	return scanMemory(row)
}

// UpdateMemory updates an existing memory
func (s *Store) UpdateMemory(m *Memory) error {
	m.UpdatedAt = time.Now()

	var embeddingBlob []byte
	if len(m.Embedding) > 0 {
		embeddingBlob, _ = json.Marshal(m.Embedding)
	}

	var nextTrigger *string
	var happensAt *string
	if m.NextTriggerAt != nil {
		t := m.NextTriggerAt.Format(time.RFC3339)
		nextTrigger = &t
	}
	if m.HappensAt != nil {
		t := m.HappensAt.Format(time.RFC3339)
		happensAt = &t
	}

	_, err := s.db.Exec(`
		UPDATE memories SET
			content = ?, memory_type = ?, importance = ?, confidence = ?,
			updated_at = ?, last_accessed_at = ?, access_count = ?,
			happens_at = ?, next_trigger_at = ?, source = ?, source_session = ?, source_message = ?,
			username = ?, channel = ?, chat_id = ?, emotion = ?, occurred_at = ?, forgotten = ?,
			embedding = ?, embedding_model = ?
		WHERE uuid = ?
	`,
		m.Content, m.Type, m.Importance, m.Confidence,
		m.UpdatedAt.Format(time.RFC3339), m.LastAccessedAt.Format(time.RFC3339),
		m.AccessCount, happensAt, nextTrigger, m.Source, m.SourceSession, m.SourceMessage,
		m.Username, m.Channel, m.ChatID, m.Emotion, m.OccurredAt.Unix(), boolToInt(m.Forgotten),
		embeddingBlob, m.EmbeddingModel,
		m.UUID,
	)
	if err != nil {
		return fmt.Errorf("update memory: %w", err)
	}

	L_debug("memorygraph: updated memory", "uuid", m.UUID)
	return nil
}

// DeleteMemory permanently removes a memory by UUID
func (s *Store) DeleteMemory(uuid string) error {
	_, err := s.db.Exec("DELETE FROM memories WHERE uuid = ?", uuid)
	if err != nil {
		return fmt.Errorf("delete memory: %w", err)
	}
	L_debug("memorygraph: deleted memory", "uuid", uuid)
	return nil
}

// ForgetMemory marks a memory as forgotten (soft delete)
func (s *Store) ForgetMemory(uuid string) error {
	_, err := s.db.Exec(`
		UPDATE memories SET forgotten = 1, updated_at = ? WHERE uuid = ?
	`, time.Now().Format(time.RFC3339), uuid)
	if err != nil {
		return fmt.Errorf("forget memory: %w", err)
	}
	L_debug("memorygraph: forgot memory", "uuid", uuid)
	return nil
}

// TouchMemory updates last_accessed_at and increments access_count
func (s *Store) TouchMemory(uuid string) error {
	_, err := s.db.Exec(`
		UPDATE memories SET
			last_accessed_at = ?,
			access_count = access_count + 1
		WHERE uuid = ?
	`, time.Now().Format(time.RFC3339), uuid)
	return err
}

// CreateAssociation creates a new association between two memories
func (s *Store) CreateAssociation(a *Association) error {
	if a.ID == "" {
		a.ID = generateULID()
	}
	if a.Weight == 0 {
		a.Weight = 1.0
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}

	// Use default directedness if not explicitly set
	if !a.Directed {
		if defaultDir, ok := DefaultDirected[a.RelationType]; ok {
			a.Directed = defaultDir
		} else {
			a.Directed = true
		}
	}

	_, err := s.db.Exec(`
		INSERT INTO associations (id, source_uuid, target_uuid, relation_type, weight, directed, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, a.ID, a.SourceID, a.TargetID, a.RelationType, a.Weight, boolToInt(a.Directed), a.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("insert association: %w", err)
	}

	L_debug("memorygraph: created association", "id", a.ID, "type", a.RelationType)
	return nil
}

// GetAssociation retrieves an association by ID
func (s *Store) GetAssociation(id string) (*Association, error) {
	row := s.db.QueryRow(`
		SELECT id, source_uuid, target_uuid, relation_type, weight, directed, created_at
		FROM associations WHERE id = ?
	`, id)

	return scanAssociation(row)
}

// GetAssociationsFrom returns all associations from a memory
func (s *Store) GetAssociationsFrom(memoryUUID string) ([]*Association, error) {
	rows, err := s.db.Query(`
		SELECT id, source_uuid, target_uuid, relation_type, weight, directed, created_at
		FROM associations WHERE source_uuid = ?
	`, memoryUUID)
	if err != nil {
		return nil, fmt.Errorf("query associations: %w", err)
	}
	defer rows.Close()

	return scanAssociations(rows)
}

// GetAssociationsTo returns all associations to a memory
func (s *Store) GetAssociationsTo(memoryUUID string) ([]*Association, error) {
	rows, err := s.db.Query(`
		SELECT id, source_uuid, target_uuid, relation_type, weight, directed, created_at
		FROM associations WHERE target_uuid = ?
	`, memoryUUID)
	if err != nil {
		return nil, fmt.Errorf("query associations: %w", err)
	}
	defer rows.Close()

	return scanAssociations(rows)
}

// GetAssociationsWithMemory returns all associations involving a memory (both directions for undirected)
func (s *Store) GetAssociationsWithMemory(memoryUUID string) ([]*Association, error) {
	rows, err := s.db.Query(`
		SELECT id, source_uuid, target_uuid, relation_type, weight, directed, created_at
		FROM associations 
		WHERE source_uuid = ? OR (target_uuid = ? AND directed = 0)
	`, memoryUUID, memoryUUID)
	if err != nil {
		return nil, fmt.Errorf("query associations: %w", err)
	}
	defer rows.Close()

	return scanAssociations(rows)
}

// DeleteAssociation removes an association by ID
func (s *Store) DeleteAssociation(id string) error {
	_, err := s.db.Exec("DELETE FROM associations WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete association: %w", err)
	}
	return nil
}

// SetRoutineMetadata creates or updates routine metadata
func (s *Store) SetRoutineMetadata(m *RoutineMetadata) error {
	var lastTriggered *string
	if m.LastTriggeredAt != nil {
		t := m.LastTriggeredAt.Format(time.RFC3339)
		lastTriggered = &t
	}

	var daysJSON, skipDatesJSON *string
	if len(m.Days) > 0 {
		if b, err := json.Marshal(m.Days); err == nil {
			s := string(b)
			daysJSON = &s
		}
	}
	if len(m.SkipDates) > 0 {
		if b, err := json.Marshal(m.SkipDates); err == nil {
			s := string(b)
			skipDatesJSON = &s
		}
	}

	var timeStart, timeEnd, location, person *string
	if m.TimeStart != "" {
		timeStart = &m.TimeStart
	}
	if m.TimeEnd != "" {
		timeEnd = &m.TimeEnd
	}
	if m.Location != "" {
		location = &m.Location
	}
	if m.Person != "" {
		person = &m.Person
	}

	var durationMinutes *int
	if m.DurationMinutes != nil {
		durationMinutes = m.DurationMinutes
	}

	var startsOn, endsOn *string
	if m.StartsOn != nil {
		s := m.StartsOn.Format("2006-01-02")
		startsOn = &s
	}
	if m.EndsOn != nil {
		s := m.EndsOn.Format("2006-01-02")
		endsOn = &s
	}

	_, err := s.db.Exec(`
		INSERT INTO routine_metadata (
			memory_uuid, trigger_type, trigger_cron, trigger_event, trigger_condition,
			action, action_entity, action_extra, autonomy,
			observations, suggestions, acceptances, rejections, auto_runs, last_triggered_at,
			days, time_start, time_end, duration_minutes, location, person,
			starts_on, ends_on, skip_dates
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(memory_uuid) DO UPDATE SET
			trigger_type = excluded.trigger_type,
			trigger_cron = excluded.trigger_cron,
			trigger_event = excluded.trigger_event,
			trigger_condition = excluded.trigger_condition,
			action = excluded.action,
			action_entity = excluded.action_entity,
			action_extra = excluded.action_extra,
			autonomy = excluded.autonomy,
			observations = excluded.observations,
			suggestions = excluded.suggestions,
			acceptances = excluded.acceptances,
			rejections = excluded.rejections,
			auto_runs = excluded.auto_runs,
			last_triggered_at = excluded.last_triggered_at,
			days = excluded.days,
			time_start = excluded.time_start,
			time_end = excluded.time_end,
			duration_minutes = excluded.duration_minutes,
			location = excluded.location,
			person = excluded.person,
			starts_on = excluded.starts_on,
			ends_on = excluded.ends_on,
			skip_dates = excluded.skip_dates
	`, m.MemoryUUID, m.TriggerType, m.TriggerCron, m.TriggerEvent, m.TriggerCondition,
		m.Action, m.ActionEntity, m.ActionExtra, m.Autonomy,
		m.Observations, m.Suggestions, m.Acceptances, m.Rejections, m.AutoRuns, lastTriggered,
		daysJSON, timeStart, timeEnd, durationMinutes, location, person,
		startsOn, endsOn, skipDatesJSON)
	return err
}

// GetRoutineMetadata retrieves routine metadata for a memory
func (s *Store) GetRoutineMetadata(memoryUUID string) (*RoutineMetadata, error) {
	row := s.db.QueryRow(`
		SELECT memory_uuid, trigger_type, trigger_cron, trigger_event, trigger_condition,
			action, action_entity, action_extra, autonomy,
			observations, suggestions, acceptances, rejections, auto_runs, last_triggered_at,
			days, time_start, time_end, duration_minutes, location, person,
			starts_on, ends_on, skip_dates
		FROM routine_metadata WHERE memory_uuid = ?
	`, memoryUUID)

	return scanRoutineMetadata(row)
}

// GetRoutineMetadataMulti batch-fetches routine metadata for a set of memory UUIDs.
// Returns a map keyed by memory_uuid; UUIDs with no routine_metadata row are absent
// from the map. Used by query/recall output formatters so they issue one SQL call
// per result set instead of N per-result fetches.
func (s *Store) GetRoutineMetadataMulti(uuids []string) (map[string]*RoutineMetadata, error) {
	out := make(map[string]*RoutineMetadata)
	if len(uuids) == 0 {
		return out, nil
	}

	placeholders := make([]string, len(uuids))
	args := make([]any, len(uuids))
	for i, u := range uuids {
		placeholders[i] = "?"
		args[i] = u
	}

	// #nosec G201 -- placeholders are literal "?" chars; all values go through args.
	query := fmt.Sprintf(`
		SELECT memory_uuid, trigger_type, trigger_cron, trigger_event, trigger_condition,
			action, action_entity, action_extra, autonomy,
			observations, suggestions, acceptances, rejections, auto_runs, last_triggered_at,
			days, time_start, time_end, duration_minutes, location, person,
			starts_on, ends_on, skip_dates
		FROM routine_metadata
		WHERE memory_uuid IN (%s)
	`, strings.Join(placeholders, ","))

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query routine metadata multi: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		m, scanErr := scanRoutineMetadata(rows)
		if scanErr != nil || m == nil {
			continue
		}
		out[m.MemoryUUID] = m
	}
	return out, rows.Err()
}

func scanRoutineMetadata(row scannable) (*RoutineMetadata, error) {
	m := &RoutineMetadata{}
	var triggerCron, triggerEvent, triggerCondition, actionEntity, actionExtra, lastTriggered sql.NullString
	var daysJSON, timeStart, timeEnd, location, person, startsOn, endsOn, skipDatesJSON sql.NullString
	var durationMinutes sql.NullInt64

	err := row.Scan(
		&m.MemoryUUID, &m.TriggerType, &triggerCron, &triggerEvent, &triggerCondition,
		&m.Action, &actionEntity, &actionExtra, &m.Autonomy,
		&m.Observations, &m.Suggestions, &m.Acceptances, &m.Rejections, &m.AutoRuns, &lastTriggered,
		&daysJSON, &timeStart, &timeEnd, &durationMinutes, &location, &person,
		&startsOn, &endsOn, &skipDatesJSON,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan routine metadata: %w", err)
	}

	m.TriggerCron = triggerCron.String
	m.TriggerEvent = triggerEvent.String
	m.TriggerCondition = triggerCondition.String
	m.ActionEntity = actionEntity.String
	m.ActionExtra = actionExtra.String
	if lastTriggered.Valid {
		t, _ := time.Parse(time.RFC3339, lastTriggered.String)
		m.LastTriggeredAt = &t
	}

	if daysJSON.Valid && daysJSON.String != "" {
		var days []string
		if err := json.Unmarshal([]byte(daysJSON.String), &days); err == nil {
			m.Days = days
		}
	}
	m.TimeStart = timeStart.String
	m.TimeEnd = timeEnd.String
	if durationMinutes.Valid {
		d := int(durationMinutes.Int64)
		m.DurationMinutes = &d
	}
	m.Location = location.String
	m.Person = person.String
	if startsOn.Valid && startsOn.String != "" {
		if t, err := time.ParseInLocation("2006-01-02", startsOn.String, time.Local); err == nil {
			m.StartsOn = &t
		}
	}
	if endsOn.Valid && endsOn.String != "" {
		if t, err := time.ParseInLocation("2006-01-02", endsOn.String, time.Local); err == nil {
			m.EndsOn = &t
		}
	}
	if skipDatesJSON.Valid && skipDatesJSON.String != "" {
		var skip []string
		if err := json.Unmarshal([]byte(skipDatesJSON.String), &skip); err == nil {
			m.SkipDates = skip
		}
	}

	return m, nil
}

// SetFeedbackMetadata creates or updates feedback metadata
func (s *Store) SetFeedbackMetadata(m *FeedbackMetadata) error {
	_, err := s.db.Exec(`
		INSERT INTO feedback_metadata (memory_uuid, routine_uuid, feedback_type, context_day, context_time, user_note)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(memory_uuid) DO UPDATE SET
			routine_uuid = excluded.routine_uuid,
			feedback_type = excluded.feedback_type,
			context_day = excluded.context_day,
			context_time = excluded.context_time,
			user_note = excluded.user_note
	`, m.MemoryUUID, m.RoutineUUID, m.FeedbackType, m.ContextDay, m.ContextTime, m.UserNote)
	return err
}

// GetFeedbackMetadata retrieves feedback metadata for a memory
func (s *Store) GetFeedbackMetadata(memoryUUID string) (*FeedbackMetadata, error) {
	row := s.db.QueryRow(`
		SELECT memory_uuid, routine_uuid, feedback_type, context_day, context_time, user_note
		FROM feedback_metadata WHERE memory_uuid = ?
	`, memoryUUID)

	m := &FeedbackMetadata{}
	var routineUUID, contextDay, contextTime, userNote sql.NullString

	err := row.Scan(&m.MemoryUUID, &routineUUID, &m.FeedbackType, &contextDay, &contextTime, &userNote)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan feedback metadata: %w", err)
	}

	m.RoutineUUID = routineUUID.String
	m.ContextDay = contextDay.String
	m.ContextTime = contextTime.String
	m.UserNote = userNote.String

	return m, nil
}

// SetAnomalyMetadata creates or updates anomaly metadata
func (s *Store) SetAnomalyMetadata(m *AnomalyMetadata) error {
	_, err := s.db.Exec(`
		INSERT INTO anomaly_metadata (memory_uuid, routine_uuid, expected, actual, severity)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(memory_uuid) DO UPDATE SET
			routine_uuid = excluded.routine_uuid,
			expected = excluded.expected,
			actual = excluded.actual,
			severity = excluded.severity
	`, m.MemoryUUID, m.RoutineUUID, m.Expected, m.Actual, m.Severity)
	return err
}

// GetAnomalyMetadata retrieves anomaly metadata for a memory
func (s *Store) GetAnomalyMetadata(memoryUUID string) (*AnomalyMetadata, error) {
	row := s.db.QueryRow(`
		SELECT memory_uuid, routine_uuid, expected, actual, severity
		FROM anomaly_metadata WHERE memory_uuid = ?
	`, memoryUUID)

	m := &AnomalyMetadata{}
	var routineUUID sql.NullString

	err := row.Scan(&m.MemoryUUID, &routineUUID, &m.Expected, &m.Actual, &m.Severity)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan anomaly metadata: %w", err)
	}

	m.RoutineUUID = routineUUID.String
	return m, nil
}

// SetCorrelationMetadata creates or updates correlation metadata
func (s *Store) SetCorrelationMetadata(m *CorrelationMetadata) error {
	var lastObserved *string
	if m.LastObservedAt != nil {
		t := m.LastObservedAt.Format(time.RFC3339)
		lastObserved = &t
	}

	_, err := s.db.Exec(`
		INSERT INTO correlation_metadata (memory_uuid, condition, outcome, strength, observations, last_observed_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(memory_uuid) DO UPDATE SET
			condition = excluded.condition,
			outcome = excluded.outcome,
			strength = excluded.strength,
			observations = excluded.observations,
			last_observed_at = excluded.last_observed_at
	`, m.MemoryUUID, m.Condition, m.Outcome, m.Strength, m.Observations, lastObserved)
	return err
}

// GetCorrelationMetadata retrieves correlation metadata for a memory
func (s *Store) GetCorrelationMetadata(memoryUUID string) (*CorrelationMetadata, error) {
	row := s.db.QueryRow(`
		SELECT memory_uuid, condition, outcome, strength, observations, last_observed_at
		FROM correlation_metadata WHERE memory_uuid = ?
	`, memoryUUID)

	m := &CorrelationMetadata{}
	var lastObserved sql.NullString

	err := row.Scan(&m.MemoryUUID, &m.Condition, &m.Outcome, &m.Strength, &m.Observations, &lastObserved)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan correlation metadata: %w", err)
	}

	if lastObserved.Valid {
		t, _ := time.Parse(time.RFC3339, lastObserved.String)
		m.LastObservedAt = &t
	}
	return m, nil
}

// SetPredictionMetadata creates or updates prediction metadata
func (s *Store) SetPredictionMetadata(m *PredictionMetadata) error {
	_, err := s.db.Exec(`
		INSERT INTO prediction_metadata (memory_uuid, routine_uuid, predicted_time, action, outcome, confidence_at_prediction)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(memory_uuid) DO UPDATE SET
			routine_uuid = excluded.routine_uuid,
			predicted_time = excluded.predicted_time,
			action = excluded.action,
			outcome = excluded.outcome,
			confidence_at_prediction = excluded.confidence_at_prediction
	`, m.MemoryUUID, m.RoutineUUID, m.PredictedTime.Format(time.RFC3339), m.Action, m.Outcome, m.ConfidenceAtPrediction)
	return err
}

// GetPredictionMetadata retrieves prediction metadata for a memory
func (s *Store) GetPredictionMetadata(memoryUUID string) (*PredictionMetadata, error) {
	row := s.db.QueryRow(`
		SELECT memory_uuid, routine_uuid, predicted_time, action, outcome, confidence_at_prediction
		FROM prediction_metadata WHERE memory_uuid = ?
	`, memoryUUID)

	m := &PredictionMetadata{}
	var routineUUID sql.NullString
	var predictedTime string

	err := row.Scan(&m.MemoryUUID, &routineUUID, &predictedTime, &m.Action, &m.Outcome, &m.ConfidenceAtPrediction)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan prediction metadata: %w", err)
	}

	m.RoutineUUID = routineUUID.String
	m.PredictedTime, _ = time.Parse(time.RFC3339, predictedTime)
	return m, nil
}

// UpdateEmbedding updates only the embedding for a memory
func (s *Store) UpdateEmbedding(uuid string, embedding []float32, model string) error {
	var blob []byte
	if len(embedding) > 0 {
		blob, _ = json.Marshal(embedding)
	}

	_, err := s.db.Exec(`
		UPDATE memories SET embedding = ?, embedding_model = ? WHERE uuid = ?
	`, blob, model, uuid)
	return err
}

// Helper functions

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func intToBool(i int) bool {
	return i != 0
}

type scannable interface {
	Scan(dest ...interface{}) error
}

func scanMemory(row scannable) (*Memory, error) {
	m := &Memory{}
	var createdAt, updatedAt, lastAccessedAt string
	var happensAt, nextTriggerAt, source, sourceSession, sourceMessage, username, channel, chatID, emotion sql.NullString
	var occurredAt int64
	var embeddingBlob []byte
	var embeddingModel sql.NullString
	var forgotten int

	err := row.Scan(
		&m.ID, &m.UUID, &m.Content, &m.Type, &m.Importance, &m.Confidence,
		&createdAt, &updatedAt, &lastAccessedAt, &m.AccessCount,
		&happensAt, &nextTriggerAt, &source, &sourceSession, &sourceMessage,
		&username, &channel, &chatID, &emotion, &occurredAt, &forgotten, &embeddingBlob, &embeddingModel,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan memory: %w", err)
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

	if len(embeddingBlob) > 0 {
		_ = json.Unmarshal(embeddingBlob, &m.Embedding)
	}

	return m, nil
}

func scanAssociation(row scannable) (*Association, error) {
	a := &Association{}
	var createdAt string
	var directed int

	err := row.Scan(&a.ID, &a.SourceID, &a.TargetID, &a.RelationType, &a.Weight, &directed, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan association: %w", err)
	}

	a.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	a.Directed = intToBool(directed)
	return a, nil
}

func scanAssociations(rows *sql.Rows) ([]*Association, error) {
	var result []*Association
	for rows.Next() {
		a := &Association{}
		var createdAt string
		var directed int

		err := rows.Scan(&a.ID, &a.SourceID, &a.TargetID, &a.RelationType, &a.Weight, &directed, &createdAt)
		if err != nil {
			return nil, fmt.Errorf("scan association: %w", err)
		}

		a.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		a.Directed = intToBool(directed)
		result = append(result, a)
	}
	return result, rows.Err()
}
