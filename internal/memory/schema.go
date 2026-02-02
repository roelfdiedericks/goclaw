package memory

import (
	"database/sql"
	"fmt"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

const schemaVersion = 1

// initSchema creates the memory search tables and indexes
func initSchema(db *sql.DB) error {
	L_debug("memory: initializing schema", "version", schemaVersion)

	// Enable WAL mode for better concurrency
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		L_warn("memory: failed to enable WAL mode", "error", err)
	}

	// Set busy timeout
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		L_warn("memory: failed to set busy timeout", "error", err)
	}

	// Create schema version table
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS memory_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("create memory_meta table: %w", err)
	}

	// Check current schema version
	var currentVersion int
	err := db.QueryRow("SELECT value FROM memory_meta WHERE key = 'schema_version'").Scan(&currentVersion)
	if err == sql.ErrNoRows {
		currentVersion = 0
	} else if err != nil {
		return fmt.Errorf("check schema version: %w", err)
	}

	if currentVersion < schemaVersion {
		if err := migrateSchema(db, currentVersion); err != nil {
			return fmt.Errorf("migrate schema: %w", err)
		}
	}

	L_debug("memory: schema ready", "version", schemaVersion)
	return nil
}

// migrateSchema runs migrations from the current version to the target version
func migrateSchema(db *sql.DB, fromVersion int) error {
	L_info("memory: migrating schema", "from", fromVersion, "to", schemaVersion)

	// Begin transaction for migration
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Migration from v0 (fresh install) to v1
	if fromVersion < 1 {
		if err := migrateV1(tx); err != nil {
			return fmt.Errorf("migrate to v1: %w", err)
		}
	}

	// Update schema version
	if _, err := tx.Exec(`
		INSERT INTO memory_meta (key, value) VALUES ('schema_version', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, schemaVersion); err != nil {
		return fmt.Errorf("update schema version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}

	return nil
}

// migrateV1 creates the initial schema
func migrateV1(tx *sql.Tx) error {
	L_debug("memory: creating v1 schema")

	// Files table - tracks indexed files
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS memory_files (
			path TEXT PRIMARY KEY,
			hash TEXT NOT NULL,
			mtime_ms INTEGER NOT NULL,
			size INTEGER NOT NULL,
			indexed_at INTEGER NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("create memory_files table: %w", err)
	}

	// Chunks table - stores text chunks with optional embeddings
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS memory_chunks (
			id TEXT PRIMARY KEY,
			path TEXT NOT NULL,
			start_line INTEGER NOT NULL,
			end_line INTEGER NOT NULL,
			hash TEXT NOT NULL,
			text TEXT NOT NULL,
			embedding BLOB,
			embedding_model TEXT,
			updated_at INTEGER NOT NULL,
			FOREIGN KEY (path) REFERENCES memory_files(path) ON DELETE CASCADE
		)
	`); err != nil {
		return fmt.Errorf("create memory_chunks table: %w", err)
	}

	// Create indexes for chunks
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_chunks_path ON memory_chunks(path)`); err != nil {
		return fmt.Errorf("create idx_chunks_path: %w", err)
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_chunks_hash ON memory_chunks(hash)`); err != nil {
		return fmt.Errorf("create idx_chunks_hash: %w", err)
	}

	// FTS5 virtual table for full-text keyword search
	// Note: FTS5 is built into most SQLite distributions including Go's mattn/go-sqlite3
	if _, err := tx.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
			text,
			id UNINDEXED,
			path UNINDEXED,
			start_line UNINDEXED,
			end_line UNINDEXED,
			content='memory_chunks',
			content_rowid='rowid'
		)
	`); err != nil {
		return fmt.Errorf("create memory_fts table: %w", err)
	}

	// Triggers to keep FTS5 in sync with chunks table
	if _, err := tx.Exec(`
		CREATE TRIGGER IF NOT EXISTS memory_chunks_ai AFTER INSERT ON memory_chunks BEGIN
			INSERT INTO memory_fts(rowid, text, id, path, start_line, end_line)
			VALUES (NEW.rowid, NEW.text, NEW.id, NEW.path, NEW.start_line, NEW.end_line);
		END
	`); err != nil {
		return fmt.Errorf("create insert trigger: %w", err)
	}

	if _, err := tx.Exec(`
		CREATE TRIGGER IF NOT EXISTS memory_chunks_ad AFTER DELETE ON memory_chunks BEGIN
			INSERT INTO memory_fts(memory_fts, rowid, text, id, path, start_line, end_line)
			VALUES ('delete', OLD.rowid, OLD.text, OLD.id, OLD.path, OLD.start_line, OLD.end_line);
		END
	`); err != nil {
		return fmt.Errorf("create delete trigger: %w", err)
	}

	if _, err := tx.Exec(`
		CREATE TRIGGER IF NOT EXISTS memory_chunks_au AFTER UPDATE ON memory_chunks BEGIN
			INSERT INTO memory_fts(memory_fts, rowid, text, id, path, start_line, end_line)
			VALUES ('delete', OLD.rowid, OLD.text, OLD.id, OLD.path, OLD.start_line, OLD.end_line);
			INSERT INTO memory_fts(rowid, text, id, path, start_line, end_line)
			VALUES (NEW.rowid, NEW.text, NEW.id, NEW.path, NEW.start_line, NEW.end_line);
		END
	`); err != nil {
		return fmt.Errorf("create update trigger: %w", err)
	}

	// Embedding cache table - caches embeddings by content hash to avoid re-computing
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS embedding_cache (
			hash TEXT NOT NULL,
			model TEXT NOT NULL,
			embedding BLOB NOT NULL,
			dims INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (hash, model)
		)
	`); err != nil {
		return fmt.Errorf("create embedding_cache table: %w", err)
	}

	return nil
}

// clearAllData removes all indexed data (for full reindex)
func clearAllData(db *sql.DB) error {
	L_debug("memory: clearing all indexed data")

	if _, err := db.Exec("DELETE FROM memory_chunks"); err != nil {
		return fmt.Errorf("clear chunks: %w", err)
	}
	if _, err := db.Exec("DELETE FROM memory_files"); err != nil {
		return fmt.Errorf("clear files: %w", err)
	}
	// FTS5 is automatically cleared via triggers

	return nil
}
