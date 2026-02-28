package memorygraph

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Maintainer handles periodic maintenance tasks on the memory graph
type Maintainer struct {
	db     *sql.DB
	config MaintenanceConfig
}

// NewMaintainer creates a new maintainer
func NewMaintainer(db *sql.DB, config MaintenanceConfig) *Maintainer {
	return &Maintainer{
		db:     db,
		config: config,
	}
}

// Run executes all maintenance tasks and returns a report
func (m *Maintainer) Run(ctx context.Context) (*MaintenanceReport, error) {
	report := &MaintenanceReport{}

	// 1. Apply importance decay
	decayed, err := m.decayImportance(ctx)
	if err != nil {
		L_warn("memorygraph: importance decay failed", "error", err)
	} else {
		report.ImportanceDecayed = decayed
	}

	// 2. Apply confidence decay to unconfirmed patterns
	confDecayed, err := m.decayConfidence(ctx)
	if err != nil {
		L_warn("memorygraph: confidence decay failed", "error", err)
	} else {
		report.ConfidenceDecayed = confDecayed
	}

	// 3. Boost accessed memories
	boosted, err := m.boostAccessed(ctx)
	if err != nil {
		L_warn("memorygraph: access boost failed", "error", err)
	} else {
		report.Boosted = boosted
	}

	// 4. Soft-delete low importance memories
	forgotten, err := m.forgetLowImportance(ctx)
	if err != nil {
		L_warn("memorygraph: forget low importance failed", "error", err)
	} else {
		report.Pruned += forgotten
	}

	// 5. Prune old forgotten memories
	pruned, err := m.pruneForgotten(ctx)
	if err != nil {
		L_warn("memorygraph: prune forgotten failed", "error", err)
	} else {
		report.Pruned += pruned
	}

	// 6. Update next trigger times for routines
	triggers, err := m.updateTriggers(ctx)
	if err != nil {
		L_warn("memorygraph: update triggers failed", "error", err)
	} else {
		report.TriggersUpdated = triggers
	}

	// 7. Detect and merge duplicates
	merged, err := m.mergeDuplicates(ctx)
	if err != nil {
		L_warn("memorygraph: merge duplicates failed", "error", err)
	} else {
		report.Merged = merged
	}

	L_info("memorygraph: maintenance completed",
		"importanceDecayed", report.ImportanceDecayed,
		"confidenceDecayed", report.ConfidenceDecayed,
		"boosted", report.Boosted,
		"pruned", report.Pruned,
		"merged", report.Merged,
		"triggersUpdated", report.TriggersUpdated,
	)

	return report, nil
}

// decayImportance applies daily decay to importance values
func (m *Maintainer) decayImportance(ctx context.Context) (int, error) {
	// Skip identity memories (never decay)
	result, err := m.db.ExecContext(ctx, `
		UPDATE memories SET
			importance = importance * ?,
			updated_at = ?
		WHERE forgotten = 0
		AND memory_type != 'identity'
		AND importance > ?
	`, m.config.ImportanceDecayRate, time.Now().Format(time.RFC3339), m.config.MinImportance)

	if err != nil {
		return 0, err
	}

	rows, _ := result.RowsAffected()
	return int(rows), nil
}

// decayConfidence applies decay to unconfirmed pattern memories
func (m *Maintainer) decayConfidence(ctx context.Context) (int, error) {
	// Only decay pattern types with non-sentinel confidence
	patternTypes := []Type{TypeRoutine, TypeCorrelation, TypePrediction}
	args := []interface{}{m.config.ConfidenceDecayRate, time.Now().Format(time.RFC3339), m.config.MinConfidence}

	placeholders := ""
	for i, t := range patternTypes {
		if i > 0 {
			placeholders += ", "
		}
		placeholders += "?"
		args = append(args, string(t))
	}

	result, err := m.db.ExecContext(ctx, `
		UPDATE memories SET
			confidence = confidence * ?,
			updated_at = ?
		WHERE forgotten = 0
		AND confidence > ?
		AND confidence != -1
		AND memory_type IN (`+placeholders+`)
	`, args...)

	if err != nil {
		return 0, err
	}

	rows, _ := result.RowsAffected()
	return int(rows), nil
}

// boostAccessed increases importance of recently accessed memories
func (m *Maintainer) boostAccessed(ctx context.Context) (int, error) {
	// Boost memories accessed in the last 24 hours
	cutoff := time.Now().Add(-24 * time.Hour).Format(time.RFC3339)

	result, err := m.db.ExecContext(ctx, `
		UPDATE memories SET
			importance = MIN(importance + ?, ?),
			updated_at = ?
		WHERE forgotten = 0
		AND last_accessed_at > ?
		AND access_count > 0
	`, m.config.AccessBoostAmount, m.config.MaxImportance, time.Now().Format(time.RFC3339), cutoff)

	if err != nil {
		return 0, err
	}

	rows, _ := result.RowsAffected()
	return int(rows), nil
}

// forgetLowImportance soft-deletes memories below the minimum importance threshold
func (m *Maintainer) forgetLowImportance(ctx context.Context) (int, error) {
	result, err := m.db.ExecContext(ctx, `
		UPDATE memories SET
			forgotten = 1,
			updated_at = ?
		WHERE forgotten = 0
		AND memory_type != 'identity'
		AND importance < ?
	`, time.Now().Format(time.RFC3339), m.config.MinImportance)

	if err != nil {
		return 0, err
	}

	rows, _ := result.RowsAffected()
	return int(rows), nil
}

// pruneForgotten permanently deletes old forgotten memories
func (m *Maintainer) pruneForgotten(ctx context.Context) (int, error) {
	cutoff := time.Now().AddDate(0, 0, -m.config.PruneAfterDays).Format(time.RFC3339)

	result, err := m.db.ExecContext(ctx, `
		DELETE FROM memories
		WHERE forgotten = 1
		AND updated_at < ?
	`, cutoff)

	if err != nil {
		return 0, err
	}

	rows, _ := result.RowsAffected()
	return int(rows), nil
}

// updateTriggers calculates next trigger times for routines
func (m *Maintainer) updateTriggers(ctx context.Context) (int, error) {
	// Get routines with cron triggers
	rows, err := m.db.QueryContext(ctx, `
		SELECT m.uuid, rm.trigger_cron
		FROM memories m
		JOIN routine_metadata rm ON rm.memory_uuid = m.uuid
		WHERE m.forgotten = 0
		AND m.memory_type = 'routine'
		AND rm.trigger_type = 'time'
		AND rm.trigger_cron IS NOT NULL
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var updated int
	now := time.Now()

	for rows.Next() {
		var uuid, cronStr string
		if err := rows.Scan(&uuid, &cronStr); err != nil {
			continue
		}

		// Parse cron and calculate next trigger
		nextTrigger := calculateNextCronTime(cronStr, now)
		if nextTrigger.IsZero() {
			continue
		}

		_, err := m.db.ExecContext(ctx, `
			UPDATE memories SET next_trigger_at = ? WHERE uuid = ?
		`, nextTrigger.Format(time.RFC3339), uuid)
		if err == nil {
			updated++
		}
	}

	return updated, rows.Err()
}

// mergeDuplicates finds and merges highly similar memories
func (m *Maintainer) mergeDuplicates(ctx context.Context) (int, error) {
	if m.config.DuplicateSimilarity <= 0 {
		return 0, nil
	}

	// Get memories with embeddings
	rows, err := m.db.QueryContext(ctx, `
		SELECT uuid, content, embedding, importance, access_count, created_at
		FROM memories 
		WHERE forgotten = 0 
		AND embedding IS NOT NULL
		ORDER BY importance DESC, created_at ASC
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type memoryInfo struct {
		uuid        string
		content     string
		embedding   []float32
		importance  float64
		accessCount int64
		createdAt   time.Time
	}

	var memories []memoryInfo
	for rows.Next() {
		var m memoryInfo
		var embeddingBlob []byte
		var createdAt string

		if err := rows.Scan(&m.uuid, &m.content, &embeddingBlob, &m.importance, &m.accessCount, &createdAt); err != nil {
			continue
		}

		if err := json.Unmarshal(embeddingBlob, &m.embedding); err != nil {
			continue
		}

		m.createdAt, _ = time.Parse(time.RFC3339, createdAt)
		memories = append(memories, m)
	}

	if err := rows.Err(); err != nil {
		return 0, err
	}

	// Find duplicates
	merged := 0
	mergedUUIDs := make(map[string]bool)

	for i := 0; i < len(memories); i++ {
		if mergedUUIDs[memories[i].uuid] {
			continue
		}

		for j := i + 1; j < len(memories); j++ {
			if mergedUUIDs[memories[j].uuid] {
				continue
			}

			sim := cosineSimilarity(memories[i].embedding, memories[j].embedding)
			if sim >= m.config.DuplicateSimilarity {
				// Merge j into i (i has higher importance or was created earlier)
				keeper := memories[i]
				duplicate := memories[j]

				// Update keeper with merged stats
				newAccessCount := keeper.accessCount + duplicate.accessCount
				newImportance := math.Max(keeper.importance, duplicate.importance)

				_, err := m.db.ExecContext(ctx, `
					UPDATE memories SET
						access_count = ?,
						importance = ?,
						updated_at = ?
					WHERE uuid = ?
				`, newAccessCount, newImportance, time.Now().Format(time.RFC3339), keeper.uuid)
				if err != nil {
					continue
				}

				// Create association showing merge
				_, err = m.db.ExecContext(ctx, `
					INSERT OR IGNORE INTO associations (id, source_uuid, target_uuid, relation_type, weight, directed, created_at)
					VALUES (?, ?, ?, 'updates', 1.0, 1, ?)
				`, generateULID(), keeper.uuid, duplicate.uuid, time.Now().Format(time.RFC3339))
				if err != nil {
					L_debug("memorygraph: failed to create merge association", "error", err)
				}

				// Mark duplicate as forgotten
				_, err = m.db.ExecContext(ctx, `
					UPDATE memories SET forgotten = 1, updated_at = ? WHERE uuid = ?
				`, time.Now().Format(time.RFC3339), duplicate.uuid)
				if err != nil {
					continue
				}

				mergedUUIDs[duplicate.uuid] = true
				merged++

				L_debug("memorygraph: merged duplicate",
					"keeper", keeper.uuid,
					"duplicate", duplicate.uuid,
					"similarity", sim,
				)
			}
		}
	}

	return merged, nil
}

// calculateNextCronTime calculates the next trigger time for a cron expression
// Supports: minute hour day-of-month month day-of-week (standard 5-field cron)
func calculateNextCronTime(cronStr string, from time.Time) time.Time {
	// Simple cron parser - supports basic cron expressions
	// For production, consider using a library like github.com/robfig/cron
	
	// This is a placeholder implementation
	// TODO: Implement proper cron parsing or use a library
	
	// For now, just return next hour as a default
	next := from.Truncate(time.Hour).Add(time.Hour)
	return next
}

// RunDecayOnly runs only the decay operations (for testing)
func (m *Maintainer) RunDecayOnly(ctx context.Context) (*MaintenanceReport, error) {
	report := &MaintenanceReport{}

	decayed, err := m.decayImportance(ctx)
	if err != nil {
		return report, err
	}
	report.ImportanceDecayed = decayed

	confDecayed, err := m.decayConfidence(ctx)
	if err != nil {
		return report, err
	}
	report.ConfidenceDecayed = confDecayed

	return report, nil
}

// RunPruneOnly runs only the pruning operations (for testing)
func (m *Maintainer) RunPruneOnly(ctx context.Context) (*MaintenanceReport, error) {
	report := &MaintenanceReport{}

	forgotten, err := m.forgetLowImportance(ctx)
	if err != nil {
		return report, err
	}
	report.Pruned = forgotten

	pruned, err := m.pruneForgotten(ctx)
	if err != nil {
		return report, err
	}
	report.Pruned += pruned

	return report, nil
}
