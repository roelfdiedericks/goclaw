package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
const currentSchemaVersion = 10

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
		migrateV7,
		migrateV8,
		migrateV9,
		migrateV10,
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

// migrateV7 adds response_group_id column for grouping multi-tool call batches
func migrateV7(db *sql.DB) error {
	schema := `
	-- Add response_group_id column to group tool calls from same LLM response
	ALTER TABLE messages ADD COLUMN response_group_id TEXT DEFAULT NULL;
	
	-- Index for efficient queries by response group (e.g., "get all tools from this batch")
	CREATE INDEX IF NOT EXISTS idx_messages_response_group ON messages(response_group_id) WHERE response_group_id IS NOT NULL;
	
	-- Update schema version
	INSERT INTO schema_version (version, applied_at) VALUES (7, ?);
	`

	_, err := db.Exec(schema, time.Now().Unix())
	return err
}

// migrateV8 adds phase column for assistant phase replay metadata.
func migrateV8(db *sql.DB) error {
	schema := `
	-- Add phase column to preserve assistant message phase for Responses API replay
	ALTER TABLE messages ADD COLUMN phase TEXT DEFAULT NULL;

	-- Update schema version
	INSERT INTO schema_version (version, applied_at) VALUES (8, ?);
	`

	_, err := db.Exec(schema, time.Now().Unix())
	return err
}

// migrateV9 adds LCM DAG fields plus compaction FTS indexing.
func migrateV9(db *sql.DB) error {
	start := time.Now()

	schema := `
	ALTER TABLE compactions ADD COLUMN kind TEXT DEFAULT NULL;
	ALTER TABLE compactions ADD COLUMN depth INTEGER DEFAULT 0;
	ALTER TABLE compactions ADD COLUMN source_message_ids TEXT DEFAULT NULL;
	ALTER TABLE compactions ADD COLUMN child_compaction_ids TEXT DEFAULT NULL;
	ALTER TABLE compactions ADD COLUMN earliest_message_at INTEGER DEFAULT NULL;
	ALTER TABLE compactions ADD COLUMN latest_message_at INTEGER DEFAULT NULL;
	ALTER TABLE compactions ADD COLUMN source_token_count INTEGER DEFAULT 0;

	CREATE INDEX IF NOT EXISTS idx_compactions_session_depth ON compactions(session_key, depth, timestamp);
	CREATE INDEX IF NOT EXISTS idx_compactions_kind_depth ON compactions(kind, depth, timestamp);

	CREATE VIRTUAL TABLE IF NOT EXISTS compactions_fts USING fts5(content);

	CREATE TRIGGER IF NOT EXISTS compactions_ai AFTER INSERT ON compactions BEGIN
		INSERT INTO compactions_fts(rowid, content) VALUES (new.rowid, new.summary);
	END;

	CREATE TRIGGER IF NOT EXISTS compactions_ad AFTER DELETE ON compactions BEGIN
		INSERT INTO compactions_fts(compactions_fts, rowid, content) VALUES ('delete', old.rowid, old.summary);
	END;

	CREATE TRIGGER IF NOT EXISTS compactions_au AFTER UPDATE OF summary ON compactions BEGIN
		INSERT INTO compactions_fts(compactions_fts, rowid, content) VALUES ('delete', old.rowid, old.summary);
		INSERT INTO compactions_fts(rowid, content) VALUES (new.rowid, new.summary);
	END;

	INSERT INTO schema_version (version, applied_at) VALUES (9, ?);
	`

	if _, err := db.Exec(schema, time.Now().Unix()); err != nil {
		return err
	}

	result, err := db.Exec(`INSERT INTO compactions_fts(rowid, content) SELECT rowid, summary FROM compactions`)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	L_info("lcm: bulk-indexed legacy compactions for FTS", "rows", rows, "durationMs", time.Since(start).Milliseconds())

	return nil
}

// migrateV10 repairs compaction FTS triggers and rebuilds the derived index.
func migrateV10(db *sql.DB) error {
	start := time.Now()

	schema := `
	DROP TRIGGER IF EXISTS compactions_ai;
	DROP TRIGGER IF EXISTS compactions_ad;
	DROP TRIGGER IF EXISTS compactions_au;
	DROP TABLE IF EXISTS compactions_fts;

	CREATE VIRTUAL TABLE compactions_fts USING fts5(content);

	CREATE TRIGGER IF NOT EXISTS compactions_ai AFTER INSERT ON compactions BEGIN
		INSERT INTO compactions_fts(rowid, content) VALUES (new.rowid, new.summary);
	END;

	CREATE TRIGGER IF NOT EXISTS compactions_ad AFTER DELETE ON compactions BEGIN
		DELETE FROM compactions_fts WHERE rowid = old.rowid;
	END;

	CREATE TRIGGER IF NOT EXISTS compactions_au AFTER UPDATE OF summary ON compactions BEGIN
		DELETE FROM compactions_fts WHERE rowid = old.rowid;
		INSERT INTO compactions_fts(rowid, content) VALUES (new.rowid, new.summary);
	END;

	INSERT INTO schema_version (version, applied_at) VALUES (10, ?);
	`

	if _, err := db.Exec(schema, time.Now().Unix()); err != nil {
		return err
	}

	result, err := db.Exec(`INSERT INTO compactions_fts(rowid, content) SELECT rowid, summary FROM compactions`)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	L_info("lcm: rebuilt compaction FTS index", "rows", rows, "durationMs", time.Since(start).Milliseconds())

	return nil
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

// ListCompactionSessionKeys returns every distinct session_key present in the
// compactions table. See the Store interface doc for why this exists —
// condensation drives iteration from here, not from the sessions table.
func (s *SQLiteStore) ListCompactionSessionKeys(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT session_key
		FROM compactions
		ORDER BY session_key
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

// AppendMessage appends a message to a session
func (s *SQLiteStore) AppendMessage(ctx context.Context, sessionKey string, msg *StoredMessage) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (id, session_key, parent_id, timestamp,
		                      role, content, tool_call_id, tool_name, tool_input,
		                      tool_result, tool_is_error, source, channel_id, user_id,
		                      input_tokens, output_tokens, raw_json, thinking, phase,
		                      supervisor, intervention_type, response_group_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		msg.ID, sessionKey, nullString(msg.ParentID), msg.Timestamp.Unix(),
		msg.Role, msg.Content, nullString(msg.ToolCallID), nullString(msg.ToolName), msg.ToolInput,
		nullString(msg.ToolResult), msg.ToolIsError, nullString(msg.Source), nullString(msg.ChannelID), nullString(msg.UserID),
		msg.InputTokens, msg.OutputTokens, msg.RawJSON, nullString(msg.Thinking), nullString(msg.Phase),
		nullString(msg.Supervisor), nullString(msg.InterventionType), nullString(msg.ResponseGroupID),
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
	// #nosec G202 -- placeholder list is generated internally; values remain parameterized
	query := `
		SELECT id, session_key, parent_id, timestamp, role, content,
		       tool_call_id, tool_name, tool_input, tool_result, tool_is_error,
		       source, channel_id, user_id, input_tokens, output_tokens, raw_json, thinking, phase,
		       supervisor, intervention_type, response_group_id
		FROM messages
		WHERE session_key = ?
	`
	args := []interface{}{sessionKey}

	if opts.AfterID != "" {
		query += " AND timestamp > (SELECT timestamp FROM messages WHERE id = ?)"
		args = append(args, opts.AfterID)
	}

	if opts.SinceID != "" {
		query += " AND timestamp >= (SELECT timestamp FROM messages WHERE id = ?)"
		args = append(args, opts.SinceID)
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
		var parentID, toolCallID, toolName, toolResult, source, channelID, userID, thinking, phase sql.NullString
		var supervisor, interventionType, responseGroupID sql.NullString
		var toolInput, rawJSON []byte

		if err := rows.Scan(
			&msg.ID, &msg.SessionKey, &parentID, &ts, &msg.Role, &msg.Content,
			&toolCallID, &toolName, &toolInput, &toolResult, &msg.ToolIsError,
			&source, &channelID, &userID, &msg.InputTokens, &msg.OutputTokens, &rawJSON, &thinking, &phase,
			&supervisor, &interventionType, &responseGroupID,
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
		msg.Phase = phase.String
		msg.Supervisor = supervisor.String
		msg.InterventionType = interventionType.String
		msg.ResponseGroupID = responseGroupID.String
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

func (s *SQLiteStore) GetMessagesByIDs(ctx context.Context, sessionKey string, ids []string) ([]StoredMessage, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]interface{}, 0, len(ids)+1)
	args = append(args, sessionKey)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}

	var queryBuilder strings.Builder
	queryBuilder.WriteString(`
		SELECT id, session_key, parent_id, timestamp, role, content,
		       tool_call_id, tool_name, tool_input, tool_result, tool_is_error,
		       source, channel_id, user_id, input_tokens, output_tokens, raw_json, thinking, phase,
		       supervisor, intervention_type, response_group_id
		FROM messages
		WHERE session_key = ? AND id IN (?`)
	queryBuilder.WriteString(repeatString(",?", len(ids)-1))
	queryBuilder.WriteString(`)
		ORDER BY timestamp ASC
	`)
	query := queryBuilder.String()

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []StoredMessage
	for rows.Next() {
		var msg StoredMessage
		var ts int64
		var parentID, toolCallID, toolName, toolResult, source, channelID, userID, thinking, phase sql.NullString
		var supervisor, interventionType, responseGroupID sql.NullString
		var toolInput, rawJSON []byte

		if err := rows.Scan(
			&msg.ID, &msg.SessionKey, &parentID, &ts, &msg.Role, &msg.Content,
			&toolCallID, &toolName, &toolInput, &toolResult, &msg.ToolIsError,
			&source, &channelID, &userID, &msg.InputTokens, &msg.OutputTokens, &rawJSON, &thinking, &phase,
			&supervisor, &interventionType, &responseGroupID,
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
		msg.Phase = phase.String
		msg.Supervisor = supervisor.String
		msg.InterventionType = interventionType.String
		msg.ResponseGroupID = responseGroupID.String
		msg.RawJSON = rawJSON

		messages = append(messages, msg)
	}

	return messages, rows.Err()
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
	sourceMessageIDsJSON, _ := json.Marshal(comp.SourceMessageIDs)
	childCompactionIDsJSON, _ := json.Marshal(comp.ChildCompactionIDs)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO compactions (id, session_key, parent_id, timestamp,
		                         summary, first_kept_entry_id, tokens_before, tokens_after,
		                         messages_removed, from_checkpoint, checkpoint_id, needs_summary_retry,
		                         kind, depth, source_message_ids, child_compaction_ids,
		                         earliest_message_at, latest_message_at, source_token_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		comp.ID, sessionKey, nullString(comp.ParentID), comp.Timestamp.Unix(),
		comp.Summary, nullString(comp.FirstKeptEntryID), comp.TokensBefore, comp.TokensAfter,
		comp.MessagesRemoved, comp.FromCheckpoint, nullString(comp.CheckpointID), comp.NeedsSummaryRetry,
		nullString(string(comp.Kind)), comp.Depth, nullString(string(sourceMessageIDsJSON)), nullString(string(childCompactionIDsJSON)),
		nullUnixTime(comp.EarliestMessageAt), nullUnixTime(comp.LatestMessageAt), comp.SourceTokenCount,
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

const compactionSelectColumns = `
	id, session_key, parent_id, timestamp,
	summary, first_kept_entry_id, tokens_before, tokens_after,
	messages_removed, from_checkpoint, checkpoint_id, needs_summary_retry,
	kind, depth, source_message_ids, child_compaction_ids,
	earliest_message_at, latest_message_at, source_token_count
`

func scanStoredCompaction(scanner interface{ Scan(dest ...any) error }) (StoredCompaction, error) {
	var comp StoredCompaction
	var ts int64
	var parentID, firstKeptID, checkpointID sql.NullString
	var kind sql.NullString
	var sourceMessageIDsJSON, childCompactionIDsJSON sql.NullString
	var earliestAt, latestAt sql.NullInt64

	err := scanner.Scan(
		&comp.ID, &comp.SessionKey, &parentID, &ts,
		&comp.Summary, &firstKeptID, &comp.TokensBefore, &comp.TokensAfter,
		&comp.MessagesRemoved, &comp.FromCheckpoint, &checkpointID, &comp.NeedsSummaryRetry,
		&kind, &comp.Depth, &sourceMessageIDsJSON, &childCompactionIDsJSON,
		&earliestAt, &latestAt, &comp.SourceTokenCount,
	)
	if err != nil {
		return StoredCompaction{}, err
	}

	comp.Timestamp = time.Unix(ts, 0).UTC()
	comp.ParentID = parentID.String
	comp.FirstKeptEntryID = firstKeptID.String
	comp.CheckpointID = checkpointID.String
	comp.Kind = CompactionKind(kind.String)
	if comp.SourceMessageIDs, err = parseStringSliceJSON(sourceMessageIDsJSON); err != nil {
		return StoredCompaction{}, fmt.Errorf("parse source_message_ids for %s: %w", comp.ID, err)
	}
	if comp.ChildCompactionIDs, err = parseStringSliceJSON(childCompactionIDsJSON); err != nil {
		return StoredCompaction{}, fmt.Errorf("parse child_compaction_ids for %s: %w", comp.ID, err)
	}
	if earliestAt.Valid {
		t := time.Unix(earliestAt.Int64, 0).UTC()
		comp.EarliestMessageAt = &t
	}
	if latestAt.Valid {
		t := time.Unix(latestAt.Int64, 0).UTC()
		comp.LatestMessageAt = &t
	}

	return comp, nil
}

func parseStringSliceJSON(raw sql.NullString) ([]string, error) {
	if !raw.Valid || raw.String == "" {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw.String), &values); err != nil {
		return nil, err
	}
	return values, nil
}

func nullUnixTime(t *time.Time) interface{} {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.Unix()
}

func (s *SQLiteStore) needsCompactionBackfill(comp *StoredCompaction) bool {
	if !s.config.LCMEnabled || comp == nil {
		return false
	}
	if comp.Kind != "" || len(comp.SourceMessageIDs) > 0 || len(comp.ChildCompactionIDs) > 0 {
		return false
	}
	if comp.EarliestMessageAt != nil || comp.LatestMessageAt != nil || comp.SourceTokenCount > 0 {
		return false
	}
	return comp.FirstKeptEntryID != ""
}

func (s *SQLiteStore) maybeBackfillCompaction(ctx context.Context, comp *StoredCompaction) error {
	if !s.needsCompactionBackfill(comp) {
		return nil
	}

	start := time.Now()
	if err := s.backfillCompactionDAG(ctx, comp.ID); err != nil {
		L_warn("lcm: backfill failed, falling back to minimal XML", "id", comp.ID, "error", err)
		return err
	}

	refreshed, err := s.getCompactionRaw(ctx, comp.ID)
	if err != nil {
		return err
	}
	if refreshed != nil {
		*comp = *refreshed
	}
	L_info("lcm: backfilled compaction DAG fields", "id", comp.ID, "sourceMessages", len(comp.SourceMessageIDs), "durationMs", time.Since(start).Milliseconds())
	return nil
}

func (s *SQLiteStore) getCompactionRaw(ctx context.Context, compactionID string) (*StoredCompaction, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+compactionSelectColumns+`
		FROM compactions
		WHERE id = ?
	`, compactionID)
	comp, err := scanStoredCompaction(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &comp, nil
}

func (s *SQLiteStore) backfillCompactionDAG(ctx context.Context, compactionID string) error {
	comp, err := s.getCompactionRaw(ctx, compactionID)
	if err != nil {
		return err
	}
	if comp == nil || !s.needsCompactionBackfill(comp) {
		return nil
	}

	prevCompaction, err := s.getPreviousCompactionRaw(ctx, comp.SessionKey, comp.Timestamp)
	if err != nil {
		return err
	}

	var startAfterID string
	if prevCompaction != nil {
		startAfterID = prevCompaction.FirstKeptEntryID
	}

	messages, err := s.GetMessagesInRange(ctx, comp.SessionKey, startAfterID, comp.FirstKeptEntryID)
	if err != nil {
		return err
	}

	var (
		sourceIDs    []string
		earliestAt   *time.Time
		latestAt     *time.Time
		sourceTokens int
	)
	if len(messages) > 0 {
		sourceIDs = make([]string, 0, len(messages))
		for i := range messages {
			sourceIDs = append(sourceIDs, messages[i].ID)
			ts := messages[i].Timestamp.UTC()
			if earliestAt == nil || ts.Before(*earliestAt) {
				earliestAt = cloneTimePtr(&ts)
			}
			if latestAt == nil || ts.After(*latestAt) {
				latestAt = cloneTimePtr(&ts)
			}
		}
		sourceTokens = estimateStoredMessageTokens(messages)
	}

	return s.UpdateCompactionDAG(ctx, comp.ID, CompactionDAGUpdate{
		Kind:              CompactionKindLeaf,
		Depth:             0,
		SourceMessageIDs:  sourceIDs,
		EarliestMessageAt: earliestAt,
		LatestMessageAt:   latestAt,
		SourceTokenCount:  sourceTokens,
	})
}

func estimateStoredMessageTokens(messages []StoredMessage) int {
	estimator := GetTokenEstimator()
	total := 0
	for i := range messages {
		msg := Message{
			ID:        messages[i].ID,
			Role:      messages[i].Role,
			Content:   messages[i].Content,
			Timestamp: messages[i].Timestamp,
			ToolName:  messages[i].ToolName,
			ToolInput: messages[i].ToolInput,
		}
		if messages[i].Role == "tool_result" && messages[i].ToolResult != "" {
			msg.Content = messages[i].ToolResult
		}
		total += estimator.EstimateMessageTokens(&msg)
	}
	return total
}

// GetCompactions returns all compactions for a session
func (s *SQLiteStore) GetCompactions(ctx context.Context, sessionKey string) ([]StoredCompaction, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+compactionSelectColumns+`
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
		comp, err := scanStoredCompaction(rows)
		if err != nil {
			return nil, err
		}
		if err := s.maybeBackfillCompaction(ctx, &comp); err != nil {
			L_debug("lcm: compaction read continuing without DAG metadata", "id", comp.ID, "error", err)
		}
		compactions = append(compactions, comp)
	}

	return compactions, rows.Err()
}

// GetLatestCompaction returns the most recent compaction for a session, or nil if none exist.
func (s *SQLiteStore) GetLatestCompaction(ctx context.Context, sessionKey string) (*StoredCompaction, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+compactionSelectColumns+`
		FROM compactions
		WHERE session_key = ?
		ORDER BY timestamp DESC
		LIMIT 1
	`, sessionKey)
	comp, err := scanStoredCompaction(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := s.maybeBackfillCompaction(ctx, &comp); err != nil {
		L_debug("lcm: latest compaction read continuing without DAG metadata", "id", comp.ID, "error", err)
	}
	return &comp, nil
}

func (s *SQLiteStore) GetCompaction(ctx context.Context, compactionID string) (*StoredCompaction, error) {
	comp, err := s.getCompactionRaw(ctx, compactionID)
	if err != nil || comp == nil {
		return comp, err
	}
	if err := s.maybeBackfillCompaction(ctx, comp); err != nil {
		L_debug("lcm: compaction read continuing without DAG metadata", "id", comp.ID, "error", err)
	}
	return comp, nil
}

func (s *SQLiteStore) GetCompactionsByDepth(ctx context.Context, sessionKey string, depth int, kind CompactionKind) ([]StoredCompaction, error) {
	query := `
		SELECT ` + compactionSelectColumns + `
		FROM compactions
		WHERE session_key = ? AND depth = ?
	`
	args := []interface{}{sessionKey, depth}
	if kind != "" {
		query += ` AND kind = ?`
		args = append(args, string(kind))
	}
	query += ` ORDER BY timestamp ASC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var compactions []StoredCompaction
	for rows.Next() {
		comp, err := scanStoredCompaction(rows)
		if err != nil {
			return nil, err
		}
		if err := s.maybeBackfillCompaction(ctx, &comp); err != nil {
			L_debug("lcm: compaction-by-depth read continuing without DAG metadata", "id", comp.ID, "error", err)
		}
		compactions = append(compactions, comp)
	}
	return compactions, rows.Err()
}

func (s *SQLiteStore) GetCompactionChildren(ctx context.Context, compactionID string) ([]StoredCompaction, error) {
	parent, err := s.GetCompaction(ctx, compactionID)
	if err != nil {
		return nil, err
	}
	if parent == nil || len(parent.ChildCompactionIDs) == 0 {
		return nil, nil
	}

	children := make([]StoredCompaction, 0, len(parent.ChildCompactionIDs))
	for _, childID := range parent.ChildCompactionIDs {
		child, err := s.GetCompaction(ctx, childID)
		if err != nil {
			return nil, err
		}
		if child != nil {
			children = append(children, *child)
		}
	}
	return children, nil
}

func (s *SQLiteStore) SearchCompactionsFTS(ctx context.Context, sessionKey, query string, limit int, mode CompactionSearchMode, sort CompactionSearchSort) ([]CompactionSearchResult, error) {
	if limit <= 0 {
		limit = 50
	}
	switch mode {
	case "", CompactionSearchModeFTS:
		return s.searchCompactionsFTS(ctx, sessionKey, query, limit, sort)
	case CompactionSearchModeRegex:
		return s.searchCompactionsRegex(ctx, sessionKey, query, limit)
	default:
		return nil, fmt.Errorf("unknown compaction search mode: %s", mode)
	}
}

func (s *SQLiteStore) UpdateCompactionDAG(ctx context.Context, compactionID string, dag CompactionDAGUpdate) error {
	sourceMessageIDsJSON, _ := json.Marshal(dag.SourceMessageIDs)
	childCompactionIDsJSON, _ := json.Marshal(dag.ChildCompactionIDs)

	result, err := s.db.ExecContext(ctx, `
		UPDATE compactions
		SET kind = ?, depth = ?, source_message_ids = ?, child_compaction_ids = ?,
		    earliest_message_at = ?, latest_message_at = ?, source_token_count = ?
		WHERE id = ?
	`,
		nullString(string(dag.Kind)), dag.Depth, nullString(string(sourceMessageIDsJSON)), nullString(string(childCompactionIDsJSON)),
		nullUnixTime(dag.EarliestMessageAt), nullUnixTime(dag.LatestMessageAt), dag.SourceTokenCount, compactionID,
	)
	if err != nil {
		return fmt.Errorf("update compaction DAG failed: %w", err)
	}
	rows, _ := result.RowsAffected()
	L_debug("lcm: compaction DAG updated", "id", compactionID, "rowsAffected", rows, "kind", dag.Kind, "depth", dag.Depth)
	return nil
}

// GetPendingSummaryRetry returns a compaction that needs summary retry (for background processing)
// Returns nil if no pending retries found
func (s *SQLiteStore) GetPendingSummaryRetry(ctx context.Context) (*StoredCompaction, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+compactionSelectColumns+`
		FROM compactions
		WHERE needs_summary_retry = 1
		ORDER BY timestamp ASC
		LIMIT 1
	`)
	comp, err := scanStoredCompaction(row)
	if err == sql.ErrNoRows {
		return nil, nil // No pending retries
	}
	if err != nil {
		return nil, err
	}
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

// UpdateCompactionStats updates telemetry fields for a compaction row after truncation.
func (s *SQLiteStore) UpdateCompactionStats(ctx context.Context, compactionID string, tokensAfter, messagesRemoved int) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE compactions
		SET tokens_after = ?, messages_removed = ?
		WHERE id = ?
	`, tokensAfter, messagesRemoved, compactionID)
	if err != nil {
		return fmt.Errorf("update compaction stats failed: %w", err)
	}
	rows, _ := result.RowsAffected()
	L_info("sqlite: compaction stats updated",
		"id", compactionID,
		"tokensAfter", tokensAfter,
		"messagesRemoved", messagesRemoved,
		"rowsAffected", rows)
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
			       input_tokens, output_tokens, thinking, phase, response_group_id
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
			       input_tokens, output_tokens, thinking, phase, response_group_id
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
		var source, channelID, userID, thinking, phase, responseGroupID sql.NullString

		if err := rows.Scan(
			&msg.ID, &msg.SessionKey, &parentID, &ts,
			&msg.Role, &msg.Content, &toolCallID, &toolName, &toolInput,
			&toolResult, &msg.ToolIsError, &source, &channelID, &userID,
			&msg.InputTokens, &msg.OutputTokens, &thinking, &phase, &responseGroupID,
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
		msg.Phase = phase.String
		msg.ResponseGroupID = responseGroupID.String

		messages = append(messages, msg)
	}

	return messages, rows.Err()
}

// GetPreviousCompaction returns the compaction before the given one (for finding message range)
func (s *SQLiteStore) GetPreviousCompaction(ctx context.Context, sessionKey string, beforeTimestamp time.Time) (*StoredCompaction, error) {
	comp, err := s.getPreviousCompactionRaw(ctx, sessionKey, beforeTimestamp)
	if err != nil || comp == nil {
		return comp, err
	}
	if err := s.maybeBackfillCompaction(ctx, comp); err != nil {
		L_debug("lcm: previous compaction read continuing without DAG metadata", "id", comp.ID, "error", err)
	}
	return comp, nil
}

func (s *SQLiteStore) getPreviousCompactionRaw(ctx context.Context, sessionKey string, beforeTimestamp time.Time) (*StoredCompaction, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+compactionSelectColumns+`
		FROM compactions
		WHERE session_key = ? AND timestamp < ?
		ORDER BY timestamp DESC
		LIMIT 1
	`, sessionKey, beforeTimestamp.Unix())
	comp, err := scanStoredCompaction(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &comp, nil
}

func (s *SQLiteStore) searchCompactionsFTS(ctx context.Context, sessionKey, query string, limit int, sort CompactionSearchSort) ([]CompactionSearchResult, error) {
	var orderBy string
	switch sort {
	case CompactionSearchSortRelevance:
		orderBy = "score ASC, c.timestamp DESC"
	case CompactionSearchSortHybrid:
		orderBy = "score ASC, c.timestamp DESC"
	case "", CompactionSearchSortRecency:
		orderBy = "c.timestamp DESC, score ASC"
	default:
		return nil, fmt.Errorf("unknown compaction search sort: %s", sort)
	}

	//nolint:gosec // orderBy is internal, values are parameterized
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT %s, bm25(compactions_fts) AS score
		FROM compactions_fts
		JOIN compactions c ON c.rowid = compactions_fts.rowid
		WHERE c.session_key = ? AND compactions_fts MATCH ?
		ORDER BY %s
		LIMIT ?
	`, compactionSelectColumns, orderBy), sessionKey, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []CompactionSearchResult
	for rows.Next() {
		comp, score, err := scanStoredCompactionWithScore(rows)
		if err != nil {
			return nil, err
		}
		if err := s.maybeBackfillCompaction(ctx, &comp); err != nil {
			L_debug("lcm: FTS result continuing without DAG metadata", "id", comp.ID, "error", err)
		}
		results = append(results, CompactionSearchResult{
			Compaction:   comp,
			MatchSource:  "fts",
			Relevance:    score,
			MatchedQuery: query,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func scanStoredCompactionWithScore(scanner interface{ Scan(dest ...any) error }) (StoredCompaction, float64, error) {
	var score float64
	comp, err := scanStoredCompaction(scoreScanner{scanner: scanner, score: &score})
	return comp, score, err
}

type scoreScanner struct {
	scanner interface{ Scan(dest ...any) error }
	score   *float64
}

func (s scoreScanner) Scan(dest ...any) error {
	dest = append(dest, s.score)
	return s.scanner.Scan(dest...)
}

func (s *SQLiteStore) searchCompactionsRegex(ctx context.Context, sessionKey, query string, limit int) ([]CompactionSearchResult, error) {
	re, err := regexp.Compile(query)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+compactionSelectColumns+`
		FROM compactions
		WHERE session_key = ?
		ORDER BY timestamp DESC
	`, sessionKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []CompactionSearchResult
	for rows.Next() {
		comp, err := scanStoredCompaction(rows)
		if err != nil {
			return nil, err
		}
		if !re.MatchString(comp.Summary) {
			continue
		}
		if err := s.maybeBackfillCompaction(ctx, &comp); err != nil {
			L_debug("lcm: regex result continuing without DAG metadata", "id", comp.ID, "error", err)
		}
		results = append(results, CompactionSearchResult{
			Compaction:   comp,
			MatchSource:  "regex",
			MatchedQuery: query,
		})
		if len(results) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *SQLiteStore) GetCompactionDAGStats(ctx context.Context, sessionKey string) (CompactionDAGStats, error) {
	compactions, err := s.GetCompactions(ctx, sessionKey)
	if err != nil {
		return CompactionDAGStats{}, err
	}

	stats := buildCompactionDAGStats(compactions)

	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM compactions_fts
		JOIN compactions c ON c.rowid = compactions_fts.rowid
		WHERE c.session_key = ?
	`, sessionKey).Scan(&stats.FTSRows); err != nil {
		return CompactionDAGStats{}, err
	}

	return stats, nil
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

// ============================================================================
// Provider State CRUD
// ============================================================================

// GetProviderState retrieves state for a (session_key, provider_key) tuple.
// providerKey format: "providerName:model" (e.g., "xai:grok-4-1-fast-reasoning")
// Returns nil if no state exists (not an error).
func (s *SQLiteStore) GetProviderState(ctx context.Context, sessionKey, providerKey string) (map[string]any, error) {
	var stateJSON string
	err := s.db.QueryRowContext(ctx, `
		SELECT state_json FROM provider_state 
		WHERE session_key = ? AND provider_key = ?
	`, sessionKey, providerKey).Scan(&stateJSON)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query provider state failed: %w", err)
	}

	var state map[string]any
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return nil, fmt.Errorf("unmarshal provider state failed: %w", err)
	}

	L_trace("sqlite: got provider state", "sessionKey", sessionKey, "providerKey", providerKey)
	return state, nil
}

// SetProviderState saves state for a (session_key, provider_key) tuple.
// providerKey format: "providerName:model" (e.g., "openrouter1:anthropic/claude-sonnet-4.5")
// Pass nil state to delete the entry.
func (s *SQLiteStore) SetProviderState(ctx context.Context, sessionKey, providerKey string, state map[string]any) error {
	if state == nil {
		_, err := s.db.ExecContext(ctx, `
			DELETE FROM provider_state WHERE session_key = ? AND provider_key = ?
		`, sessionKey, providerKey)
		if err != nil {
			return fmt.Errorf("delete provider state failed: %w", err)
		}
		L_trace("sqlite: deleted provider state", "sessionKey", sessionKey, "providerKey", providerKey)
		return nil
	}

	stateJSON, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal provider state failed: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO provider_state (session_key, provider_key, state_json, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(session_key, provider_key) DO UPDATE SET
			state_json = excluded.state_json,
			updated_at = excluded.updated_at
	`, sessionKey, providerKey, string(stateJSON), time.Now().Unix())
	if err != nil {
		return fmt.Errorf("upsert provider state failed: %w", err)
	}

	L_trace("sqlite: set provider state", "sessionKey", sessionKey, "providerKey", providerKey)
	return nil
}

// DeleteProviderStates deletes all provider states for a session.
// Note: CASCADE on FK handles this automatically when session is deleted,
// but this method allows explicit cleanup without deleting the session.
func (s *SQLiteStore) DeleteProviderStates(ctx context.Context, sessionKey string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM provider_state WHERE session_key = ?`, sessionKey)
	if err != nil {
		return fmt.Errorf("delete provider states failed: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows > 0 {
		L_debug("sqlite: deleted provider states", "sessionKey", sessionKey, "count", rows)
	}
	return nil
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
