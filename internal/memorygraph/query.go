package memorygraph

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// QueryOptions represents a query builder for memories
type QueryOptions struct {
	types           []Type
	username        string
	channel         string
	minImportance   *float32
	maxImportance   *float32
	minConfidence   *float32
	maxConfidence   *float32
	sinceCreated    *time.Time
	untilCreated    *time.Time
	sinceAccessed   *time.Time
	untilAccessed   *time.Time
	includeForgotten bool
	hasTriggerBefore *time.Time
	orderBy         string
	orderDesc       bool
	limit           int
	offset          int
}

// Query creates a new query builder
func Query() *QueryOptions {
	return &QueryOptions{
		orderBy:   "created_at",
		orderDesc: true,
		limit:     100,
	}
}

// Types filters by memory types
func (q *QueryOptions) Types(types ...Type) *QueryOptions {
	q.types = types
	return q
}

// Username filters by username
func (q *QueryOptions) Username(username string) *QueryOptions {
	q.username = username
	return q
}

// Channel filters by channel
func (q *QueryOptions) Channel(channel string) *QueryOptions {
	q.channel = channel
	return q
}

// MinImportance filters by minimum importance
func (q *QueryOptions) MinImportance(min float32) *QueryOptions {
	q.minImportance = &min
	return q
}

// MaxImportance filters by maximum importance
func (q *QueryOptions) MaxImportance(max float32) *QueryOptions {
	q.maxImportance = &max
	return q
}

// MinConfidence filters by minimum confidence
func (q *QueryOptions) MinConfidence(min float32) *QueryOptions {
	q.minConfidence = &min
	return q
}

// MaxConfidence filters by maximum confidence
func (q *QueryOptions) MaxConfidence(max float32) *QueryOptions {
	q.maxConfidence = &max
	return q
}

// SinceCreated filters by created_at >= time
func (q *QueryOptions) SinceCreated(t time.Time) *QueryOptions {
	q.sinceCreated = &t
	return q
}

// UntilCreated filters by created_at <= time
func (q *QueryOptions) UntilCreated(t time.Time) *QueryOptions {
	q.untilCreated = &t
	return q
}

// SinceAccessed filters by last_accessed_at >= time
func (q *QueryOptions) SinceAccessed(t time.Time) *QueryOptions {
	q.sinceAccessed = &t
	return q
}

// UntilAccessed filters by last_accessed_at <= time
func (q *QueryOptions) UntilAccessed(t time.Time) *QueryOptions {
	q.untilAccessed = &t
	return q
}

// IncludeForgotten includes soft-deleted memories
func (q *QueryOptions) IncludeForgotten() *QueryOptions {
	q.includeForgotten = true
	return q
}

// HasTriggerBefore returns memories with next_trigger_at before the given time
func (q *QueryOptions) HasTriggerBefore(t time.Time) *QueryOptions {
	q.hasTriggerBefore = &t
	return q
}

// OrderBy sets the order field (created_at, updated_at, importance, access_count)
func (q *QueryOptions) OrderBy(field string) *QueryOptions {
	validFields := map[string]bool{
		"created_at":       true,
		"updated_at":       true,
		"last_accessed_at": true,
		"importance":       true,
		"confidence":       true,
		"access_count":     true,
	}
	if validFields[field] {
		q.orderBy = field
	}
	return q
}

// Ascending sets order to ascending
func (q *QueryOptions) Ascending() *QueryOptions {
	q.orderDesc = false
	return q
}

// Descending sets order to descending
func (q *QueryOptions) Descending() *QueryOptions {
	q.orderDesc = true
	return q
}

// Limit sets the maximum number of results
func (q *QueryOptions) Limit(n int) *QueryOptions {
	if n > 0 {
		q.limit = n
	}
	return q
}

// Offset sets the offset for pagination
func (q *QueryOptions) Offset(n int) *QueryOptions {
	if n >= 0 {
		q.offset = n
	}
	return q
}

// Build constructs the SQL query and arguments
func (q *QueryOptions) Build() (string, []interface{}) {
	var conditions []string
	var args []interface{}

	// Base query
	query := `
		SELECT id, uuid, content, memory_type, importance, confidence,
			created_at, updated_at, last_accessed_at, access_count,
			next_trigger_at, source, source_session, source_message,
			username, channel, chat_id, forgotten, embedding, embedding_model
		FROM memories
	`

	// Type filter
	if len(q.types) > 0 {
		placeholders := make([]string, len(q.types))
		for i, t := range q.types {
			placeholders[i] = "?"
			args = append(args, string(t))
		}
		conditions = append(conditions, fmt.Sprintf("memory_type IN (%s)", strings.Join(placeholders, ", ")))
	}

	// Username filter
	if q.username != "" {
		conditions = append(conditions, "username = ?")
		args = append(args, q.username)
	}

	// Channel filter
	if q.channel != "" {
		conditions = append(conditions, "channel = ?")
		args = append(args, q.channel)
	}

	// Importance filters
	if q.minImportance != nil {
		conditions = append(conditions, "importance >= ?")
		args = append(args, *q.minImportance)
	}
	if q.maxImportance != nil {
		conditions = append(conditions, "importance <= ?")
		args = append(args, *q.maxImportance)
	}

	// Confidence filters
	if q.minConfidence != nil {
		conditions = append(conditions, "confidence >= ?")
		args = append(args, *q.minConfidence)
	}
	if q.maxConfidence != nil {
		conditions = append(conditions, "confidence <= ?")
		args = append(args, *q.maxConfidence)
	}

	// Time filters
	if q.sinceCreated != nil {
		conditions = append(conditions, "created_at >= ?")
		args = append(args, q.sinceCreated.Format(time.RFC3339))
	}
	if q.untilCreated != nil {
		conditions = append(conditions, "created_at <= ?")
		args = append(args, q.untilCreated.Format(time.RFC3339))
	}
	if q.sinceAccessed != nil {
		conditions = append(conditions, "last_accessed_at >= ?")
		args = append(args, q.sinceAccessed.Format(time.RFC3339))
	}
	if q.untilAccessed != nil {
		conditions = append(conditions, "last_accessed_at <= ?")
		args = append(args, q.untilAccessed.Format(time.RFC3339))
	}

	// Forgotten filter (default exclude)
	if !q.includeForgotten {
		conditions = append(conditions, "forgotten = 0")
	}

	// Trigger filter
	if q.hasTriggerBefore != nil {
		conditions = append(conditions, "next_trigger_at IS NOT NULL AND next_trigger_at <= ?")
		args = append(args, q.hasTriggerBefore.Format(time.RFC3339))
	}

	// Build WHERE clause
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	// Order by
	direction := "DESC"
	if !q.orderDesc {
		direction = "ASC"
	}
	query += fmt.Sprintf(" ORDER BY %s %s", q.orderBy, direction)

	// Pagination
	query += fmt.Sprintf(" LIMIT %d OFFSET %d", q.limit, q.offset)

	return query, args
}

// Execute runs the query and returns the results
func (q *QueryOptions) Execute(db *sql.DB) ([]*Memory, error) {
	query, args := q.Build()

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query memories: %w", err)
	}
	defer rows.Close()

	var memories []*Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		memories = append(memories, m)
	}

	return memories, rows.Err()
}

// Count returns the count of matching memories
func (q *QueryOptions) Count(db *sql.DB) (int, error) {
	query, args := q.Build()

	// Replace SELECT fields with COUNT(*)
	countQuery := "SELECT COUNT(*) FROM (" + query + ") AS subquery"

	var count int
	err := db.QueryRow(countQuery, args...).Scan(&count)
	return count, err
}

// AssociationQuery represents a query builder for associations
type AssociationQuery struct {
	memoryUUID   string
	direction    string // "from", "to", "both"
	types        []RelationType
	minWeight    *float32
	limit        int
}

// QueryAssociations creates a new association query builder
func QueryAssociations(memoryUUID string) *AssociationQuery {
	return &AssociationQuery{
		memoryUUID: memoryUUID,
		direction:  "both",
		limit:      100,
	}
}

// From filters associations originating from the memory
func (q *AssociationQuery) From() *AssociationQuery {
	q.direction = "from"
	return q
}

// To filters associations targeting the memory
func (q *AssociationQuery) To() *AssociationQuery {
	q.direction = "to"
	return q
}

// Both includes associations in both directions
func (q *AssociationQuery) Both() *AssociationQuery {
	q.direction = "both"
	return q
}

// Types filters by relation types
func (q *AssociationQuery) Types(types ...RelationType) *AssociationQuery {
	q.types = types
	return q
}

// MinWeight filters by minimum weight
func (q *AssociationQuery) MinWeight(w float32) *AssociationQuery {
	q.minWeight = &w
	return q
}

// Limit sets the maximum number of results
func (q *AssociationQuery) Limit(n int) *AssociationQuery {
	if n > 0 {
		q.limit = n
	}
	return q
}

// Execute runs the query and returns associations
func (q *AssociationQuery) Execute(db *sql.DB) ([]*Association, error) {
	var conditions []string
	var args []interface{}

	query := `
		SELECT id, source_uuid, target_uuid, relation_type, weight, directed, created_at
		FROM associations
	`

	// Direction filter
	switch q.direction {
	case "from":
		conditions = append(conditions, "source_uuid = ?")
		args = append(args, q.memoryUUID)
	case "to":
		conditions = append(conditions, "target_uuid = ?")
		args = append(args, q.memoryUUID)
	case "both":
		conditions = append(conditions, "(source_uuid = ? OR (target_uuid = ? AND directed = 0))")
		args = append(args, q.memoryUUID, q.memoryUUID)
	}

	// Type filter
	if len(q.types) > 0 {
		placeholders := make([]string, len(q.types))
		for i, t := range q.types {
			placeholders[i] = "?"
			args = append(args, string(t))
		}
		conditions = append(conditions, fmt.Sprintf("relation_type IN (%s)", strings.Join(placeholders, ", ")))
	}

	// Weight filter
	if q.minWeight != nil {
		conditions = append(conditions, "weight >= ?")
		args = append(args, *q.minWeight)
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	query += fmt.Sprintf(" LIMIT %d", q.limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query associations: %w", err)
	}
	defer rows.Close()

	return scanAssociations(rows)
}

// GetRelatedMemories returns memories connected to the given memory UUID
func GetRelatedMemories(db *sql.DB, memoryUUID string, depth int, types []RelationType) ([]*Memory, error) {
	if depth < 1 {
		depth = 1
	}
	if depth > 3 {
		depth = 3 // Cap depth to prevent expensive queries
	}

	visited := make(map[string]bool)
	visited[memoryUUID] = true

	var result []*Memory
	current := []string{memoryUUID}

	for d := 0; d < depth; d++ {
		if len(current) == 0 {
			break
		}

		// Build query for adjacent memories
		q := QueryAssociations(current[0]).Both()
		if len(types) > 0 {
			q.Types(types...)
		}

		var nextLevel []string

		for _, uuid := range current {
			assocs, err := QueryAssociations(uuid).Both().Types(types...).Execute(db)
			if err != nil {
				return nil, err
			}

			for _, assoc := range assocs {
				neighborUUID := assoc.TargetID
				if assoc.TargetID == uuid {
					neighborUUID = assoc.SourceID
				}

				if !visited[neighborUUID] {
					visited[neighborUUID] = true
					nextLevel = append(nextLevel, neighborUUID)

					// Fetch the memory
					mem, err := (&Store{db: db}).GetMemory(neighborUUID)
					if err != nil {
						return nil, err
					}
					if mem != nil && !mem.Forgotten {
						result = append(result, mem)
					}
				}
			}
		}

		current = nextLevel
	}

	return result, nil
}
