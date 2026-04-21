package session

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteStorePersistsMessagePhase(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "session.db")
	store, err := NewSQLiteStore(StoreConfig{
		Type:    "sqlite",
		Path:    dbPath,
		WALMode: true,
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	if _, err := store.DB().Exec("SELECT phase FROM messages LIMIT 1"); err != nil {
		t.Fatalf("expected messages.phase column to exist: %v", err)
	}

	sess := &StoredSession{
		Key:       "primary",
		ID:        "session-1",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	msg := &StoredMessage{
		ID:         "msg-1",
		SessionKey: "primary",
		Timestamp:  time.Now(),
		Role:       "assistant",
		Content:    "done",
		Phase:      "final_answer",
	}
	if err := store.AppendMessage(ctx, "primary", msg); err != nil {
		t.Fatalf("AppendMessage failed: %v", err)
	}

	msgs, err := store.GetMessages(ctx, "primary", MessageQueryOpts{})
	if err != nil {
		t.Fatalf("GetMessages failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Phase != "final_answer" {
		t.Fatalf("expected phase final_answer, got %q", msgs[0].Phase)
	}
}

func TestSQLiteStoreMigratesV7ToV8AddsPhaseColumn(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "session-v7.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open legacy sqlite failed: %v", err)
	}
	legacySchema := `
		CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL);
		INSERT INTO schema_version (version, applied_at) VALUES (7, 0);
		CREATE TABLE messages (
			id TEXT PRIMARY KEY,
			session_key TEXT NOT NULL,
			parent_id TEXT,
			timestamp INTEGER NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL DEFAULT '',
			tool_call_id TEXT,
			tool_name TEXT,
			tool_input TEXT,
			tool_result TEXT,
			tool_is_error INTEGER DEFAULT 0,
			source TEXT,
			channel_id TEXT,
			user_id TEXT,
			input_tokens INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0,
			raw_json TEXT,
			thinking TEXT,
			supervisor TEXT,
			intervention_type TEXT,
			response_group_id TEXT
		);
		CREATE TABLE compactions (
			id TEXT PRIMARY KEY,
			session_key TEXT NOT NULL,
			parent_id TEXT,
			timestamp INTEGER NOT NULL,
			summary TEXT NOT NULL,
			first_kept_entry_id TEXT,
			tokens_before INTEGER NOT NULL,
			tokens_after INTEGER DEFAULT 0,
			messages_removed INTEGER DEFAULT 0,
			from_checkpoint INTEGER DEFAULT 0,
			checkpoint_id TEXT,
			needs_summary_retry INTEGER DEFAULT 0
		);
	`
	if _, err := db.Exec(legacySchema); err != nil {
		_ = db.Close()
		t.Fatalf("create legacy schema failed: %v", err)
	}
	_ = db.Close()

	store, err := NewSQLiteStore(StoreConfig{
		Type:    "sqlite",
		Path:    dbPath,
		WALMode: true,
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore migration failed: %v", err)
	}
	defer store.Close()

	if _, err := store.DB().Exec("SELECT phase FROM messages LIMIT 1"); err != nil {
		t.Fatalf("expected phase column after migration: %v", err)
	}

	var version int
	if err := store.DB().QueryRow("SELECT version FROM schema_version ORDER BY version DESC LIMIT 1").Scan(&version); err != nil {
		t.Fatalf("read schema_version failed: %v", err)
	}
	if version != 10 {
		t.Fatalf("expected schema version 10 after migration, got %d", version)
	}
}

func TestSQLiteStoreMigratesV8ToV10AndBackfillsLegacyCompaction(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "session-v8.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}

	migrations := []func(*sql.DB) error{migrateV1, migrateV2, migrateV3, migrateV4, migrateV5, migrateV6, migrateV7, migrateV8}
	for _, migrate := range migrations {
		if err := migrate(db); err != nil {
			_ = db.Close()
			t.Fatalf("seed migrate failed: %v", err)
		}
	}

	now := time.Now().Unix()
	if _, err := db.Exec(`
		INSERT INTO sessions (key, id, created_at, updated_at, model, thinking_level, compaction_count, total_tokens, max_tokens, flushed_thresholds, flush_actioned)
		VALUES ('primary', 'session-1', ?, ?, '', '', 1, 100, 1000, '[]', 0)
	`, now, now); err != nil {
		_ = db.Close()
		t.Fatalf("insert session failed: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO messages (id, session_key, timestamp, role, content, tool_is_error, input_tokens, output_tokens)
		VALUES
		('1772237640000_000001', 'primary', 1713603600, 'user', 'hello', 0, 0, 0),
		('1772237641000_000002', 'primary', 1713603900, 'assistant', 'world', 0, 0, 0),
		('1772237642000_000003', 'primary', 1713604200, 'user', 'tail', 0, 0, 0)
	`); err != nil {
		_ = db.Close()
		t.Fatalf("insert messages failed: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO compactions (id, session_key, timestamp, summary, first_kept_entry_id, tokens_before, tokens_after, messages_removed, from_checkpoint, checkpoint_id, needs_summary_retry)
		VALUES ('1772237645628_000001', 'primary', 1713604500, 'legacy summary', '1772237642000_000003', 222, 100, 2, 0, NULL, 0)
	`); err != nil {
		_ = db.Close()
		t.Fatalf("insert compaction failed: %v", err)
	}
	_ = db.Close()

	store, err := NewSQLiteStore(StoreConfig{
		Type:       "sqlite",
		Path:       dbPath,
		WALMode:    true,
		LCMEnabled: true,
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore migration failed: %v", err)
	}
	defer store.Close()

	var version int
	if err := store.DB().QueryRow("SELECT version FROM schema_version ORDER BY version DESC LIMIT 1").Scan(&version); err != nil {
		t.Fatalf("read schema_version failed: %v", err)
	}
	if version != 10 {
		t.Fatalf("expected schema version 10 after migration, got %d", version)
	}

	var ftsRows int
	if err := store.DB().QueryRow("SELECT COUNT(*) FROM compactions_fts").Scan(&ftsRows); err != nil {
		t.Fatalf("count compactions_fts failed: %v", err)
	}
	if ftsRows != 1 {
		t.Fatalf("expected 1 FTS row after legacy bulk index, got %d", ftsRows)
	}

	comp, err := store.GetCompaction(ctx, "1772237645628_000001")
	if err != nil {
		t.Fatalf("GetCompaction failed: %v", err)
	}
	if comp == nil {
		t.Fatalf("expected compaction to exist")
	}
	if comp.Kind != CompactionKindLeaf {
		t.Fatalf("expected backfilled kind=leaf, got %q", comp.Kind)
	}
	if comp.Depth != 0 {
		t.Fatalf("expected backfilled depth 0, got %d", comp.Depth)
	}
	if len(comp.SourceMessageIDs) != 2 {
		t.Fatalf("expected 2 backfilled source messages, got %d", len(comp.SourceMessageIDs))
	}
	if comp.SourceMessageIDs[0] != "1772237640000_000001" || comp.SourceMessageIDs[1] != "1772237641000_000002" {
		t.Fatalf("unexpected backfilled source_message_ids: %#v", comp.SourceMessageIDs)
	}
	if comp.SourceTokenCount <= 0 {
		t.Fatalf("expected source token count to be estimated from fetched messages, got %d", comp.SourceTokenCount)
	}
	if comp.EarliestMessageAt == nil || comp.LatestMessageAt == nil {
		t.Fatalf("expected earliest/latest timestamps after backfill")
	}
}

func TestSQLiteStoreMigratesV9ToV10RepairsCompactionFTSTriggers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "session-v9.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}

	migrations := []func(*sql.DB) error{migrateV1, migrateV2, migrateV3, migrateV4, migrateV5, migrateV6, migrateV7, migrateV8, migrateV9}
	for _, migrate := range migrations {
		if err := migrate(db); err != nil {
			_ = db.Close()
			t.Fatalf("seed migrate failed: %v", err)
		}
	}

	now := time.Now().Unix()
	if _, err := db.Exec(`
		INSERT INTO compactions (id, session_key, timestamp, summary, first_kept_entry_id, tokens_before, tokens_after, messages_removed, from_checkpoint, checkpoint_id, needs_summary_retry)
		VALUES ('1776771279129_000014', 'primary', ?, 'placeholder summary', 'msg-tail', 222, 100, 2, 0, NULL, 1)
	`, now); err != nil {
		_ = db.Close()
		t.Fatalf("insert compaction failed: %v", err)
	}
	_ = db.Close()

	store, err := NewSQLiteStore(StoreConfig{
		Type:       "sqlite",
		Path:       dbPath,
		WALMode:    true,
		LCMEnabled: true,
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore migration failed: %v", err)
	}
	defer store.Close()

	var version int
	if err := store.DB().QueryRow("SELECT version FROM schema_version ORDER BY version DESC LIMIT 1").Scan(&version); err != nil {
		t.Fatalf("read schema_version failed: %v", err)
	}
	if version != 10 {
		t.Fatalf("expected schema version 10 after migration, got %d", version)
	}

	const updatedSummary = "updated summary with real details"
	if err := store.UpdateCompactionSummary(ctx, "1776771279129_000014", updatedSummary); err != nil {
		t.Fatalf("UpdateCompactionSummary failed after v10 migration: %v", err)
	}

	var ftsContent string
	if err := store.DB().QueryRow("SELECT content FROM compactions_fts").Scan(&ftsContent); err != nil {
		t.Fatalf("read compactions_fts failed: %v", err)
	}
	if ftsContent != updatedSummary {
		t.Fatalf("expected rebuilt FTS content %q, got %q", updatedSummary, ftsContent)
	}

	if _, err := store.DB().Exec(`DELETE FROM compactions WHERE id = ?`, "1776771279129_000014"); err != nil {
		t.Fatalf("delete compaction failed after v10 migration: %v", err)
	}

	var ftsRows int
	if err := store.DB().QueryRow("SELECT COUNT(*) FROM compactions_fts").Scan(&ftsRows); err != nil {
		t.Fatalf("count compactions_fts failed: %v", err)
	}
	if ftsRows != 0 {
		t.Fatalf("expected FTS rows to be deleted, got %d", ftsRows)
	}
}

func TestSummaryAndMessageIDPrefixesRoundTrip(t *testing.T) {
	t.Parallel()

	summaryID := "1772237645628_000001"
	formattedSummary := FormatSummaryID(summaryID)
	if formattedSummary != "sum_1772237645628_000001" {
		t.Fatalf("unexpected formatted summary id: %s", formattedSummary)
	}
	parsedSummary, err := ParseSummaryID(formattedSummary)
	if err != nil {
		t.Fatalf("ParseSummaryID failed: %v", err)
	}
	if parsedSummary != summaryID {
		t.Fatalf("expected parsed summary id %q, got %q", summaryID, parsedSummary)
	}

	messageID := "1772237640000_000001"
	formattedMessage := FormatMessageID(messageID)
	if formattedMessage != "msg_1772237640000_000001" {
		t.Fatalf("unexpected formatted message id: %s", formattedMessage)
	}
	parsedMessage, err := ParseMessageID(formattedMessage)
	if err != nil {
		t.Fatalf("ParseMessageID failed: %v", err)
	}
	if parsedMessage != messageID {
		t.Fatalf("expected parsed message id %q, got %q", messageID, parsedMessage)
	}
}
