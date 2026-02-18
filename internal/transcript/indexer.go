package transcript

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/config"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/memory"
)

// Indexer manages background indexing of session messages
type Indexer struct {
	db       *sql.DB
	provider memory.EmbeddingProvider
	config   config.TranscriptConfig

	syncing  atomic.Bool
	stopChan chan struct{}
	syncChan chan struct{}
	wg       sync.WaitGroup
	mu       sync.RWMutex

	// Stats
	lastSync      time.Time
	chunksIndexed int

	// Identity
	agentName string // Agent's display name for transcript labels
}

// NewIndexer creates a new transcript indexer
func NewIndexer(db *sql.DB, provider memory.EmbeddingProvider, cfg config.TranscriptConfig) *Indexer {
	// Apply defaults for zero values
	if cfg.IndexIntervalSeconds <= 0 {
		cfg.IndexIntervalSeconds = 30
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	if cfg.BackfillBatchSize <= 0 {
		cfg.BackfillBatchSize = 10
	}
	if cfg.MaxGroupGapSeconds <= 0 {
		cfg.MaxGroupGapSeconds = 300
	}
	if cfg.MaxMessagesPerChunk <= 0 {
		cfg.MaxMessagesPerChunk = 8
	}
	if cfg.MaxEmbeddingContentLen <= 0 {
		cfg.MaxEmbeddingContentLen = 16000
	}

	return &Indexer{
		db:        db,
		provider:  provider,
		config:    cfg,
		stopChan:  make(chan struct{}),
		syncChan:  make(chan struct{}, 1),
		agentName: "GoClaw", // Default
	}
}

// SetAgentName sets the agent's display name for transcript labels
func (idx *Indexer) SetAgentName(name string) {
	idx.agentName = name
}

// UpdateConfig updates the indexer configuration
func (idx *Indexer) UpdateConfig(cfg config.TranscriptConfig) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Apply defaults for zero values
	if cfg.IndexIntervalSeconds <= 0 {
		cfg.IndexIntervalSeconds = 30
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	if cfg.BackfillBatchSize <= 0 {
		cfg.BackfillBatchSize = 10
	}
	if cfg.MaxGroupGapSeconds <= 0 {
		cfg.MaxGroupGapSeconds = 300
	}
	if cfg.MaxMessagesPerChunk <= 0 {
		cfg.MaxMessagesPerChunk = 8
	}
	if cfg.MaxEmbeddingContentLen <= 0 {
		cfg.MaxEmbeddingContentLen = 16000
	}

	idx.config = cfg
	L_debug("transcript: config updated", "enabled", cfg.Enabled)
}

// Start begins the background indexer goroutine
func (idx *Indexer) Start() {
	L_info("transcript: starting indexer")

	idx.wg.Add(1)
	go idx.loop()
}

// Stop stops the indexer gracefully
func (idx *Indexer) Stop() {
	L_info("transcript: stopping indexer")
	close(idx.stopChan)
	idx.wg.Wait()
	L_debug("transcript: indexer stopped")
}

// TriggerSync requests a sync (non-blocking)
func (idx *Indexer) TriggerSync() {
	select {
	case idx.syncChan <- struct{}{}:
		L_trace("transcript: sync triggered")
	default:
		// Already a sync pending
	}
}

// IsSyncing returns true if a sync is in progress
func (idx *Indexer) IsSyncing() bool {
	return idx.syncing.Load()
}

// loop is the main indexer goroutine
func (idx *Indexer) loop() {
	defer idx.wg.Done()

	interval := time.Duration(idx.config.IndexIntervalSeconds) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial sync after a short delay
	time.AfterFunc(5*time.Second, func() {
		idx.TriggerSync()
	})

	for {
		select {
		case <-idx.stopChan:
			L_debug("transcript: indexer received stop signal")
			return

		case <-ticker.C:
			idx.runSync()
			// After processing new messages, backfill old chunks without embeddings
			idx.runBackfill()

		case <-idx.syncChan:
			idx.runSync()
		}
	}
}

// runSync performs the actual indexing operation
func (idx *Indexer) runSync() {
	if idx.syncing.Load() {
		L_trace("transcript: sync already in progress")
		return
	}
	idx.syncing.Store(true)
	defer idx.syncing.Store(false)

	startTime := time.Now()
	ctx := context.Background()

	// Get unindexed messages
	messages, err := idx.getUnindexedMessages(ctx)
	if err != nil {
		L_error("transcript: failed to get unindexed messages", "error", err)
		return
	}

	if len(messages) == 0 {
		L_trace("transcript: no unindexed messages")
		return
	}

	// Only DEBUG log if processing more than a couple messages
	if len(messages) > 2 {
		L_debug("transcript: processing unindexed messages", "count", len(messages))
	} else {
		L_trace("transcript: processing unindexed messages", "count", len(messages))
	}

	// Group messages into conversation chunks
	chunks := idx.groupMessages(messages)

	if len(chunks) > 0 {
		L_debug("transcript: grouped into chunks", "chunks", len(chunks))
	}

	// Generate embeddings and store chunks
	chunksCreated := 0
	chunksDeferred := 0
	for _, chunk := range chunks {
		if err := idx.indexChunk(ctx, chunk); err != nil {
			L_warn("transcript: failed to index chunk", "error", err)
			chunksDeferred++
			continue
		}
		chunksCreated++
	}

	// Update stats
	idx.mu.Lock()
	idx.lastSync = time.Now()
	idx.chunksIndexed += chunksCreated
	idx.mu.Unlock()

	// Get remaining count for progress
	remaining := idx.PendingCount()
	totalIndexable := idx.TotalIndexableCount()
	indexed := totalIndexable - remaining
	progress := "0%"
	if totalIndexable > 0 {
		progress = fmt.Sprintf("%d/%d (%.0f%%)", indexed, totalIndexable, float64(indexed)/float64(totalIndexable)*100)
	}

	elapsed := time.Since(startTime)

	// Only log at INFO when actual chunks were created, otherwise TRACE
	if chunksCreated > 0 || chunksDeferred > 0 {
		L_info("transcript: sync completed",
			"messagesProcessed", len(messages),
			"chunksCreated", chunksCreated,
			"chunksDeferred", chunksDeferred,
			"progress", progress,
			"remaining", remaining,
			"elapsed", elapsed.String(),
		)
	} else {
		L_trace("transcript: sync completed (no chunks)",
			"messagesProcessed", len(messages),
			"progress", progress,
		)
	}
}

// runBackfill adds embeddings to chunks that were created without them
func (idx *Indexer) runBackfill() {
	// Only backfill if we have a working embedding provider
	if idx.provider == nil || !idx.provider.Available() {
		return
	}

	ctx := context.Background()

	// Get count of chunks needing embeddings
	needingCount := idx.ChunksNeedingEmbeddings()
	if needingCount == 0 {
		return
	}

	// Process a batch of chunks (limit to avoid overwhelming the embedding provider)
	backfillBatchSize := idx.config.BackfillBatchSize

	rows, err := idx.db.QueryContext(ctx, `
		SELECT id, content FROM transcript_chunks
		WHERE embedding IS NULL
		ORDER BY created_at ASC
		LIMIT ?
	`, backfillBatchSize)
	if err != nil {
		L_warn("transcript: backfill query failed", "error", err)
		return
	}
	defer rows.Close()

	var chunks []struct {
		id      string
		content string
	}
	for rows.Next() {
		var c struct {
			id      string
			content string
		}
		if err := rows.Scan(&c.id, &c.content); err != nil {
			continue
		}
		chunks = append(chunks, c)
	}
	if err := rows.Err(); err != nil {
		L_warn("transcript: backfill row iteration error", "error", err)
		return
	}

	if len(chunks) == 0 {
		return
	}

	startTime := time.Now()
	successCount := 0
	failCount := 0

	for _, chunk := range chunks {
		// Truncate content if too long
		contentToEmbed := chunk.content
		maxLen := idx.config.MaxEmbeddingContentLen
		if len(contentToEmbed) > maxLen {
			contentToEmbed = contentToEmbed[:maxLen]
		}

		embedding, err := idx.provider.EmbedQuery(ctx, contentToEmbed)
		if err != nil {
			L_debug("transcript: backfill embedding failed", "chunkID", chunk.id, "error", err)
			failCount++
			continue
		}

		if embedding == nil {
			failCount++
			continue
		}

		embeddingBlob, _ := json.Marshal(embedding)
		embeddingModel := idx.provider.Model()

		_, err = idx.db.ExecContext(ctx, `
			UPDATE transcript_chunks 
			SET embedding = ?, embedding_model = ?
			WHERE id = ?
		`, embeddingBlob, embeddingModel, chunk.id)
		if err != nil {
			L_warn("transcript: backfill update failed", "chunkID", chunk.id, "error", err)
			failCount++
			continue
		}

		successCount++
	}

	elapsed := time.Since(startTime)
	remaining := needingCount - successCount

	// Always log backfill progress at INFO level so user can see it
	if successCount > 0 || failCount > 0 {
		L_info("transcript: backfill progress",
			"processed", successCount,
			"failed", failCount,
			"remaining", remaining,
			"elapsed", elapsed.Round(time.Millisecond),
		)
	}
}

// getUnindexedMessages fetches messages that haven't been indexed yet
func (idx *Indexer) getUnindexedMessages(ctx context.Context) ([]*Message, error) {
	rows, err := idx.db.QueryContext(ctx, `
		SELECT id, session_key, timestamp, role, content, user_id
		FROM messages
		WHERE transcript_indexed_at IS NULL
		  AND role IN ('user', 'assistant')
		ORDER BY session_key, timestamp
		LIMIT ?
	`, idx.config.BatchSize)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	var messages []*Message
	for rows.Next() {
		var msg Message
		var userID sql.NullString
		if err := rows.Scan(&msg.ID, &msg.SessionKey, &msg.Timestamp, &msg.Role, &msg.Content, &userID); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		if userID.Valid {
			msg.UserID = userID.String
		}
		messages = append(messages, &msg)
	}

	return messages, rows.Err()
}

// ConversationChunk represents a group of related messages
type ConversationChunk struct {
	Messages       []*Message
	SessionKey     string
	UserID         string
	TimestampStart int64
	TimestampEnd   int64
	Content        string // Combined, cleaned content
}

// groupMessages groups consecutive messages into conversation chunks
func (idx *Indexer) groupMessages(messages []*Message) []*ConversationChunk {
	if len(messages) == 0 {
		return nil
	}

	var chunks []*ConversationChunk
	var currentChunk *ConversationChunk

	for _, msg := range messages {
		// Skip messages that shouldn't be indexed
		if !ShouldIndex(msg) {
			// Still mark as indexed so we don't keep checking
			idx.markAsIndexed(msg.ID)
			continue
		}

		// Start new chunk if needed:
		// - First message
		// - Different session
		// - Different user
		// - Time gap too large
		// - Current chunk at message limit
		maxGap := int64(idx.config.MaxGroupGapSeconds)
		needNewChunk := currentChunk == nil ||
			currentChunk.SessionKey != msg.SessionKey ||
			currentChunk.UserID != msg.UserID ||
			msg.Timestamp-currentChunk.TimestampEnd > maxGap ||
			len(currentChunk.Messages) >= idx.config.MaxMessagesPerChunk

		if needNewChunk {
			// Save current chunk
			if currentChunk != nil && len(currentChunk.Messages) > 0 {
				currentChunk.Content = idx.buildChunkContent(currentChunk.Messages)
				chunks = append(chunks, currentChunk)
			}

			// Start new chunk
			currentChunk = &ConversationChunk{
				SessionKey:     msg.SessionKey,
				UserID:         msg.UserID,
				TimestampStart: msg.Timestamp,
			}
		}

		currentChunk.Messages = append(currentChunk.Messages, msg)
		currentChunk.TimestampEnd = msg.Timestamp
	}

	// Don't forget last chunk
	if currentChunk != nil && len(currentChunk.Messages) > 0 {
		currentChunk.Content = idx.buildChunkContent(currentChunk.Messages)
		chunks = append(chunks, currentChunk)
	}

	return chunks
}

// buildChunkContent builds the combined content for a chunk
func (idx *Indexer) buildChunkContent(messages []*Message) string {
	var parts []string
	for _, msg := range messages {
		var label string
		if msg.Role == "user" {
			// Use actual user ID/name for searchability
			if msg.UserID != "" {
				label = msg.UserID
			} else {
				label = "User"
			}
		} else if msg.Role == "assistant" {
			label = idx.agentName
		} else {
			label = msg.Role
		}
		cleaned := CleanContent(msg.Content)
		parts = append(parts, fmt.Sprintf("%s: %s", label, cleaned))
	}
	return strings.Join(parts, "\n\n")
}

// indexChunk stores a conversation chunk with embedding
func (idx *Indexer) indexChunk(ctx context.Context, chunk *ConversationChunk) error {
	// Generate chunk ID from content hash
	hash := sha256.Sum256([]byte(chunk.Content))
	chunkID := hex.EncodeToString(hash[:16])

	// Collect message IDs
	messageIDs := make([]string, len(chunk.Messages))
	for i, msg := range chunk.Messages {
		messageIDs[i] = msg.ID
	}
	messageIDsJSON, _ := json.Marshal(messageIDs)

	contentLen := len(chunk.Content)

	// Generate embedding if provider available
	var embeddingBlob []byte
	var embeddingModel string
	var embeddingFailed bool

	if idx.provider != nil && idx.provider.Available() {
		// Truncate content if too long for embedding model
		contentToEmbed := chunk.Content
		maxLen := idx.config.MaxEmbeddingContentLen
		if contentLen > maxLen {
			contentToEmbed = chunk.Content[:maxLen]
			L_debug("transcript: truncating content for embedding",
				"originalLength", contentLen,
				"truncatedLength", maxLen,
				"chunkID", chunkID,
			)
		}

		embedding, err := idx.provider.EmbedQuery(ctx, contentToEmbed)
		if err != nil {
			L_warn("transcript: failed to generate embedding",
				"error", err,
				"contentLength", contentLen,
				"chunkID", chunkID,
				"messageCount", len(chunk.Messages),
			)
			embeddingFailed = true
		} else if embedding != nil {
			embeddingBlob, _ = json.Marshal(embedding)
			embeddingModel = idx.provider.Model()
		}
	}

	// If embedding provider is available but embedding failed, don't store the chunk
	// Messages will remain unindexed and be retried on next sync
	if embeddingFailed {
		L_debug("transcript: skipping chunk storage due to embedding failure, will retry",
			"chunkID", chunkID,
			"messageCount", len(chunk.Messages),
		)
		return fmt.Errorf("embedding failed, will retry")
	}

	// Insert chunk
	now := time.Now().UnixMilli()
	_, err := idx.db.ExecContext(ctx, `
		INSERT INTO transcript_chunks (id, user_id, session_key, message_ids, timestamp_start, timestamp_end, role, content, embedding, embedding_model, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 'conversation', ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			embedding = excluded.embedding,
			embedding_model = excluded.embedding_model
	`, chunkID, chunk.UserID, chunk.SessionKey, string(messageIDsJSON), chunk.TimestampStart, chunk.TimestampEnd, chunk.Content, embeddingBlob, embeddingModel, now)
	if err != nil {
		return fmt.Errorf("insert chunk: %w", err)
	}

	// Mark source messages as indexed only after successful storage
	for _, msg := range chunk.Messages {
		idx.markAsIndexed(msg.ID)
	}

	L_trace("transcript: chunk indexed",
		"chunkID", chunkID,
		"contentLength", contentLen,
		"messageCount", len(chunk.Messages),
		"hasEmbedding", embeddingBlob != nil,
	)

	return nil
}

// markAsIndexed marks a message as indexed
func (idx *Indexer) markAsIndexed(messageID string) {
	now := time.Now().Unix()
	_, err := idx.db.Exec(`
		UPDATE messages SET transcript_indexed_at = ? WHERE id = ?
	`, now, messageID)
	if err != nil {
		L_warn("transcript: failed to mark message as indexed", "id", messageID, "error", err)
	}
}

// Stats returns current indexer statistics
func (idx *Indexer) Stats() (chunksIndexed int, lastSync time.Time) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.chunksIndexed, idx.lastSync
}

// PendingCount returns the number of unindexed messages
func (idx *Indexer) PendingCount() int {
	var count int
	err := idx.db.QueryRow(`
		SELECT COUNT(*) FROM messages
		WHERE transcript_indexed_at IS NULL
		  AND role IN ('user', 'assistant')
	`).Scan(&count)
	if err != nil {
		L_warn("transcript: failed to count pending", "error", err)
		return 0
	}
	return count
}

// TotalIndexableCount returns the total number of indexable messages
func (idx *Indexer) TotalIndexableCount() int {
	var count int
	err := idx.db.QueryRow(`
		SELECT COUNT(*) FROM messages
		WHERE role IN ('user', 'assistant')
	`).Scan(&count)
	if err != nil {
		L_warn("transcript: failed to count total", "error", err)
		return 0
	}
	return count
}

// ChunksNeedingEmbeddings returns the count of chunks without embeddings
func (idx *Indexer) ChunksNeedingEmbeddings() int {
	var count int
	err := idx.db.QueryRow(`
		SELECT COUNT(*) FROM transcript_chunks
		WHERE embedding IS NULL
	`).Scan(&count)
	if err != nil {
		L_warn("transcript: failed to count chunks needing embeddings", "error", err)
		return 0
	}
	return count
}
