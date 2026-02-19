package transcript

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/bus"
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
	needingEmbeddings := m.indexer.ChunksNeedingEmbeddings()

	var totalChunks int
	if err := m.db.QueryRow("SELECT COUNT(*) FROM transcript_chunks").Scan(&totalChunks); err != nil {
		L_warn("transcript: failed to count chunks", "error", err)
	}

	var chunksWithEmbeddings int
	if err := m.db.QueryRow("SELECT COUNT(*) FROM transcript_chunks WHERE embedding IS NOT NULL").Scan(&chunksWithEmbeddings); err != nil {
		L_warn("transcript: failed to count embedded chunks", "error", err)
	}

	providerName := "none"
	if m.provider != nil && m.provider.Available() {
		providerName = m.provider.Model()
	}

	return TranscriptStats{
		TotalChunks:             totalChunks,
		ChunksWithEmbeddings:    chunksWithEmbeddings,
		ChunksNeedingEmbeddings: needingEmbeddings,
		PendingMessages:         pending,
		ChunksIndexedSession:    chunksIndexed,
		LastSync:                lastSync,
		Provider:                providerName,
	}
}

// Recent returns recent transcript entries for a user
func (m *Manager) Recent(ctx context.Context, userID string, isOwner bool, limit int, filter *QueryFilter) ([]RecentEntry, error) {
	if limit <= 0 {
		limit = 10
	}
	if filter == nil {
		filter = &QueryFilter{}
	}

	// Build WHERE clause
	conditions := []string{"role IN ('user', 'assistant')"}
	var args []interface{}

	// User scoping
	if !isOwner && userID != "" {
		conditions = append(conditions, "user_id = ?")
		args = append(args, userID)
	}

	// Apply filters
	conditions, args = applyQueryFilters(conditions, args, filter)

	// Legacy content-based filtering (only if not using humanOnly)
	if !filter.HumanOnly {
		conditions = append(conditions, "content NOT LIKE '%HEARTBEAT%'")
		conditions = append(conditions, "content NOT LIKE '%heartbeat%'")
		conditions = append(conditions, "content NOT LIKE '%Memory checkpoint%'")
	}

	whereClause := "WHERE " + strings.Join(conditions, " AND ")
	args = append(args, limit)

	//nolint:gosec // G201: conditions are internal strings, values parameterized
	query := fmt.Sprintf(`
		SELECT id, session_key, timestamp, role, 
			   CASE WHEN LENGTH(content) > 200 THEN SUBSTR(content, 1, 200) || '...' ELSE content END as preview,
			   user_id, source
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
		var userIDNull, sourceNull sql.NullString
		var ts int64
		if err := rows.Scan(&e.ID, &e.SessionKey, &ts, &e.Role, &e.Preview, &userIDNull, &sourceNull); err != nil {
			continue
		}
		e.Timestamp = time.Unix(ts, 0)
		if userIDNull.Valid {
			e.UserID = userIDNull.String
		}
		if sourceNull.Valid {
			e.Source = sourceNull.String
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	return entries, nil
}

// applyQueryFilters adds filter conditions to a query
func applyQueryFilters(conditions []string, args []interface{}, filter *QueryFilter) ([]string, []interface{}) {
	if filter == nil {
		return conditions, args
	}

	// Source filter
	if filter.Source != "" {
		conditions = append(conditions, "source = ?")
		args = append(args, filter.Source)
	}

	// Exclude sources
	if len(filter.ExcludeSources) > 0 {
		placeholders := make([]string, len(filter.ExcludeSources))
		for i, s := range filter.ExcludeSources {
			placeholders[i] = "?"
			args = append(args, s)
		}
		conditions = append(conditions, fmt.Sprintf("(source IS NULL OR source NOT IN (%s))", strings.Join(placeholders, ",")))
	}

	// HumanOnly - exclude cron and heartbeat sources
	if filter.HumanOnly {
		conditions = append(conditions, "(source IS NULL OR source NOT IN ('cron', 'heartbeat'))")
	}

	// Time filters
	if !filter.After.IsZero() {
		conditions = append(conditions, "timestamp > ?")
		args = append(args, filter.After.Unix())
	}
	if !filter.Before.IsZero() {
		conditions = append(conditions, "timestamp < ?")
		args = append(args, filter.Before.Unix())
	}
	if filter.LastDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -filter.LastDays).Unix()
		conditions = append(conditions, "timestamp > ?")
		args = append(args, cutoff)
	}

	// Role filter
	if filter.Role != "" {
		conditions = append(conditions, "role = ?")
		args = append(args, filter.Role)
	}

	return conditions, args
}

// ExactSearch performs substring search on message content
// Returns message-level results (not chunks)
func (m *Manager) ExactSearch(ctx context.Context, query string, userID string, isOwner bool, limit int, filter *QueryFilter) ([]RecentEntry, error) {
	if limit <= 0 {
		limit = 10
	}
	if filter == nil {
		filter = &QueryFilter{}
	}

	// Build WHERE clause
	conditions := []string{
		"role IN ('user', 'assistant')",
		"content LIKE ?", // Substring match
	}
	args := []interface{}{
		"%" + query + "%",
	}

	// User scoping
	if !isOwner && userID != "" {
		conditions = append(conditions, "user_id = ?")
		args = append(args, userID)
	}

	// Apply filters
	conditions, args = applyQueryFilters(conditions, args, filter)

	whereClause := "WHERE " + strings.Join(conditions, " AND ")
	args = append(args, limit)

	//nolint:gosec // G201: conditions are internal strings, values parameterized
	sqlQuery := fmt.Sprintf(`
		SELECT id, session_key, timestamp, role,
		       CASE WHEN LENGTH(content) > 200 THEN SUBSTR(content, 1, 200) || '...' ELSE content END as preview,
		       user_id, source
		FROM messages
		%s
		ORDER BY timestamp DESC
		LIMIT ?
	`, whereClause)

	rows, err := m.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("exact search: %w", err)
	}
	defer rows.Close()

	var entries []RecentEntry
	for rows.Next() {
		var e RecentEntry
		var userIDNull, sourceNull sql.NullString
		var ts int64
		if err := rows.Scan(&e.ID, &e.SessionKey, &ts, &e.Role, &e.Preview, &userIDNull, &sourceNull); err != nil {
			continue
		}
		e.Timestamp = time.Unix(ts, 0)
		if userIDNull.Valid {
			e.UserID = userIDNull.String
		}
		if sourceNull.Valid {
			e.Source = sourceNull.String
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	L_debug("transcript: exact search",
		"query", query,
		"results", len(entries),
	)

	return entries, nil
}

// Gaps returns time gaps in conversation history (potential sleep/away periods)
func (m *Manager) Gaps(ctx context.Context, userID string, isOwner bool, minHours float64, limit int, filter *QueryFilter) ([]GapEntry, error) {
	if limit <= 0 {
		limit = 10
	}
	if minHours <= 0 {
		minHours = 1.0
	}
	if filter == nil {
		filter = &QueryFilter{}
	}
	minSeconds := int64(minHours * 3600)

	// Build WHERE clause - gaps are based on user messages
	conditions := []string{"role = 'user'"}
	var args []interface{}

	// User scoping
	if !isOwner && userID != "" {
		conditions = append(conditions, "user_id = ?")
		args = append(args, userID)
	}

	// Apply filters (especially humanOnly for excluding cron/heartbeat)
	conditions, args = applyQueryFilters(conditions, args, filter)

	// Legacy content-based filtering (only if not using humanOnly)
	if !filter.HumanOnly {
		conditions = append(conditions, "content NOT LIKE '%HEARTBEAT%'")
		conditions = append(conditions, "content NOT LIKE '%heartbeat%'")
		conditions = append(conditions, "content NOT LIKE '%Memory checkpoint%'")
	}

	whereClause := "WHERE " + strings.Join(conditions, " AND ")
	args = append(args, minSeconds, limit)

	//nolint:gosec // G201: conditions are internal strings, values parameterized
	query := fmt.Sprintf(`
		WITH user_msgs AS (
			SELECT timestamp, content, source,
				   LEAD(timestamp) OVER (ORDER BY timestamp) as next_timestamp
			FROM messages
			%s
		)
		SELECT timestamp, next_timestamp,
			   (next_timestamp - timestamp) as gap_seconds,
			   CASE WHEN LENGTH(content) > 100 THEN SUBSTR(content, 1, 100) || '...' ELSE content END as last_msg,
			   source
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
		var sourceNull sql.NullString
		if err := rows.Scan(&fromTs, &toTs, &gapSecs, &e.LastMessage, &sourceNull); err != nil {
			continue
		}
		e.From = time.Unix(fromTs, 0)
		e.To = time.Unix(toTs, 0)
		e.GapHours = float64(gapSecs) / 3600.0
		if sourceNull.Valid {
			e.Source = sourceNull.String
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	return entries, nil
}

// TranscriptStats contains indexing statistics
type TranscriptStats struct {
	TotalChunks             int       `json:"totalChunks"`
	ChunksWithEmbeddings    int       `json:"chunksWithEmbeddings"`
	ChunksNeedingEmbeddings int       `json:"chunksNeedingEmbeddings"`
	PendingMessages         int       `json:"pendingMessages"`
	ChunksIndexedSession    int       `json:"chunksIndexedSession"`
	LastSync                time.Time `json:"lastSync"`
	Provider                string    `json:"provider"`
}

// RecentEntry represents a recent message
type RecentEntry struct {
	ID         string    `json:"id"`
	SessionKey string    `json:"sessionKey"`
	Timestamp  time.Time `json:"timestamp"`
	Role       string    `json:"role"`
	Preview    string    `json:"preview"`
	UserID     string    `json:"userId,omitempty"`
	Source     string    `json:"source,omitempty"`
}

// GapEntry represents a time gap in conversation
type GapEntry struct {
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	GapHours    float64   `json:"gapHours"`
	LastMessage string    `json:"lastMessage"`
	Source      string    `json:"source,omitempty"`
}

// QueryFilter contains common filter options for transcript queries
type QueryFilter struct {
	// Source filters
	Source         string   // Include only this source (e.g., "telegram")
	ExcludeSources []string // Exclude these sources
	HumanOnly      bool     // Exclude cron and heartbeat sources

	// Time filters
	After    time.Time // Messages after this time
	Before   time.Time // Messages before this time
	LastDays int       // Messages from last N days (alternative to After)

	// Role filter
	Role string // Filter by role ("user" or "assistant")
}

// RegisterCommands registers transcript command handlers with the bus
func (m *Manager) RegisterCommands() {
	bus.RegisterCommand("transcript", "test", m.handleTest)
	bus.RegisterCommand("transcript", "apply", m.handleApply)
	bus.RegisterCommand("transcript", "stats", m.handleStats)
	bus.RegisterCommand("transcript", "reindex", m.handleReindex)
	L_info("transcript: commands registered")
}

// UnregisterCommands removes transcript command handlers
func (m *Manager) UnregisterCommands() {
	bus.UnregisterComponent("transcript")
}

// handleTest verifies database and embedding provider connectivity
func (m *Manager) handleTest(cmd bus.Command) bus.CommandResult {
	L_debug("transcript: testing connection")

	// Test database
	if err := m.db.Ping(); err != nil {
		return bus.CommandResult{
			Error:   err,
			Message: fmt.Sprintf("database connection failed: %v", err),
		}
	}

	// Test embedding provider
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := m.provider.EmbedQuery(ctx, "test")
	if err != nil {
		return bus.CommandResult{
			Error:   err,
			Message: fmt.Sprintf("embedding provider failed: %v", err),
		}
	}

	return bus.CommandResult{
		Success: true,
		Message: "Database and embedding provider OK",
	}
}

// handleApply applies new config to the running manager
func (m *Manager) handleApply(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(config.TranscriptConfig)
	if !ok {
		return bus.CommandResult{
			Error:   fmt.Errorf("expected config.TranscriptConfig payload, got %T", cmd.Payload),
			Message: "invalid payload type",
		}
	}

	// Validate
	if err := ValidateConfig(cfg); err != nil {
		return bus.CommandResult{
			Error:   err,
			Message: fmt.Sprintf("config validation failed: %v", err),
		}
	}

	// Apply config
	m.config = cfg
	m.indexer.UpdateConfig(cfg)

	L_info("transcript: config applied",
		"enabled", cfg.Enabled,
		"indexInterval", cfg.IndexIntervalSeconds,
	)

	return bus.CommandResult{
		Success: true,
		Message: "Config applied successfully",
	}
}

// handleStats returns current indexing statistics
func (m *Manager) handleStats(cmd bus.Command) bus.CommandResult {
	stats := m.Stats()
	return bus.CommandResult{
		Success: true,
		Message: fmt.Sprintf("Chunks: %d indexed (%d session), %d pending | Last sync: %s",
			stats.TotalChunks, stats.ChunksIndexedSession, stats.PendingMessages,
			stats.LastSync.Format("15:04:05")),
		Data: stats,
	}
}

// handleReindex triggers a full reindex
func (m *Manager) handleReindex(cmd bus.Command) bus.CommandResult {
	m.TriggerIndex()
	return bus.CommandResult{
		Success: true,
		Message: "Reindex triggered",
	}
}
