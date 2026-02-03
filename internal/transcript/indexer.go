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

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/memory"
)

const (
	// indexInterval is how often to check for new messages to index
	indexInterval = 30 * time.Second

	// batchSize is the maximum number of messages to process in one batch
	batchSize = 100

	// maxGroupGapSeconds is the maximum time gap between messages to group them
	maxGroupGapSeconds = 300 // 5 minutes
)

// Indexer manages background indexing of session messages
type Indexer struct {
	db       *sql.DB
	provider memory.EmbeddingProvider

	syncing  atomic.Bool
	stopChan chan struct{}
	syncChan chan struct{}
	wg       sync.WaitGroup
	mu       sync.RWMutex

	// Stats
	lastSync      time.Time
	chunksIndexed int
}

// NewIndexer creates a new transcript indexer
func NewIndexer(db *sql.DB, provider memory.EmbeddingProvider) *Indexer {
	return &Indexer{
		db:       db,
		provider: provider,
		stopChan: make(chan struct{}),
		syncChan: make(chan struct{}, 1),
	}
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

	ticker := time.NewTicker(indexInterval)
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

	L_debug("transcript: processing unindexed messages", "count", len(messages))

	// Group messages into conversation chunks
	chunks := idx.groupMessages(messages)

	L_debug("transcript: grouped into chunks", "chunks", len(chunks))

	// Generate embeddings and store chunks
	chunksProcessed := 0
	for _, chunk := range chunks {
		if err := idx.indexChunk(ctx, chunk); err != nil {
			L_warn("transcript: failed to index chunk", "error", err)
			continue
		}
		chunksProcessed++
	}

	// Update stats
	idx.mu.Lock()
	idx.lastSync = time.Now()
	idx.chunksIndexed += chunksProcessed
	idx.mu.Unlock()

	elapsed := time.Since(startTime)
	L_info("transcript: sync completed",
		"messagesProcessed", len(messages),
		"chunksCreated", chunksProcessed,
		"elapsed", elapsed.String(),
	)
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
	`, batchSize)
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

		// Start new chunk if needed
		if currentChunk == nil ||
			currentChunk.SessionKey != msg.SessionKey ||
			currentChunk.UserID != msg.UserID ||
			msg.Timestamp-currentChunk.TimestampEnd > maxGroupGapSeconds {

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
		role := msg.Role
		if role == "user" {
			role = "Human"
		} else if role == "assistant" {
			role = "Assistant"
		}
		cleaned := CleanContent(msg.Content)
		parts = append(parts, fmt.Sprintf("%s: %s", role, cleaned))
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

	// Generate embedding if provider available
	var embeddingBlob []byte
	var embeddingModel string
	if idx.provider != nil && idx.provider.Available() {
		embedding, err := idx.provider.EmbedQuery(ctx, chunk.Content)
		if err != nil {
			L_warn("transcript: failed to generate embedding", "error", err)
			// Continue without embedding
		} else {
			embeddingBlob, _ = json.Marshal(embedding)
			embeddingModel = idx.provider.Model()
		}
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

	// Mark source messages as indexed
	for _, msg := range chunk.Messages {
		idx.markAsIndexed(msg.ID)
	}

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
