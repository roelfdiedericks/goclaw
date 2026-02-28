package memorygraph

import (
	"database/sql"
	"fmt"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Current schema version
const schemaVersion = 2

// Migration represents a database migration
type Migration struct {
	Version int
	Up      string
}

// Migrations for the memory graph database
var migrations = []Migration{
	{
		Version: 1,
		Up: `
-- Main memories table
CREATE TABLE IF NOT EXISTS memories (
    id INTEGER PRIMARY KEY,
    uuid TEXT NOT NULL UNIQUE,
    content TEXT NOT NULL,
    memory_type TEXT NOT NULL,
    importance REAL NOT NULL DEFAULT 0.5,
    confidence REAL NOT NULL DEFAULT -1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    last_accessed_at TEXT NOT NULL,
    access_count INTEGER NOT NULL DEFAULT 0,
    next_trigger_at TEXT,
    source TEXT,
    source_session TEXT,
    source_message TEXT,
    username TEXT,
    channel TEXT,
    chat_id TEXT,
    forgotten INTEGER NOT NULL DEFAULT 0,
    embedding BLOB,
    embedding_model TEXT
);

-- Indexes on memories
CREATE INDEX IF NOT EXISTS idx_memories_uuid ON memories(uuid);
CREATE INDEX IF NOT EXISTS idx_memories_type ON memories(memory_type);
CREATE INDEX IF NOT EXISTS idx_memories_username ON memories(username);
CREATE INDEX IF NOT EXISTS idx_memories_channel ON memories(channel);
CREATE INDEX IF NOT EXISTS idx_memories_importance ON memories(importance DESC);
CREATE INDEX IF NOT EXISTS idx_memories_forgotten ON memories(forgotten);
CREATE INDEX IF NOT EXISTS idx_memories_next_trigger ON memories(next_trigger_at) WHERE next_trigger_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_memories_created ON memories(created_at DESC);

-- Full-text search virtual table
CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
    content,
    content='memories',
    content_rowid='id'
);

-- Triggers to keep FTS in sync
CREATE TRIGGER IF NOT EXISTS memories_ai AFTER INSERT ON memories BEGIN
    INSERT INTO memories_fts(rowid, content) VALUES (NEW.id, NEW.content);
END;

CREATE TRIGGER IF NOT EXISTS memories_ad AFTER DELETE ON memories BEGIN
    INSERT INTO memories_fts(memories_fts, rowid, content) VALUES('delete', OLD.id, OLD.content);
END;

CREATE TRIGGER IF NOT EXISTS memories_au AFTER UPDATE ON memories BEGIN
    INSERT INTO memories_fts(memories_fts, rowid, content) VALUES('delete', OLD.id, OLD.content);
    INSERT INTO memories_fts(rowid, content) VALUES (NEW.id, NEW.content);
END;

-- Associations table (edges between memories)
CREATE TABLE IF NOT EXISTS associations (
    id TEXT PRIMARY KEY,
    source_uuid TEXT NOT NULL,
    target_uuid TEXT NOT NULL,
    relation_type TEXT NOT NULL,
    weight REAL NOT NULL DEFAULT 1.0,
    directed INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    FOREIGN KEY (source_uuid) REFERENCES memories(uuid) ON DELETE CASCADE,
    FOREIGN KEY (target_uuid) REFERENCES memories(uuid) ON DELETE CASCADE
);

-- Indexes on associations
CREATE INDEX IF NOT EXISTS idx_assoc_source ON associations(source_uuid);
CREATE INDEX IF NOT EXISTS idx_assoc_target ON associations(target_uuid);
CREATE INDEX IF NOT EXISTS idx_assoc_type ON associations(relation_type);
CREATE UNIQUE INDEX IF NOT EXISTS idx_assoc_unique ON associations(source_uuid, target_uuid, relation_type);

-- Routine metadata
CREATE TABLE IF NOT EXISTS routine_metadata (
    memory_uuid TEXT PRIMARY KEY,
    trigger_type TEXT NOT NULL,
    trigger_cron TEXT,
    trigger_event TEXT,
    trigger_condition TEXT,
    action TEXT NOT NULL,
    action_entity TEXT,
    action_extra TEXT,
    autonomy TEXT NOT NULL DEFAULT 'suggest',
    observations INTEGER NOT NULL DEFAULT 0,
    suggestions INTEGER NOT NULL DEFAULT 0,
    acceptances INTEGER NOT NULL DEFAULT 0,
    rejections INTEGER NOT NULL DEFAULT 0,
    auto_runs INTEGER NOT NULL DEFAULT 0,
    last_triggered_at TEXT,
    FOREIGN KEY (memory_uuid) REFERENCES memories(uuid) ON DELETE CASCADE
);

-- Feedback metadata
CREATE TABLE IF NOT EXISTS feedback_metadata (
    memory_uuid TEXT PRIMARY KEY,
    routine_uuid TEXT,
    feedback_type TEXT NOT NULL,
    context_day TEXT,
    context_time TEXT,
    user_note TEXT,
    FOREIGN KEY (memory_uuid) REFERENCES memories(uuid) ON DELETE CASCADE,
    FOREIGN KEY (routine_uuid) REFERENCES memories(uuid) ON DELETE SET NULL
);

-- Anomaly metadata
CREATE TABLE IF NOT EXISTS anomaly_metadata (
    memory_uuid TEXT PRIMARY KEY,
    routine_uuid TEXT,
    expected TEXT NOT NULL,
    actual TEXT NOT NULL,
    severity TEXT NOT NULL DEFAULT 'medium',
    FOREIGN KEY (memory_uuid) REFERENCES memories(uuid) ON DELETE CASCADE,
    FOREIGN KEY (routine_uuid) REFERENCES memories(uuid) ON DELETE SET NULL
);

-- Correlation metadata
CREATE TABLE IF NOT EXISTS correlation_metadata (
    memory_uuid TEXT PRIMARY KEY,
    condition TEXT NOT NULL,
    outcome TEXT NOT NULL,
    strength REAL NOT NULL DEFAULT 0.5,
    observations INTEGER NOT NULL DEFAULT 0,
    last_observed_at TEXT,
    FOREIGN KEY (memory_uuid) REFERENCES memories(uuid) ON DELETE CASCADE
);

-- Prediction metadata
CREATE TABLE IF NOT EXISTS prediction_metadata (
    memory_uuid TEXT PRIMARY KEY,
    routine_uuid TEXT,
    predicted_time TEXT NOT NULL,
    action TEXT NOT NULL,
    outcome TEXT NOT NULL DEFAULT '',
    confidence_at_prediction REAL NOT NULL,
    FOREIGN KEY (memory_uuid) REFERENCES memories(uuid) ON DELETE CASCADE,
    FOREIGN KEY (routine_uuid) REFERENCES memories(uuid) ON DELETE SET NULL
);

-- Schema version tracking
CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY
);

INSERT INTO schema_version (version) VALUES (1);
`,
	},
	{
		Version: 2,
		Up: `
-- Ingestion state tracking for deduplication
CREATE TABLE IF NOT EXISTS ingestion_state (
    source_type TEXT NOT NULL,      -- "markdown" or "transcript"
    source_path TEXT NOT NULL,      -- file path or chunk ID
    content_hash TEXT NOT NULL,     -- SHA256 of content
    ingested_at TEXT NOT NULL,
    memory_count INTEGER NOT NULL,  -- memories created from this source
    PRIMARY KEY (source_type, source_path)
);

CREATE INDEX IF NOT EXISTS idx_ingestion_type ON ingestion_state(source_type);

INSERT OR REPLACE INTO schema_version (version) VALUES (2);
`,
	},
}

// InitSchema initializes the database schema
func InitSchema(db *sql.DB) error {
	var currentVersion int
	err := db.QueryRow("SELECT version FROM schema_version ORDER BY version DESC LIMIT 1").Scan(&currentVersion)
	if err == sql.ErrNoRows {
		currentVersion = 0
	} else if err != nil {
		// Table doesn't exist yet
		currentVersion = 0
	}

	for _, m := range migrations {
		if m.Version > currentVersion {
			L_info("memorygraph: applying migration", "version", m.Version)
			_, err := db.Exec(m.Up)
			if err != nil {
				return fmt.Errorf("migration %d failed: %w", m.Version, err)
			}
			currentVersion = m.Version
		}
	}

	L_info("memorygraph: schema initialized", "version", currentVersion)
	return nil
}
