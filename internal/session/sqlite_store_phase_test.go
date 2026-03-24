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
	if version != 8 {
		t.Fatalf("expected schema version 8 after migration, got %d", version)
	}
}
