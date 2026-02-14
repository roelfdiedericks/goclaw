package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// SQLiteStore implements Store using SQLite
type SQLiteStore struct {
	db     *sql.DB
	config StoreConfig
}

// Schema version for migrations
const currentSchemaVersion = 6

// NewSQLiteStore creates a new SQLite store
func NewSQLiteStore(cfg StoreConfig) (*SQLiteStore, error) {
	// Ensure directory exists
	dir := filepath.Dir(cfg.Path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	// Open database
	db, err := sql.Open("sqlite3", cfg.Path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Enable WAL mode explicitly (belt and suspenders)
	if cfg.WALMode {
		if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
			L_warn("sqlite: failed to enable WAL mode", "error", err)
		}
	}

	// Set busy timeout
	timeout := cfg.BusyTimeout
	if timeout == 0 {
		timeout = 5000
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA busy_timeout=%d", timeout)); err != nil {
		L_warn("sqlite: failed to set busy_timeout", "error", err)
	}

	store := &SQLiteStore{db: db, config: cfg}

	// Run migrations
	if err := store.Migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	L_info("sqlite: store opened", "path", cfg.Path)
	return store, nil
}

// Migrate runs database migrations
func (s *SQLiteStore) Migrate() error {
	// Check current schema version
	var version int
	err := s.db.QueryRow("SELECT version FROM schema_version ORDER BY version DESC LIMIT 1").Scan(&version)
	if err != nil {
		// Table doesn't exist, start from scratch
		version = 0
	}

	if version >= currentSchemaVersion {
		L_debug("sqlite: schema up to date", "version", version)
		return nil
	}

	L_info("sqlite: migrating schema", "from", version, "to", currentSchemaVersion)

	// Run migrations in order
	migrations := []func(*sql.DB) error{
		migrateV1,
		migrateV2,
		migrateV3,
		migrateV4,
		migrateV5,
		migrateV6,
	}

	for i := version; i < len(migrations); i++ {
		if err := migrations[i](s.db); err != nil {
			return fmt.Errorf("migration v%d failed: %w", i+1, err)
		}
		L_debug("sqlite: applied migration", "version", i+1)
	}

	return nil
}

// migrateV1 creates the initial schema
func migrateV1(db *sql.DB) error {
	schema := `
	-- Schema version tracking
	CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER PRIMARY KEY,
		applied_at INTEGER NOT NULL
	);
	INSERT INTO schema_version (version, applied_at) VALUES (1, ?);

	-- Sessions table
	CREATE TABLE IF NOT EXISTS sessions (
		key TEXT PRIMARY KEY,
		id TEXT NOT NULL UNIQUE,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		
		-- Configuration
		model TEXT,
		thinking_level TEXT,
		max_tokens INTEGER DEFAULT 200000,
		
		-- State
		compaction_count INTEGER DEFAULT 0,
		total_tokens INTEGER DEFAULT 0,
		
		-- Flush state (JSON array for thresholds)
		flushed_thresholds TEXT DEFAULT '[]',
		flush_actioned INTEGER DEFAULT 0
	);
	CREATE INDEX IF NOT EXISTS idx_sessions_updated ON sessions(updated_at);

	-- Messages table
	CREATE TABLE IF NOT EXISTS messages (
		id TEXT PRIMARY KEY,
		session_key TEXT NOT NULL,
		parent_id TEXT,
		timestamp INTEGER NOT NULL,
		
		-- Core message data
		role TEXT NOT NULL,
		content TEXT NOT NULL DEFAULT '',
		
		-- Tool interaction fields (nullable)
		tool_call_id TEXT,
		tool_name TEXT,
		tool_input TEXT,
		tool_result TEXT,
		tool_is_error INTEGER DEFAULT 0,
		
		-- Source metadata
		source TEXT,
		channel_id TEXT,
		user_id TEXT,
		
		-- Token tracking
		input_tokens INTEGER DEFAULT 0,
		output_tokens INTEGER DEFAULT 0,
		
		-- Raw JSON for full fidelity (optional)
		raw_json TEXT,
		
		FOREIGN KEY (session_key) REFERENCES sessions(key) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_key, timestamp);
	CREATE INDEX IF NOT EXISTS idx_messages_session_role ON messages(session_key, role);
	CREATE INDEX IF NOT EXISTS idx_messages_parent ON messages(parent_id);
	CREATE INDEX IF NOT EXISTS idx_messages_tool ON messages(tool_call_id);

	-- Checkpoints table
	CREATE TABLE IF NOT EXISTS checkpoints (
		id TEXT PRIMARY KEY,
		session_key TEXT NOT NULL,
		parent_id TEXT,
		timestamp INTEGER NOT NULL,
		
		-- Summary data
		summary TEXT NOT NULL,
		tokens_at_checkpoint INTEGER NOT NULL,
		message_count_at_checkpoint INTEGER NOT NULL,
		
		-- Structured data (JSON arrays)
		topics TEXT DEFAULT '[]',
		key_decisions TEXT DEFAULT '[]',
		open_questions TEXT DEFAULT '[]',
		
		-- Generation metadata
		generated_by TEXT,
		covers_up_to TEXT,
		
		FOREIGN KEY (session_key) REFERENCES sessions(key) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_checkpoints_session ON checkpoints(session_key, timestamp);

	-- Compactions table
	CREATE TABLE IF NOT EXISTS compactions (
		id TEXT PRIMARY KEY,
		session_key TEXT NOT NULL,
		parent_id TEXT,
		timestamp INTEGER NOT NULL,
		
		-- Compaction data
		summary TEXT NOT NULL,
		first_kept_entry_id TEXT,
		tokens_before INTEGER NOT NULL,
		tokens_after INTEGER DEFAULT 0,
		messages_removed INTEGER DEFAULT 0,
		from_checkpoint INTEGER DEFAULT 0,
		checkpoint_id TEXT,
		
		FOREIGN KEY (session_key) REFERENCES sessions(key) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_compactions_session ON compactions(session_key, timestamp);
	`

	_, err := db.Exec(schema, time.Now().Unix())
	return err
}

// migrateV2 adds needs_summary_retry column for emergency truncation recovery
func migrateV2(db *sql.DB) error {
	schema := `
	-- Add needs_summary_retry column to compactions for emergency truncation recovery
	ALTER TABLE compactions ADD COLUMN needs_summary_retry INTEGER DEFAULT 0;
	
	-- Index for efficient lookup of pending retries
	CREATE INDEX IF NOT EXISTS idx_compactions_pending_retry ON compactions(needs_summary_retry) WHERE needs_summary_retry = 1;
	
	-- Update schema version
	INSERT INTO schema_version (version, applied_at) VALUES (2, ?);
	`

	_, err := db.Exec(schema, time.Now().Unix())
	return err
}

// migrateV3 adds transcript_indexed_at column for transcript search indexing
func migrateV3(db *sql.DB) error {
	schema := `
	-- Add transcript_indexed_at column to messages for tracking which messages have been indexed
	-- NULL = not indexed, timestamp = when indexed
	ALTER TABLE messages ADD COLUMN transcript_indexed_at INTEGER DEFAULT NULL;
	
	-- Index for efficient lookup of unindexed messages
	CREATE INDEX IF NOT EXISTS idx_messages_unindexed ON messages(transcript_indexed_at) WHERE transcript_indexed_at IS NULL;
	
	-- Update schema version
	INSERT INTO schema_version (version, applied_at) VALUES (3, ?);
	`

	_, err := db.Exec(schema, time.Now().Unix())
	return err
}

// migrateV4 adds thinking column for reasoning/thinking content (Kimi, Deepseek, etc.)
func migrateV4(db *sql.DB) error {
	schema := `
	-- Add thinking column to messages for reasoning/thinking content
	ALTER TABLE messages ADD COLUMN thinking TEXT DEFAULT NULL;
	
	-- Update schema version
	INSERT INTO schema_version (version, applied_at) VALUES (4, ?);
	`

	_, err := db.Exec(schema, time.Now().Unix())
	return err
}

// migrateV5 adds supervision fields for tracking guidance and ghostwriting interventions
func migrateV5(db *sql.DB) error {
	schema := `
	-- Add supervisor column to track who performed supervision intervention
	ALTER TABLE messages ADD COLUMN supervisor TEXT DEFAULT NULL;
	
	-- Add intervention_type column: "guidance" or "ghostwrite"
	ALTER TABLE messages ADD COLUMN intervention_type TEXT DEFAULT NULL;
	
	-- Index for querying supervision interventions
	CREATE INDEX IF NOT EXISTS idx_messages_supervision ON messages(supervisor) WHERE supervisor IS NOT NULL;
	
	-- Update schema version
	INSERT INTO schema_version (version, applied_at) VALUES (5, ?);
	`

	_, err := db.Exec(schema, time.Now().Unix())
	return err
}

// migrateV6 adds provider_state table for stateful provider session-scoped state
// Used by providers like xAI to persist response_id for context chaining
func migrateV6(db *sql.DB) error {
	schema := `
	-- Provider state table for session-scoped provider state
	-- Key format: provider_key = "providerName:model" (e.g., "xai:grok-4-1-fast-reasoning")
	CREATE TABLE IF NOT EXISTS provider_state (
		session_key TEXT NOT NULL,
		provider_key TEXT NOT NULL,
		state_json TEXT NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (session_key, provider_key),
		FOREIGN KEY (session_key) REFERENCES sessions(key) ON DELETE CASCADE
	);
	
	-- Index for efficient session-scoped queries (e.g., delete all state for a session)
	CREATE INDEX IF NOT EXISTS idx_provider_state_session ON provider_state(session_key);
	
	-- Update schema version
	INSERT INTO schema_version (version, applied_at) VALUES (6, ?);
	`

	_, err := db.Exec(schema, time.Now().Unix())
	return err
}

// Close closes the database connection
func (s *SQLiteStore) Close() error {
	L_debug("sqlite: closing store")
	return s.db.Close()
}

// DB returns the underlying database connection for external use
// (e.g., transcript indexing needs direct DB access)
func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}

// GetSession retrieves a session by key
func (s *SQLiteStore) GetSession(ctx context.Context, key string) (*StoredSession, error) {
	var sess StoredSession
	var flushedJSON string
	var createdAt, updatedAt int64

	err := s.db.QueryRowContext(ctx, `
		SELECT key, id, created_at, updated_at, model, thinking_level,
		       compaction_count, total_tokens, max_tokens,
		       flushed_thresholds, flush_actioned
		FROM sessions WHERE key = ?
	`, key).Scan(
		&sess.Key, &sess.ID, &createdAt, &updatedAt,
		&sess.Model, &sess.ThinkingLevel,
		&sess.CompactionCount, &sess.TotalTokens, &sess.MaxTokens,
		&flushedJSON, &sess.FlushActioned,
	)

	if err == sql.ErrNoRows {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}

	sess.CreatedAt = time.Unix(createdAt, 0)
	sess.UpdatedAt = time.Unix(updatedAt, 0)
	if err := json.Unmarshal([]byte(flushedJSON), &sess.FlushedThresholds); err != nil {
		L_warn("sqlite: failed to unmarshal flushed thresholds", "session", sess.Key, "error", err)
	}

	return &sess, nil
}

// CreateSession creates a new session
func (s *SQLiteStore) CreateSession(ctx context.Context, sess *StoredSession) error {
	flushedJSON, _ := json.Marshal(sess.FlushedThresholds)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (key, id, created_at, updated_at, model, thinking_level,
		                      compaction_count, total_tokens, max_tokens,
		                      flushed_thresholds, flush_actioned)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		sess.Key, sess.ID, sess.CreatedAt.Unix(), sess.UpdatedAt.Unix(),
		sess.Model, sess.ThinkingLevel,
		sess.CompactionCount, sess.TotalTokens, sess.MaxTokens,
		string(flushedJSON), sess.FlushActioned,
	)

	if err != nil {
		return fmt.Errorf("insert failed: %w", err)
	}

	L_debug("sqlite: session created", "key", sess.Key, "id", sess.ID)
	return nil
}

// UpdateSession updates an existing session
func (s *SQLiteStore) UpdateSession(ctx context.Context, sess *StoredSession) error {
	flushedJSON, _ := json.Marshal(sess.FlushedThresholds)

	result, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET
			updated_at = ?, model = ?, thinking_level = ?,
			compaction_count = ?, total_tokens = ?, max_tokens = ?,
			flushed_thresholds = ?, flush_actioned = ?
		WHERE key = ?
	`,
		time.Now().Unix(), sess.Model, sess.ThinkingLevel,
		sess.CompactionCount, sess.TotalTokens, sess.MaxTokens,
		string(flushedJSON), sess.FlushActioned,
		sess.Key,
	)

	if err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrSessionNotFound
	}

	return nil
}

// ListSessions returns all sessions
func (s *SQLiteStore) ListSessions(ctx context.Context) ([]StoredSessionInfo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.key, s.id, s.created_at, s.updated_at, s.compaction_count, s.total_tokens,
		       (SELECT COUNT(*) FROM messages WHERE session_key = s.key) as msg_count
		FROM sessions s
		ORDER BY s.updated_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []StoredSessionInfo
	for rows.Next() {
		var si StoredSessionInfo
		var createdAt, updatedAt int64
		if err := rows.Scan(&si.Key, &si.ID, &createdAt, &updatedAt, &si.CompactionCount, &si.TotalTokens, &si.MessageCount); err != nil {
			return nil, err
		}
		si.CreatedAt = time.Unix(createdAt, 0)
		si.UpdatedAt = time.Unix(updatedAt, 0)
		sessions = append(sessions, si)
	}

	return sessions, rows.Err()
}

// AppendMessage appends a message to a session
func (s *SQLiteStore) AppendMessage(ctx context.Context, sessionKey string, msg *StoredMessage) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (id, session_key, parent_id, timestamp,
		                      role, content, tool_call_id, tool_name, tool_input,
		                      tool_result, tool_is_error, source, channel_id, user_id,
		                      input_tokens, output_tokens, raw_json, thinking,
		                      supervisor, intervention_type)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		msg.ID, sessionKey, nullString(msg.ParentID), msg.Timestamp.Unix(),
		msg.Role, msg.Content, nullString(msg.ToolCallID), nullString(msg.ToolName), msg.ToolInput,
		nullString(msg.ToolResult), msg.ToolIsError, nullString(msg.Source), nullString(msg.ChannelID), nullString(msg.UserID),
		msg.InputTokens, msg.OutputTokens, msg.RawJSON, nullString(msg.Thinking),
		nullString(msg.Supervisor), nullString(msg.InterventionType),
	)

	if err != nil {
		return fmt.Errorf("insert message failed: %w", err)
	}

	// Update session timestamp
	if _, err := s.db.ExecContext(ctx, "UPDATE sessions SET updated_at = ? WHERE key = ?", time.Now().Unix(), sessionKey); err != nil {
		L_warn("sqlite: failed to update session timestamp", "session", sessionKey, "error", err)
	}

	L_trace("sqlite: message appended", "session", sessionKey, "id", msg.ID, "role", msg.Role)
	return nil
}

// GetMessages retrieves messages for a session
func (s *SQLiteStore) GetMessages(ctx context.Context, sessionKey string, opts MessageQueryOpts) ([]StoredMessage, error) {
	query := `
		SELECT id, session_key, parent_id, timestamp, role, content,
		       tool_call_id, tool_name, tool_input, tool_result, tool_is_error,
		       source, channel_id, user_id, input_tokens, output_tokens, raw_json, thinking,
		       supervisor, intervention_type
		FROM messages
		WHERE session_key = ?
	`
	args := []interface{}{sessionKey}

	if opts.AfterID != "" {
		query += " AND timestamp > (SELECT timestamp FROM messages WHERE id = ?)"
		args = append(args, opts.AfterID)
	}

	if !opts.AfterTime.IsZero() {
		query += " AND timestamp > ?"
		args = append(args, opts.AfterTime.Unix())
	}

	if len(opts.RolesOnly) > 0 {
		query += " AND role IN (?" + repeatString(",?", len(opts.RolesOnly)-1) + ")"
		for _, r := range opts.RolesOnly {
			args = append(args, r)
		}
	}

	query += " ORDER BY timestamp ASC"

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []StoredMessage
	for rows.Next() {
		var msg StoredMessage
		var ts int64
		var parentID, toolCallID, toolName, toolResult, source, channelID, userID, thinking sql.NullString
		var supervisor, interventionType sql.NullString
		var toolInput, rawJSON []byte

		if err := rows.Scan(
			&msg.ID, &msg.SessionKey, &parentID, &ts, &msg.Role, &msg.Content,
			&toolCallID, &toolName, &toolInput, &toolResult, &msg.ToolIsError,
			&source, &channelID, &userID, &msg.InputTokens, &msg.OutputTokens, &rawJSON, &thinking,
			&supervisor, &interventionType,
		); err != nil {
			return nil, err
		}

		msg.Timestamp = time.Unix(ts, 0)
		msg.ParentID = parentID.String
		msg.ToolCallID = toolCallID.String
		msg.ToolName = toolName.String
		msg.ToolInput = toolInput
		msg.ToolResult = toolResult.String
		msg.Source = source.String
		msg.ChannelID = channelID.String
		msg.UserID = userID.String
		msg.Thinking = thinking.String
		msg.Supervisor = supervisor.String
		msg.InterventionType = interventionType.String
		if opts.IncludeRaw {
			msg.RawJSON = rawJSON
		}

		messages = append(messages, msg)
	}

	return messages, rows.Err()
}

// GetMessageCount returns the number of messages in a session
func (s *SQLiteStore) GetMessageCount(ctx context.Context, sessionKey string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages WHERE session_key = ?", sessionKey).Scan(&count)
	return count, err
}

// AppendCheckpoint appends a checkpoint to a session
func (s *SQLiteStore) AppendCheckpoint(ctx context.Context, sessionKey string, cp *StoredCheckpoint) error {
	topicsJSON, _ := json.Marshal(cp.Topics)
	decisionsJSON, _ := json.Marshal(cp.KeyDecisions)
	questionsJSON, _ := json.Marshal(cp.OpenQuestions)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO checkpoints (id, session_key, parent_id, timestamp,
		                         summary, tokens_at_checkpoint, message_count_at_checkpoint,
		                         topics, key_decisions, open_questions,
		                         generated_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		cp.ID, sessionKey, nullString(cp.ParentID), cp.Timestamp.Unix(),
		cp.Summary, cp.TokensAtCheckpoint, cp.MessageCountAtCheckpoint,
		string(topicsJSON), string(decisionsJSON), string(questionsJSON),
		nullString(cp.GeneratedBy),
	)

	if err != nil {
		return fmt.Errorf("insert checkpoint failed: %w", err)
	}

	L_debug("sqlite: checkpoint appended", "session", sessionKey, "id", cp.ID, "tokens", cp.TokensAtCheckpoint)
	return nil
}

// GetLatestCheckpoint returns the most recent checkpoint
func (s *SQLiteStore) GetLatestCheckpoint(ctx context.Context, sessionKey string) (*StoredCheckpoint, error) {
	var cp StoredCheckpoint
	var ts int64
	var parentID, generatedBy sql.NullString
	var topicsJSON, decisionsJSON, questionsJSON string

	err := s.db.QueryRowContext(ctx, `
		SELECT id, session_key, parent_id, timestamp,
		       summary, tokens_at_checkpoint, message_count_at_checkpoint,
		       topics, key_decisions, open_questions,
		       generated_by
		FROM checkpoints
		WHERE session_key = ?
		ORDER BY timestamp DESC
		LIMIT 1
	`, sessionKey).Scan(
		&cp.ID, &cp.SessionKey, &parentID, &ts,
		&cp.Summary, &cp.TokensAtCheckpoint, &cp.MessageCountAtCheckpoint,
		&topicsJSON, &decisionsJSON, &questionsJSON,
		&generatedBy,
	)

	if err == sql.ErrNoRows {
		return nil, nil // No checkpoint found
	}
	if err != nil {
		return nil, err
	}

	cp.Timestamp = time.Unix(ts, 0)
	cp.ParentID = parentID.String
	cp.GeneratedBy = generatedBy.String
	if err := json.Unmarshal([]byte(topicsJSON), &cp.Topics); err != nil {
		L_warn("sqlite: failed to unmarshal checkpoint topics", "checkpoint", cp.ID, "error", err)
	}
	if err := json.Unmarshal([]byte(decisionsJSON), &cp.KeyDecisions); err != nil {
		L_warn("sqlite: failed to unmarshal checkpoint decisions", "checkpoint", cp.ID, "error", err)
	}
	if err := json.Unmarshal([]byte(questionsJSON), &cp.OpenQuestions); err != nil {
		L_warn("sqlite: failed to unmarshal checkpoint questions", "checkpoint", cp.ID, "error", err)
	}

	return &cp, nil
}

// GetCheckpoints returns all checkpoints for a session
func (s *SQLiteStore) GetCheckpoints(ctx context.Context, sessionKey string) ([]StoredCheckpoint, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_key, parent_id, timestamp,
		       summary, tokens_at_checkpoint, message_count_at_checkpoint,
		       topics, key_decisions, open_questions,
		       generated_by
		FROM checkpoints
		WHERE session_key = ?
		ORDER BY timestamp ASC
	`, sessionKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var checkpoints []StoredCheckpoint
	for rows.Next() {
		var cp StoredCheckpoint
		var ts int64
		var parentID, generatedBy sql.NullString
		var topicsJSON, decisionsJSON, questionsJSON string

		if err := rows.Scan(
			&cp.ID, &cp.SessionKey, &parentID, &ts,
			&cp.Summary, &cp.TokensAtCheckpoint, &cp.MessageCountAtCheckpoint,
			&topicsJSON, &decisionsJSON, &questionsJSON,
			&generatedBy,
		); err != nil {
			return nil, err
		}

		cp.Timestamp = time.Unix(ts, 0)
		cp.ParentID = parentID.String
		cp.GeneratedBy = generatedBy.String
		if err := json.Unmarshal([]byte(topicsJSON), &cp.Topics); err != nil {
			L_warn("sqlite: failed to unmarshal checkpoint topics", "checkpoint", cp.ID, "error", err)
		}
		if err := json.Unmarshal([]byte(decisionsJSON), &cp.KeyDecisions); err != nil {
			L_warn("sqlite: failed to unmarshal checkpoint decisions", "checkpoint", cp.ID, "error", err)
		}
		if err := json.Unmarshal([]byte(questionsJSON), &cp.OpenQuestions); err != nil {
			L_warn("sqlite: failed to unmarshal checkpoint questions", "checkpoint", cp.ID, "error", err)
		}

		checkpoints = append(checkpoints, cp)
	}

	return checkpoints, rows.Err()
}

// AppendCompaction appends a compaction record
func (s *SQLiteStore) AppendCompaction(ctx context.Context, sessionKey string, comp *StoredCompaction) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO compactions (id, session_key, parent_id, timestamp,
		                         summary, first_kept_entry_id, tokens_before, tokens_after,
		                         messages_removed, from_checkpoint, checkpoint_id, needs_summary_retry)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		comp.ID, sessionKey, nullString(comp.ParentID), comp.Timestamp.Unix(),
		comp.Summary, nullString(comp.FirstKeptEntryID), comp.TokensBefore, comp.TokensAfter,
		comp.MessagesRemoved, comp.FromCheckpoint, nullString(comp.CheckpointID), comp.NeedsSummaryRetry,
	)

	if err != nil {
		return fmt.Errorf("insert compaction failed: %w", err)
	}

	// Update session compaction count
	if _, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET compaction_count = compaction_count + 1, updated_at = ?
		WHERE key = ?
	`, time.Now().Unix(), sessionKey); err != nil {
		L_warn("sqlite: failed to update compaction count", "session", sessionKey, "error", err)
	}

	L_info("sqlite: compaction appended", "session", sessionKey, "id", comp.ID,
		"tokensBefore", comp.TokensBefore, "needsRetry", comp.NeedsSummaryRetry)
	return nil
}

// GetCompactions returns all compactions for a session
func (s *SQLiteStore) GetCompactions(ctx context.Context, sessionKey string) ([]StoredCompaction, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_key, parent_id, timestamp,
		       summary, first_kept_entry_id, tokens_before, tokens_after,
		       messages_removed, from_checkpoint, checkpoint_id
		FROM compactions
		WHERE session_key = ?
		ORDER BY timestamp ASC
	`, sessionKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var compactions []StoredCompaction
	for rows.Next() {
		var comp StoredCompaction
		var ts int64
		var parentID, firstKeptID, checkpointID sql.NullString

		if err := rows.Scan(
			&comp.ID, &comp.SessionKey, &parentID, &ts,
			&comp.Summary, &firstKeptID, &comp.TokensBefore, &comp.TokensAfter,
			&comp.MessagesRemoved, &comp.FromCheckpoint, &checkpointID,
		); err != nil {
			return nil, err
		}

		comp.Timestamp = time.Unix(ts, 0)
		comp.ParentID = parentID.String
		comp.FirstKeptEntryID = firstKeptID.String
		comp.CheckpointID = checkpointID.String

		compactions = append(compactions, comp)
	}

	return compactions, rows.Err()
}

// GetPendingSummaryRetry returns a compaction that needs summary retry (for background processing)
// Returns nil if no pending retries found
func (s *SQLiteStore) GetPendingSummaryRetry(ctx context.Context) (*StoredCompaction, error) {
	var comp StoredCompaction
	var ts int64
	var parentID, firstKeptID, checkpointID sql.NullString

	err := s.db.QueryRowContext(ctx, `
		SELECT id, session_key, parent_id, timestamp,
		       summary, first_kept_entry_id, tokens_before, tokens_after,
		       messages_removed, from_checkpoint, checkpoint_id, needs_summary_retry
		FROM compactions
		WHERE needs_summary_retry = 1
		ORDER BY timestamp ASC
		LIMIT 1
	`).Scan(
		&comp.ID, &comp.SessionKey, &parentID, &ts,
		&comp.Summary, &firstKeptID, &comp.TokensBefore, &comp.TokensAfter,
		&comp.MessagesRemoved, &comp.FromCheckpoint, &checkpointID, &comp.NeedsSummaryRetry,
	)

	if err == sql.ErrNoRows {
		return nil, nil // No pending retries
	}
	if err != nil {
		return nil, err
	}

	comp.Timestamp = time.Unix(ts, 0)
	comp.ParentID = parentID.String
	comp.FirstKeptEntryID = firstKeptID.String
	comp.CheckpointID = checkpointID.String

	return &comp, nil
}

// UpdateCompactionSummary updates a compaction's summary and clears the retry flag
func (s *SQLiteStore) UpdateCompactionSummary(ctx context.Context, compactionID string, summary string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE compactions
		SET summary = ?, needs_summary_retry = 0
		WHERE id = ?
	`, summary, compactionID)

	if err != nil {
		return fmt.Errorf("update compaction summary failed: %w", err)
	}

	rows, _ := result.RowsAffected()
	L_info("sqlite: compaction summary updated", "id", compactionID, "rowsAffected", rows)
	return nil
}

// GetMessagesInRange returns messages between two compaction points for summary regeneration
// startAfterID: ID of first_kept_entry from previous compaction (or empty for all messages)
// endBeforeID: ID of first_kept_entry from current compaction (messages to summarize)
func (s *SQLiteStore) GetMessagesInRange(ctx context.Context, sessionKey string, startAfterID, endBeforeID string) ([]StoredMessage, error) {
	// Build query based on whether we have a start boundary
	var query string
	var args []interface{}

	if startAfterID == "" {
		// Get all messages before endBeforeID
		query = `
			SELECT id, session_key, parent_id, timestamp,
			       role, content, tool_call_id, tool_name, tool_input,
			       tool_result, tool_is_error, source, channel_id, user_id,
			       input_tokens, output_tokens, thinking
			FROM messages
			WHERE session_key = ? AND id < ?
			ORDER BY timestamp ASC
		`
		args = []interface{}{sessionKey, endBeforeID}
	} else {
		// Get messages between start and end
		query = `
			SELECT id, session_key, parent_id, timestamp,
			       role, content, tool_call_id, tool_name, tool_input,
			       tool_result, tool_is_error, source, channel_id, user_id,
			       input_tokens, output_tokens, thinking
			FROM messages
			WHERE session_key = ? AND id > ? AND id < ?
			ORDER BY timestamp ASC
		`
		args = []interface{}{sessionKey, startAfterID, endBeforeID}
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []StoredMessage
	for rows.Next() {
		var msg StoredMessage
		var ts int64
		var parentID, toolCallID, toolName, toolInput, toolResult sql.NullString
		var source, channelID, userID, thinking sql.NullString

		if err := rows.Scan(
			&msg.ID, &msg.SessionKey, &parentID, &ts,
			&msg.Role, &msg.Content, &toolCallID, &toolName, &toolInput,
			&toolResult, &msg.ToolIsError, &source, &channelID, &userID,
			&msg.InputTokens, &msg.OutputTokens, &thinking,
		); err != nil {
			return nil, err
		}

		msg.Timestamp = time.Unix(ts, 0)
		msg.ParentID = parentID.String
		msg.ToolCallID = toolCallID.String
		msg.ToolName = toolName.String
		if toolInput.Valid {
			msg.ToolInput = []byte(toolInput.String)
		}
		msg.ToolResult = toolResult.String
		msg.Source = source.String
		msg.ChannelID = channelID.String
		msg.UserID = userID.String
		msg.Thinking = thinking.String

		messages = append(messages, msg)
	}

	return messages, rows.Err()
}

// GetPreviousCompaction returns the compaction before the given one (for finding message range)
func (s *SQLiteStore) GetPreviousCompaction(ctx context.Context, sessionKey string, beforeTimestamp time.Time) (*StoredCompaction, error) {
	var comp StoredCompaction
	var ts int64
	var parentID, firstKeptID, checkpointID sql.NullString

	err := s.db.QueryRowContext(ctx, `
		SELECT id, session_key, parent_id, timestamp,
		       summary, first_kept_entry_id, tokens_before, tokens_after,
		       messages_removed, from_checkpoint, checkpoint_id, needs_summary_retry
		FROM compactions
		WHERE session_key = ? AND timestamp < ?
		ORDER BY timestamp DESC
		LIMIT 1
	`, sessionKey, beforeTimestamp.Unix()).Scan(
		&comp.ID, &comp.SessionKey, &parentID, &ts,
		&comp.Summary, &firstKeptID, &comp.TokensBefore, &comp.TokensAfter,
		&comp.MessagesRemoved, &comp.FromCheckpoint, &checkpointID, &comp.NeedsSummaryRetry,
	)

	if err == sql.ErrNoRows {
		return nil, nil // No previous compaction
	}
	if err != nil {
		return nil, err
	}

	comp.Timestamp = time.Unix(ts, 0)
	comp.ParentID = parentID.String
	comp.FirstKeptEntryID = firstKeptID.String
	comp.CheckpointID = checkpointID.String

	return &comp, nil
}

// DeleteOrphanedToolMessages deletes ALL tool_use and tool_result messages from a session
// This is a nuclear option to fix corrupted tool pairing in session history
func (s *SQLiteStore) DeleteOrphanedToolMessages(ctx context.Context, sessionKey string) (int, error) {
	// Delete ALL tool messages (both tool_use and tool_result)
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM messages
		WHERE session_key = ?
		AND role IN ('tool_use', 'tool_result')
	`, sessionKey)
	if err != nil {
		return 0, fmt.Errorf("failed to delete tool messages: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows > 0 {
		L_info("sqlite: deleted all tool messages", "count", rows, "sessionKey", sessionKey)
	}

	return int(rows), nil
}

// Helper functions

func nullString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func repeatString(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}
