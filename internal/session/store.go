// Package session provides session storage and management.
package session

import (
	"context"
	"time"
)

// Store is the interface for session storage backends.
// Implementations: SQLiteStore (primary), JSONLStore (read-only, OpenClaw compat)
type Store interface {
	// Session operations
	GetSession(ctx context.Context, key string) (*StoredSession, error)
	CreateSession(ctx context.Context, session *StoredSession) error
	UpdateSession(ctx context.Context, session *StoredSession) error
	ListSessions(ctx context.Context) ([]StoredSessionInfo, error)

	// Message operations
	AppendMessage(ctx context.Context, sessionKey string, msg *StoredMessage) error
	GetMessages(ctx context.Context, sessionKey string, opts MessageQueryOpts) ([]StoredMessage, error)
	GetMessageCount(ctx context.Context, sessionKey string) (int, error)

	// Checkpoint operations
	AppendCheckpoint(ctx context.Context, sessionKey string, cp *StoredCheckpoint) error
	GetLatestCheckpoint(ctx context.Context, sessionKey string) (*StoredCheckpoint, error)
	GetCheckpoints(ctx context.Context, sessionKey string) ([]StoredCheckpoint, error)

	// Compaction operations
	AppendCompaction(ctx context.Context, sessionKey string, comp *StoredCompaction) error
	GetCompactions(ctx context.Context, sessionKey string) ([]StoredCompaction, error)

	// Compaction retry operations
	GetPendingSummaryRetry(ctx context.Context) (*StoredCompaction, error)
	UpdateCompactionSummary(ctx context.Context, compactionID string, summary string) error
	GetMessagesInRange(ctx context.Context, sessionKey string, startAfterID, endBeforeID string) ([]StoredMessage, error)
	GetPreviousCompaction(ctx context.Context, sessionKey string, beforeTimestamp time.Time) (*StoredCompaction, error)

	// Cleanup operations
	DeleteOrphanedToolMessages(ctx context.Context, sessionKey string) (int, error) // Delete tool_use/tool_result with no matching pair

	// Lifecycle
	Close() error
	Migrate() error // Run schema migrations
}

// MessageQueryOpts controls message retrieval
type MessageQueryOpts struct {
	AfterID    string    // Get messages after this ID
	AfterTime  time.Time // Get messages after this timestamp
	Limit      int       // Max messages to return (0 = no limit)
	RolesOnly  []string  // Filter by roles (empty = all)
	IncludeRaw bool      // Include raw JSON in response
}

// StoredSessionInfo is a lightweight session summary for listing (Store-specific)
type StoredSessionInfo struct {
	Key             string
	ID              string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	MessageCount    int
	CompactionCount int
	TotalTokens     int
}

// StoredSession represents a session in storage
type StoredSession struct {
	Key       string
	ID        string // UUID
	CreatedAt time.Time
	UpdatedAt time.Time

	// Metadata
	Model           string
	ThinkingLevel   string
	CompactionCount int
	TotalTokens     int
	MaxTokens       int

	// Flush state
	FlushedThresholds map[int]bool
	FlushActioned     bool
}

// StoredMessage represents a message in storage
type StoredMessage struct {
	ID         string
	SessionKey string
	ParentID   string
	Timestamp  time.Time

	// Core fields (explicit, not JSON)
	Role    string // "user", "assistant", "system", "tool_use", "tool_result"
	Content string // Text content

	// For tool interactions (nullable)
	ToolCallID   string // tool_use ID or tool_result's referenced ID
	ToolName     string // Tool name (for tool_use)
	ToolInput    []byte // JSON input (for tool_use)
	ToolResult   string // Result text (for tool_result)
	ToolIsError  bool   // Whether tool result is an error

	// Reasoning/thinking content (Kimi, Deepseek, Claude, etc.)
	Thinking string

	// Source metadata
	Source    string // "telegram", "tui", "api", etc.
	ChannelID string // Channel-specific ID (e.g., telegram message ID)
	UserID    string // User who sent the message

	// Supervision metadata (for guidance/ghostwriting interventions)
	Supervisor       string // Username/ID of supervisor who intervened (empty if none)
	InterventionType string // "guidance" or "ghostwrite" (empty if none)

	// Token tracking
	InputTokens  int
	OutputTokens int

	// Raw JSON for OpenClaw compat (optional)
	RawJSON []byte
}

// StoredCheckpoint represents a checkpoint in storage
type StoredCheckpoint struct {
	ID         string
	SessionKey string
	ParentID   string
	Timestamp  time.Time

	// Checkpoint data (explicit fields)
	Summary                 string
	TokensAtCheckpoint      int
	MessageCountAtCheckpoint int
	
	// Structured data (JSON for flexibility)
	Topics        []string // Main topics discussed
	KeyDecisions  []string // Important decisions made
	OpenQuestions []string // Unresolved questions
	
	// Generation metadata
	GeneratedBy string // Model used to generate
}

// StoredCompaction represents a compaction event in storage
type StoredCompaction struct {
	ID         string
	SessionKey string
	ParentID   string
	Timestamp  time.Time

	// Compaction data
	Summary           string
	FirstKeptEntryID  string
	TokensBefore      int
	TokensAfter       int
	MessagesRemoved   int
	FromCheckpoint    bool   // Was summary from a checkpoint?
	CheckpointID      string // If from checkpoint, which one
	NeedsSummaryRetry bool   // True if emergency truncation, needs LLM retry
}

// StoreConfig configures the storage backend
type StoreConfig struct {
	Type string // "sqlite" or "jsonl"
	Path string // Database file path or sessions directory

	// SQLite specific
	WALMode     bool // Enable WAL mode (default: true)
	BusyTimeout int  // Busy timeout in ms (default: 5000)
}

// NewStore creates a storage backend based on config
func NewStore(cfg StoreConfig) (Store, error) {
	switch cfg.Type {
	case "sqlite":
		return NewSQLiteStore(cfg)
	case "jsonl":
		return NewJSONLStore(cfg)
	default:
		return NewSQLiteStore(cfg) // Default to SQLite
	}
}
