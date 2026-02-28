package memorygraph

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// IngestionSource provides items for ingestion into the memory graph
type IngestionSource interface {
	// Type returns the source type identifier (e.g., "markdown", "transcript")
	Type() string
	// Scan returns a channel of items to ingest
	Scan(ctx context.Context) (<-chan IngestItem, error)
}

// IngestItem represents a single item to be ingested
type IngestItem struct {
	SourcePath  string            // Unique identifier within the source (file path, chunk ID)
	Content     string            // The content to extract memories from
	ContentHash string            // SHA256 hash of content for change detection
	Metadata    map[string]string // Additional metadata (filename, timestamp, etc.)
}

// IngestReport summarizes the results of an ingestion run
type IngestReport struct {
	SourceType string
	Scanned    int // Total items scanned
	Skipped    int // Items skipped (unchanged hash)
	Extracted  int // Memories created
	Errors     int // Items that failed
	Duration   time.Duration
}

// IngestionState tracks what has been ingested
type IngestionState struct {
	SourceType  string
	SourcePath  string
	ContentHash string
	IngestedAt  time.Time
	MemoryCount int
}

// HashContent computes the SHA256 hash of content
func HashContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// Ingest processes items from a source and extracts memories
// For transcript sources, use IngestWithBatching for better efficiency
func Ingest(ctx context.Context, mgr *Manager, provider llm.Provider, source IngestionSource, username string) (*IngestReport, error) {
	return IngestWithBatching(ctx, mgr, provider, source, username, 1)
}

// IngestWithBatching processes items with configurable batch size
// batchSize > 1 combines multiple items into a single LLM call (useful for transcripts)
// totalItems is optional (pass 0 if unknown) - used for progress display
func IngestWithBatching(ctx context.Context, mgr *Manager, provider llm.Provider, source IngestionSource, username string, batchSize int) (*IngestReport, error) {
	return IngestWithBatchingAndTotal(ctx, mgr, provider, source, username, batchSize, 0)
}

// IngestWithBatchingAndTotal processes items with configurable batch size and known total
func IngestWithBatchingAndTotal(ctx context.Context, mgr *Manager, provider llm.Provider, source IngestionSource, username string, batchSize, totalItems int) (*IngestReport, error) {
	if batchSize < 1 {
		batchSize = 1
	}

	start := time.Now()
	report := &IngestReport{
		SourceType: source.Type(),
	}

	// Create extractor with the LLM provider
	extractor := NewExtractor(mgr)
	extractor.SetProvider(provider)

	// Start scanning
	items, err := source.Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("scan source: %w", err)
	}

	// Collect items for batching
	var batch []IngestItem
	batchNum := 0

	processBatch := func() error {
		if len(batch) == 0 {
			return nil
		}
		batchNum++

		// Build combined content for batch
		var combinedContent string
		var sourcePaths []string

		for i, item := range batch {
			if i > 0 {
				combinedContent += "\n\n--- CONVERSATION SEGMENT ---\n\n"
			}
			combinedContent += item.Content
			sourcePaths = append(sourcePaths, item.SourcePath)
		}

		// Extract memories from combined batch
		ec := ExtractionContext{
			Username:     username,
			Conversation: combinedContent,
			SourceFile:   fmt.Sprintf("batch of %d chunks", len(batch)),
			SourceType:   source.Type(),
		}

		// Use metadata from first item for context
		if len(batch) > 0 && batch[0].Metadata != nil {
			if ch, ok := batch[0].Metadata["channel"]; ok {
				ec.Channel = ch
			}
			if sess, ok := batch[0].Metadata["session"]; ok {
				ec.SessionKey = sess
			}
		}

		result, err := extractor.Extract(ctx, ec)
		if err != nil {
			L_warn("memorygraph: batch extraction failed", "paths", sourcePaths, "error", err)
			report.Errors += len(batch)
			batch = nil
			return nil // Continue with next batch
		}

		memoryCount := len(result.Memories)
		report.Extracted += memoryCount

		// Update ingestion state for each item in batch
		memoriesPerItem := memoryCount / len(batch)
		if memoriesPerItem < 1 {
			memoriesPerItem = 1
		}

		for _, item := range batch {
			if err := setIngestionState(mgr.db, &IngestionState{
				SourceType:  source.Type(),
				SourcePath:  item.SourcePath,
				ContentHash: item.ContentHash,
				IngestedAt:  time.Now(),
				MemoryCount: memoriesPerItem,
			}); err != nil {
				L_warn("memorygraph: failed to update ingestion state", "path", item.SourcePath, "error", err)
			}
		}

		// Build progress string
		progressStr := fmt.Sprintf("%d", report.Scanned)
		if totalItems > 0 {
			pct := float64(report.Scanned) / float64(totalItems) * 100
			progressStr = fmt.Sprintf("%d/%d (%.1f%%)", report.Scanned, totalItems, pct)
		}

		L_info("memorygraph: batch ingested",
			"progress", progressStr,
			"batch", batchNum,
			"chunks", len(batch),
			"memories", memoryCount,
			"skipped", result.Skipped)
		batch = nil
		return nil
	}

	for item := range items {
		select {
		case <-ctx.Done():
			report.Duration = time.Since(start)
			return report, ctx.Err()
		default:
		}

		report.Scanned++

		// Check if already ingested with same hash
		state, err := getIngestionState(mgr.db, source.Type(), item.SourcePath)
		if err != nil {
			L_warn("memorygraph: failed to check ingestion state", "path", item.SourcePath, "error", err)
		}

		if state != nil && state.ContentHash == item.ContentHash {
			L_debug("memorygraph: skipping unchanged", "path", item.SourcePath)
			report.Skipped++
			continue
		}

		// For batch size 1, process immediately (original behavior)
		if batchSize == 1 {
			ec := ExtractionContext{
				Username:     username,
				Conversation: item.Content,
				SourceFile:   item.SourcePath,
				SourceType:   source.Type(),
			}

			if item.Metadata != nil {
				if ch, ok := item.Metadata["channel"]; ok {
					ec.Channel = ch
				}
				if sess, ok := item.Metadata["session"]; ok {
					ec.SessionKey = sess
				}
			}

			result, err := extractor.Extract(ctx, ec)
			if err != nil {
				L_warn("memorygraph: extraction failed", "path", item.SourcePath, "error", err)
				report.Errors++
				continue
			}

			memoryCount := len(result.Memories)
			report.Extracted += memoryCount

			if err := setIngestionState(mgr.db, &IngestionState{
				SourceType:  source.Type(),
				SourcePath:  item.SourcePath,
				ContentHash: item.ContentHash,
				IngestedAt:  time.Now(),
				MemoryCount: memoryCount,
			}); err != nil {
				L_warn("memorygraph: failed to update ingestion state", "path", item.SourcePath, "error", err)
			}

			L_info("memorygraph: ingested", "path", item.SourcePath, "memories", memoryCount, "skipped", result.Skipped)
			continue
		}

		// Batching mode: collect items
		batch = append(batch, item)

		// Process batch when full
		if len(batch) >= batchSize {
			if err := processBatch(); err != nil {
				return report, err
			}
		}
	}

	// Process remaining items in final batch
	if err := processBatch(); err != nil {
		return report, err
	}

	report.Duration = time.Since(start)
	return report, nil
}

// getIngestionState retrieves the ingestion state for a source item
func getIngestionState(db *sql.DB, sourceType, sourcePath string) (*IngestionState, error) {
	row := db.QueryRow(`
		SELECT source_type, source_path, content_hash, ingested_at, memory_count
		FROM ingestion_state
		WHERE source_type = ? AND source_path = ?
	`, sourceType, sourcePath)

	state := &IngestionState{}
	var ingestedAt string

	err := row.Scan(&state.SourceType, &state.SourcePath, &state.ContentHash, &ingestedAt, &state.MemoryCount)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	state.IngestedAt, _ = time.Parse(time.RFC3339, ingestedAt)
	return state, nil
}

// setIngestionState updates or inserts the ingestion state for a source item
func setIngestionState(db *sql.DB, state *IngestionState) error {
	_, err := db.Exec(`
		INSERT INTO ingestion_state (source_type, source_path, content_hash, ingested_at, memory_count)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(source_type, source_path) DO UPDATE SET
			content_hash = excluded.content_hash,
			ingested_at = excluded.ingested_at,
			memory_count = excluded.memory_count
	`, state.SourceType, state.SourcePath, state.ContentHash, state.IngestedAt.Format(time.RFC3339), state.MemoryCount)
	return err
}

// GetIngestionStats returns statistics about ingestion
func GetIngestionStats(db *sql.DB) (map[string]int, error) {
	rows, err := db.Query(`
		SELECT source_type, COUNT(*), SUM(memory_count)
		FROM ingestion_state
		GROUP BY source_type
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := make(map[string]int)
	for rows.Next() {
		var sourceType string
		var count, memoryCount int
		if err := rows.Scan(&sourceType, &count, &memoryCount); err != nil {
			continue
		}
		stats[sourceType+"_sources"] = count
		stats[sourceType+"_memories"] = memoryCount
	}

	return stats, rows.Err()
}

// ClearIngestionState removes all ingestion state (for re-ingestion)
func ClearIngestionState(db *sql.DB, sourceType string) error {
	if sourceType == "" {
		_, err := db.Exec("DELETE FROM ingestion_state")
		return err
	}
	_, err := db.Exec("DELETE FROM ingestion_state WHERE source_type = ?", sourceType)
	return err
}
