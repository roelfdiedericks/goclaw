package transcript

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/config"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/memory"
)

// Manager coordinates transcript indexing and search
type Manager struct {
	db       *sql.DB
	provider memory.EmbeddingProvider
	config   config.TranscriptConfig
	indexer  *Indexer
	searcher *Searcher
}

// NewManager creates a new transcript manager
func NewManager(db *sql.DB, provider memory.EmbeddingProvider, cfg config.TranscriptConfig) (*Manager, error) {
	// Initialize schema
	if err := initSchema(db); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}

	indexer := NewIndexer(db, provider, cfg)
	searcher := NewSearcher(db, provider)

	return &Manager{
		db:       db,
		provider: provider,
		config:   cfg,
		indexer:  indexer,
		searcher: searcher,
	}, nil
}

// SetAgentName sets the agent's display name for transcript labels
func (m *Manager) SetAgentName(name string) {
	m.indexer.SetAgentName(name)
}

// Start begins background indexing
func (m *Manager) Start() {
	L_info("transcript: starting manager")
	m.indexer.Start()
}

// Stop stops the manager gracefully
func (m *Manager) Stop() {
	L_info("transcript: stopping manager")
	m.indexer.Stop()
}

// Search performs semantic and keyword search over transcripts
// userID: current user's ID
// isOwner: if true, can search all users' transcripts
func (m *Manager) Search(ctx context.Context, query string, userID string, isOwner bool, opts SearchOptions) ([]SearchResult, error) {
	return m.searcher.Search(ctx, query, userID, isOwner, opts)
}

// TriggerIndex requests a sync of unindexed messages
func (m *Manager) TriggerIndex() {
	m.indexer.TriggerSync()
}

// Stats returns indexing statistics
func (m *Manager) Stats() TranscriptStats {
	chunksIndexed, lastSync := m.indexer.Stats()
	pending := m.indexer.PendingCount()

	var totalChunks int
	m.db.QueryRow("SELECT COUNT(*) FROM transcript_chunks").Scan(&totalChunks)

	var chunksWithEmbeddings int
	m.db.QueryRow("SELECT COUNT(*) FROM transcript_chunks WHERE embedding IS NOT NULL").Scan(&chunksWithEmbeddings)

	providerName := "none"
	if m.provider != nil && m.provider.Available() {
		providerName = m.provider.Model()
	}

	return TranscriptStats{
		TotalChunks:          totalChunks,
		ChunksWithEmbeddings: chunksWithEmbeddings,
		PendingMessages:      pending,
		ChunksIndexedSession: chunksIndexed,
		LastSync:             lastSync,
		Provider:             providerName,
	}
}

// Recent returns recent transcript entries for a user
func (m *Manager) Recent(ctx context.Context, userID string, isOwner bool, limit int) ([]RecentEntry, error) {
	if limit <= 0 {
		limit = 10
	}

	// Build WHERE clause for user scoping
	whereClause := "WHERE role IN ('user', 'assistant')"
	var args []interface{}

	if !isOwner && userID != "" {
		whereClause += " AND user_id = ?"
		args = append(args, userID)
	}

	// Exclude system messages
	whereClause += " AND content NOT LIKE '%HEARTBEAT%'"
	whereClause += " AND content NOT LIKE '%heartbeat%'"
	whereClause += " AND content NOT LIKE '%Memory checkpoint%'"

	args = append(args, limit)

	query := fmt.Sprintf(`
		SELECT id, session_key, timestamp, role, 
			   CASE WHEN LENGTH(content) > 200 THEN SUBSTR(content, 1, 200) || '...' ELSE content END as preview,
			   user_id
		FROM messages
		%s
		ORDER BY timestamp DESC
		LIMIT ?
	`, whereClause)

	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query recent: %w", err)
	}
	defer rows.Close()

	var entries []RecentEntry
	for rows.Next() {
		var e RecentEntry
		var userIDNull sql.NullString
		var ts int64
		if err := rows.Scan(&e.ID, &e.SessionKey, &ts, &e.Role, &e.Preview, &userIDNull); err != nil {
			continue
		}
		e.Timestamp = time.Unix(ts, 0)
		if userIDNull.Valid {
			e.UserID = userIDNull.String
		}
		entries = append(entries, e)
	}

	return entries, nil
}

// Gaps returns time gaps in conversation history (potential sleep/away periods)
func (m *Manager) Gaps(ctx context.Context, userID string, isOwner bool, minHours float64, limit int) ([]GapEntry, error) {
	if limit <= 0 {
		limit = 10
	}
	if minHours <= 0 {
		minHours = 1.0
	}
	minSeconds := int64(minHours * 3600)

	// Build WHERE clause for user scoping
	whereClause := "WHERE role = 'user'"
	var args []interface{}

	if !isOwner && userID != "" {
		whereClause += " AND user_id = ?"
		args = append(args, userID)
	}

	// Exclude system messages
	whereClause += " AND content NOT LIKE '%HEARTBEAT%'"
	whereClause += " AND content NOT LIKE '%heartbeat%'"
	whereClause += " AND content NOT LIKE '%Memory checkpoint%'"

	args = append(args, minSeconds, limit)

	query := fmt.Sprintf(`
		WITH user_msgs AS (
			SELECT timestamp, content,
				   LEAD(timestamp) OVER (ORDER BY timestamp) as next_timestamp
			FROM messages
			%s
		)
		SELECT timestamp, next_timestamp,
			   (next_timestamp - timestamp) as gap_seconds,
			   CASE WHEN LENGTH(content) > 100 THEN SUBSTR(content, 1, 100) || '...' ELSE content END as last_msg
		FROM user_msgs
		WHERE next_timestamp IS NOT NULL
		  AND (next_timestamp - timestamp) > ?
		ORDER BY timestamp DESC
		LIMIT ?
	`, whereClause)

	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query gaps: %w", err)
	}
	defer rows.Close()

	var entries []GapEntry
	for rows.Next() {
		var e GapEntry
		var fromTs, toTs, gapSecs int64
		if err := rows.Scan(&fromTs, &toTs, &gapSecs, &e.LastMessage); err != nil {
			continue
		}
		e.From = time.Unix(fromTs, 0)
		e.To = time.Unix(toTs, 0)
		e.GapHours = float64(gapSecs) / 3600.0
		entries = append(entries, e)
	}

	return entries, nil
}

// TranscriptStats contains indexing statistics
type TranscriptStats struct {
	TotalChunks          int       `json:"totalChunks"`
	ChunksWithEmbeddings int       `json:"chunksWithEmbeddings"`
	PendingMessages      int       `json:"pendingMessages"`
	ChunksIndexedSession int       `json:"chunksIndexedSession"`
	LastSync             time.Time `json:"lastSync"`
	Provider             string    `json:"provider"`
}

// RecentEntry represents a recent message
type RecentEntry struct {
	ID         string    `json:"id"`
	SessionKey string    `json:"sessionKey"`
	Timestamp  time.Time `json:"timestamp"`
	Role       string    `json:"role"`
	Preview    string    `json:"preview"`
	UserID     string    `json:"userId,omitempty"`
}

// GapEntry represents a time gap in conversation
type GapEntry struct {
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	GapHours    float64   `json:"gapHours"`
	LastMessage string    `json:"lastMessage"`
}
