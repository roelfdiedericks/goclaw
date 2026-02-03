package transcript

import (
	"database/sql"
	"fmt"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// initSchema creates the transcript search tables and indexes
func initSchema(db *sql.DB) error {
	L_debug("transcript: initializing schema")

	// Create transcript_chunks table
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS transcript_chunks (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			session_key TEXT NOT NULL,
			message_ids TEXT NOT NULL,
			timestamp_start INTEGER NOT NULL,
			timestamp_end INTEGER NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			embedding BLOB,
			embedding_model TEXT,
			created_at INTEGER NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("create transcript_chunks table: %w", err)
	}

	// Create indexes for efficient queries
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_transcript_user ON transcript_chunks(user_id)`); err != nil {
		return fmt.Errorf("create idx_transcript_user: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_transcript_session ON transcript_chunks(session_key)`); err != nil {
		return fmt.Errorf("create idx_transcript_session: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_transcript_time ON transcript_chunks(timestamp_start)`); err != nil {
		return fmt.Errorf("create idx_transcript_time: %w", err)
	}

	// Create FTS5 virtual table for full-text keyword search
	if _, err := db.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS transcript_fts USING fts5(
			content,
			id UNINDEXED,
			user_id UNINDEXED,
			session_key UNINDEXED,
			content='transcript_chunks',
			content_rowid='rowid'
		)
	`); err != nil {
		return fmt.Errorf("create transcript_fts table: %w", err)
	}

	// Triggers to keep FTS5 in sync with chunks table
	if _, err := db.Exec(`
		CREATE TRIGGER IF NOT EXISTS transcript_chunks_ai AFTER INSERT ON transcript_chunks BEGIN
			INSERT INTO transcript_fts(rowid, content, id, user_id, session_key)
			VALUES (NEW.rowid, NEW.content, NEW.id, NEW.user_id, NEW.session_key);
		END
	`); err != nil {
		return fmt.Errorf("create insert trigger: %w", err)
	}

	if _, err := db.Exec(`
		CREATE TRIGGER IF NOT EXISTS transcript_chunks_ad AFTER DELETE ON transcript_chunks BEGIN
			INSERT INTO transcript_fts(transcript_fts, rowid, content, id, user_id, session_key)
			VALUES ('delete', OLD.rowid, OLD.content, OLD.id, OLD.user_id, OLD.session_key);
		END
	`); err != nil {
		return fmt.Errorf("create delete trigger: %w", err)
	}

	if _, err := db.Exec(`
		CREATE TRIGGER IF NOT EXISTS transcript_chunks_au AFTER UPDATE ON transcript_chunks BEGIN
			INSERT INTO transcript_fts(transcript_fts, rowid, content, id, user_id, session_key)
			VALUES ('delete', OLD.rowid, OLD.content, OLD.id, OLD.user_id, OLD.session_key);
			INSERT INTO transcript_fts(rowid, content, id, user_id, session_key)
			VALUES (NEW.rowid, NEW.content, NEW.id, NEW.user_id, NEW.session_key);
		END
	`); err != nil {
		return fmt.Errorf("create update trigger: %w", err)
	}

	L_debug("transcript: schema ready")
	return nil
}
