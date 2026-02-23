// Package embeddings provides status and rebuild functionality for embedding management.
package embeddings

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Manager handles embedding status and rebuild operations
type Manager struct {
	sessionsDB *sql.DB
	memoryDB   *sql.DB
	registry   *llm.Registry
	cfg        llm.LLMPurposeConfig
}

var (
	globalManager *Manager
	managerOnce   sync.Once
)

// InitManager initializes the global embeddings manager.
// Must be called once at startup before using embeddings commands.
func InitManager(sessionsDB, memoryDB *sql.DB, registry *llm.Registry, cfg llm.LLMPurposeConfig) *Manager {
	managerOnce.Do(func() {
		globalManager = &Manager{
			sessionsDB: sessionsDB,
			memoryDB:   memoryDB,
			registry:   registry,
			cfg:        cfg,
		}
	})
	return globalManager
}

// GetManager returns the global embeddings manager.
// Returns nil if InitManager hasn't been called.
func GetManager() *Manager {
	return globalManager
}

// ProgressFunc is called during rebuild to report progress
type ProgressFunc func(processed, total int, err error, done bool)

// --- Public API (uses singleton if available, otherwise uses passed params) ---

// Status represents the overall embedding status (public for gateway)
type Status struct {
	PrimaryModel string
	AutoRebuild  bool
	Transcript   TableStatus
	Memory       TableStatus
}

// TableStatus represents embedding status for a single table (public for gateway)
type TableStatus struct {
	TotalChunks       int
	PrimaryModelCount int
	NeedsRebuildCount int
	Models            []ModelCount
}

// ModelCount represents a model and its chunk count (public for gateway)
type ModelCount struct {
	Model     string
	Count     int
	IsPrimary bool
}

// GetStatus queries embedding status - for gateway backward compat
func GetStatus(sessionsDB, memoryDB *sql.DB, cfg llm.LLMPurposeConfig) (*Status, error) {
	if len(cfg.Models) == 0 {
		return nil, fmt.Errorf("no embedding models configured")
	}

	primaryModel := extractModelName(cfg.Models[0])

	s := &Status{
		PrimaryModel: primaryModel,
		AutoRebuild:  cfg.GetAutoRebuild(),
	}

	transcriptStatus, err := getTableStatusPublic(sessionsDB, "transcript_chunks", primaryModel)
	if err != nil {
		return nil, fmt.Errorf("query transcript_chunks: %w", err)
	}
	s.Transcript = *transcriptStatus

	if memoryDB != nil {
		memoryStatus, err := getTableStatusPublic(memoryDB, "memory_chunks", primaryModel)
		if err != nil {
			return nil, fmt.Errorf("query memory_chunks: %w", err)
		}
		s.Memory = *memoryStatus
	}

	return s, nil
}

func getTableStatusPublic(db *sql.DB, tableName, primaryModel string) (*TableStatus, error) {
	s := &TableStatus{
		Models: []ModelCount{},
	}

	err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", tableName)).Scan(&s.TotalChunks)
	if err != nil {
		return nil, fmt.Errorf("count total: %w", err)
	}

	rows, err := db.Query(fmt.Sprintf(`
		SELECT COALESCE(embedding_model, '(none)'), COUNT(*) 
		FROM %s 
		GROUP BY embedding_model
		ORDER BY COUNT(*) DESC
	`, tableName))
	if err != nil {
		return nil, fmt.Errorf("query by model: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var model string
		var count int
		if err := rows.Scan(&model, &count); err != nil {
			return nil, fmt.Errorf("scan model count: %w", err)
		}

		isPrimary := model == primaryModel
		s.Models = append(s.Models, ModelCount{
			Model:     model,
			Count:     count,
			IsPrimary: isPrimary,
		})

		if isPrimary {
			s.PrimaryModelCount = count
		} else {
			s.NeedsRebuildCount += count
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	return s, nil
}

// Rebuild re-embeds all chunks - for gateway backward compat
func Rebuild(ctx context.Context, sessionsDB, memoryDB *sql.DB, cfg llm.LLMPurposeConfig, registry *llm.Registry, batchSize int, force bool, onProgress ProgressFunc) error {
	// Create temp manager for this call
	m := &Manager{
		sessionsDB: sessionsDB,
		memoryDB:   memoryDB,
		registry:   registry,
		cfg:        cfg,
	}
	return m.Rebuild(ctx, force, onProgress)
}

// StatusString returns formatted embedding status as a string
func (m *Manager) StatusString() string {
	if m == nil {
		return "Embeddings manager not initialized"
	}

	if len(m.cfg.Models) == 0 {
		return "Embeddings not configured"
	}

	status, err := m.getStatus()
	if err != nil {
		return fmt.Sprintf("Error getting status: %v", err)
	}

	var sb strings.Builder
	sb.WriteString("Embeddings Status\n\n")
	sb.WriteString(fmt.Sprintf("Primary model: %s\n", status.PrimaryModel))
	sb.WriteString(fmt.Sprintf("Auto-rebuild: %v\n\n", status.AutoRebuild))

	// Models in DB
	sb.WriteString("Models in DB:\n")
	allModels := make(map[string]int)
	for _, mc := range status.Transcript.Models {
		allModels[mc.Model] += mc.Count
	}
	for _, mc := range status.Memory.Models {
		allModels[mc.Model] += mc.Count
	}
	for model, count := range allModels {
		if model == status.PrimaryModel {
			sb.WriteString(fmt.Sprintf("  [OK] %s: %d chunks\n", model, count))
		} else {
			sb.WriteString(fmt.Sprintf("  [!] %s: %d chunks\n", model, count))
		}
	}
	sb.WriteString("\n")

	// Transcript
	sb.WriteString(fmt.Sprintf("Transcripts: %d chunks\n", status.Transcript.TotalChunks))
	if status.Transcript.TotalChunks > 0 {
		sb.WriteString(fmt.Sprintf("  [OK] %d primary model\n", status.Transcript.PrimaryModelCount))
		if status.Transcript.NeedsRebuildCount > 0 {
			sb.WriteString(fmt.Sprintf("  [!] %d needs rebuild\n", status.Transcript.NeedsRebuildCount))
		}
	}

	// Memory
	if m.memoryDB != nil {
		sb.WriteString(fmt.Sprintf("\nMemory: %d chunks\n", status.Memory.TotalChunks))
		if status.Memory.TotalChunks > 0 {
			sb.WriteString(fmt.Sprintf("  [OK] %d primary model\n", status.Memory.PrimaryModelCount))
			if status.Memory.NeedsRebuildCount > 0 {
				sb.WriteString(fmt.Sprintf("  [!] %d needs rebuild\n", status.Memory.NeedsRebuildCount))
			}
		}
	}

	return sb.String()
}

// NeedsRebuild returns true if any chunks need rebuilding
func (m *Manager) NeedsRebuild() bool {
	if m == nil || len(m.cfg.Models) == 0 {
		return false
	}
	status, err := m.getStatus()
	if err != nil {
		return false
	}
	return status.Transcript.NeedsRebuildCount+status.Memory.NeedsRebuildCount > 0
}

// Rebuild re-embeds chunks. If force=true, rebuilds ALL chunks.
// If force=false, only rebuilds chunks that don't match the primary model.
// Runs in foreground - caller should run in goroutine if background needed.
func (m *Manager) Rebuild(ctx context.Context, force bool, onProgress ProgressFunc) error {
	if m == nil {
		return fmt.Errorf("embeddings manager not initialized")
	}

	if len(m.cfg.Models) == 0 {
		return fmt.Errorf("no embedding models configured")
	}

	primaryModelRef := m.cfg.Models[0]
	primaryModel := extractModelName(primaryModelRef)

	L_info("embeddings: rebuild starting", "primaryModel", primaryModel, "force", force)

	// Resolve primary model for embeddings (no failover)
	provider, err := m.registry.ResolveForPurpose(primaryModelRef, "embeddings")
	if err != nil {
		return fmt.Errorf("primary embedding model unavailable: %w", err)
	}

	// Get embedder interface
	embedder, ok := provider.(llm.LLMEmbedder)
	if !ok {
		return fmt.Errorf("provider does not support embeddings: %T", provider)
	}

	embeddingProvider := llm.NewLLMProviderAdapter(embedder)

	// Count total chunks to process
	totalTranscript := 0
	totalMemory := 0

	if force {
		// Force rebuild: count all chunks, then clear embedding_model to mark all for reprocessing
		if err := m.sessionsDB.QueryRow(`SELECT COUNT(*) FROM transcript_chunks`).Scan(&totalTranscript); err != nil {
			return fmt.Errorf("count transcript chunks: %w", err)
		}
		if m.memoryDB != nil {
			if err := m.memoryDB.QueryRow(`SELECT COUNT(*) FROM memory_chunks`).Scan(&totalMemory); err != nil {
				return fmt.Errorf("count memory chunks: %w", err)
			}
		}

		// Clear embedding_model so standard WHERE clause finds all chunks
		if _, err := m.sessionsDB.Exec(`UPDATE transcript_chunks SET embedding_model = NULL`); err != nil {
			return fmt.Errorf("clear transcript embedding_model: %w", err)
		}
		if m.memoryDB != nil {
			if _, err := m.memoryDB.Exec(`UPDATE memory_chunks SET embedding_model = NULL`); err != nil {
				return fmt.Errorf("clear memory embedding_model: %w", err)
			}
		}
		L_info("embeddings: cleared embedding_model for force rebuild")
	} else {
		if err := m.sessionsDB.QueryRow(`
			SELECT COUNT(*) FROM transcript_chunks 
			WHERE embedding_model IS NULL OR embedding_model != ?
		`, primaryModel).Scan(&totalTranscript); err != nil {
			return fmt.Errorf("count transcript chunks: %w", err)
		}
		if m.memoryDB != nil {
			if err := m.memoryDB.QueryRow(`
				SELECT COUNT(*) FROM memory_chunks 
				WHERE embedding_model IS NULL OR embedding_model != ?
			`, primaryModel).Scan(&totalMemory); err != nil {
				return fmt.Errorf("count memory chunks: %w", err)
			}
		}
	}

	total := totalTranscript + totalMemory
	if total == 0 {
		L_info("embeddings: nothing to rebuild")
		if onProgress != nil {
			onProgress(0, 0, nil, true)
		}
		return nil
	}

	L_info("embeddings: chunks to rebuild", "transcript", totalTranscript, "memory", totalMemory, "total", total)

	processed := 0
	batchSize := 50

	// Rebuild transcript chunks
	if totalTranscript > 0 {
		n, err := rebuildTable(ctx, m.sessionsDB, "transcript_chunks", "content", primaryModel, embeddingProvider, batchSize, processed, total, onProgress)
		processed += n
		if err != nil {
			if onProgress != nil {
				onProgress(processed, total, err, true)
			}
			return fmt.Errorf("rebuild transcript_chunks: %w", err)
		}
	}

	// Rebuild memory chunks
	if m.memoryDB != nil && totalMemory > 0 {
		n, err := rebuildTable(ctx, m.memoryDB, "memory_chunks", "text", primaryModel, embeddingProvider, batchSize, processed, total, onProgress)
		processed += n
		if err != nil {
			if onProgress != nil {
				onProgress(processed, total, err, true)
			}
			return fmt.Errorf("rebuild memory_chunks: %w", err)
		}
	}

	L_info("embeddings: rebuild completed", "processed", processed)
	if onProgress != nil {
		onProgress(processed, total, nil, true)
	}

	return nil
}

// GetConfig returns the embeddings config
func (m *Manager) GetConfig() llm.LLMPurposeConfig {
	if m == nil {
		return llm.LLMPurposeConfig{}
	}
	return m.cfg
}

// --- Internal helpers ---

type status struct {
	PrimaryModel string
	AutoRebuild  bool
	Transcript   tableStatus
	Memory       tableStatus
}

type tableStatus struct {
	TotalChunks       int
	PrimaryModelCount int
	NeedsRebuildCount int
	Models            []modelCount
}

type modelCount struct {
	Model     string
	Count     int
	IsPrimary bool
}

func (m *Manager) getStatus() (*status, error) {
	if m.sessionsDB == nil {
		return nil, fmt.Errorf("sessions database not available")
	}

	primaryModel := extractModelName(m.cfg.Models[0])

	s := &status{
		PrimaryModel: primaryModel,
		AutoRebuild:  m.cfg.GetAutoRebuild(),
	}

	// Query transcript_chunks
	transcriptStatus, err := getTableStatus(m.sessionsDB, "transcript_chunks", primaryModel)
	if err != nil {
		return nil, fmt.Errorf("query transcript_chunks: %w", err)
	}
	s.Transcript = *transcriptStatus

	// Query memory_chunks if available
	if m.memoryDB != nil {
		memoryStatus, err := getTableStatus(m.memoryDB, "memory_chunks", primaryModel)
		if err != nil {
			return nil, fmt.Errorf("query memory_chunks: %w", err)
		}
		s.Memory = *memoryStatus
	}

	return s, nil
}

func getTableStatus(db *sql.DB, tableName, primaryModel string) (*tableStatus, error) {
	s := &tableStatus{
		Models: []modelCount{},
	}

	// Get total count
	err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", tableName)).Scan(&s.TotalChunks)
	if err != nil {
		return nil, fmt.Errorf("count total: %w", err)
	}

	// Get counts by model
	rows, err := db.Query(fmt.Sprintf(`
		SELECT COALESCE(embedding_model, '(none)'), COUNT(*) 
		FROM %s 
		GROUP BY embedding_model
		ORDER BY COUNT(*) DESC
	`, tableName))
	if err != nil {
		return nil, fmt.Errorf("query by model: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var model string
		var count int
		if err := rows.Scan(&model, &count); err != nil {
			return nil, fmt.Errorf("scan model count: %w", err)
		}

		isPrimary := model == primaryModel
		s.Models = append(s.Models, modelCount{
			Model:     model,
			Count:     count,
			IsPrimary: isPrimary,
		})

		if isPrimary {
			s.PrimaryModelCount = count
		} else {
			s.NeedsRebuildCount += count
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	return s, nil
}

func rebuildTable(ctx context.Context, db *sql.DB, tableName, textColumn, primaryModel string, provider llm.EmbeddingProvider, batchSize, processedSoFar, total int, onProgress ProgressFunc) (int, error) {
	processed := 0
	progressInterval := 50

	for {
		select {
		case <-ctx.Done():
			return processed, ctx.Err()
		default:
		}

		// Get batch of chunks needing rebuild
		rows, err := db.Query(fmt.Sprintf(`
			SELECT id, %s FROM %s 
			WHERE embedding_model IS NULL OR embedding_model != ?
			LIMIT ?
		`, textColumn, tableName), primaryModel, batchSize)
		if err != nil {
			return processed, fmt.Errorf("query chunks: %w", err)
		}

		type chunk struct {
			id   string
			text string
		}
		var chunks []chunk

		for rows.Next() {
			var c chunk
			if err := rows.Scan(&c.id, &c.text); err != nil {
				rows.Close() //nolint:sqlclosecheck // can't defer in loop
				return processed, fmt.Errorf("scan chunk: %w", err)
			}
			chunks = append(chunks, c)
		}
		if err := rows.Err(); err != nil {
			rows.Close() //nolint:sqlclosecheck // can't defer in loop
			return processed, fmt.Errorf("iterate rows: %w", err)
		}
		rows.Close() //nolint:sqlclosecheck // can't defer in loop

		if len(chunks) == 0 {
			break
		}

		L_debug("embeddings: processing batch", "table", tableName, "chunks", len(chunks))

		// Generate embeddings for batch
		texts := make([]string, len(chunks))
		for i, c := range chunks {
			texts[i] = c.text
		}

		embeddings, err := provider.EmbedBatch(ctx, texts)
		if err != nil {
			return processed, fmt.Errorf("generate embeddings: %w", err)
		}

		// Update chunks with new embeddings
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return processed, fmt.Errorf("begin transaction: %w", err)
		}

		stmt, err := tx.Prepare(fmt.Sprintf(`
			UPDATE %s SET embedding = ?, embedding_model = ? WHERE id = ?
		`, tableName))
		if err != nil {
			tx.Rollback()
			return processed, fmt.Errorf("prepare update: %w", err)
		}

		for i, c := range chunks {
			embeddingBlob, _ := json.Marshal(embeddings[i])
			if _, err := stmt.Exec(embeddingBlob, primaryModel, c.id); err != nil {
				stmt.Close() //nolint:sqlclosecheck // can't defer in loop
				tx.Rollback()
				return processed, fmt.Errorf("update chunk %s: %w", c.id, err)
			}
			processed++

			if onProgress != nil && processed%progressInterval == 0 {
				onProgress(processedSoFar+processed, total, nil, false)
			}
		}

		stmt.Close() //nolint:sqlclosecheck // can't defer in loop
		if err := tx.Commit(); err != nil {
			return processed, fmt.Errorf("commit transaction: %w", err)
		}
	}

	return processed, nil
}

func extractModelName(ref string) string {
	parts := strings.SplitN(ref, "/", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return ref
}
