package session

import (
	"context"
	"fmt"
	"sort"
	"sync"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// PrimarySession is the ONE session key for owner's DMs/TUI
// All primary session operations use this key - no exceptions
const PrimarySession = "primary"

// Default OpenClaw session paths
const (
	DefaultOpenClawSessionsDir = "~/.openclaw/agents/main/sessions"
	DefaultOpenClawSessionKey  = "agent:main:main"
)

// SessionInfo provides summary information about a session
type SessionInfo struct {
	ID           string  `json:"id"`
	Key          string  `json:"key,omitempty"`
	MessageCount int     `json:"messageCount"`
	InputTokens  int     `json:"inputTokens"`
	OutputTokens int     `json:"outputTokens"`
	TotalTokens  int     `json:"totalTokens"`
	MaxTokens    int     `json:"maxTokens"`
	ContextUsage float64 `json:"contextUsage"` // 0.0 to 1.0
	CreatedAt    string  `json:"createdAt"`
	UpdatedAt    string  `json:"updatedAt"`
}

// ManagerConfig holds configuration for the session manager
type ManagerConfig struct {
	// Storage backend
	StoreType   string // "jsonl" or "sqlite"
	StorePath   string // Path for storage (DB file or sessions dir)

	// OpenClaw session inheritance (read-only)
	SessionsDir string // Directory for OpenClaw session files (for watching)
	InheritFrom string // Session key to inherit from (e.g., "agent:main:main")
	WorkingDir  string // Working directory for new sessions

	// Legacy
	EnablePersist bool // Enable persistence (always true when store configured)
}

// Manager maintains all active sessions
type Manager struct {
	sessions map[string]*Session
	store    Store          // Primary storage backend (SQLite or JSONL)
	reader   *JSONLReader   // For reading OpenClaw sessions (inheritance)
	writer   *JSONLWriter   // Legacy: direct JSONL writer (deprecated, use store)
	watcher  *SessionWatcher
	config   *ManagerConfig
	mu       sync.RWMutex
}

// NewManager creates a new session manager
func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
	}
}

// NewManagerWithConfig creates a session manager with persistence config
func NewManagerWithConfig(cfg *ManagerConfig) (*Manager, error) {
	m := &Manager{
		sessions: make(map[string]*Session),
		config:   cfg,
	}

	if cfg == nil {
		return m, nil
	}

	// Create storage backend
	if cfg.StoreType != "" && cfg.StorePath != "" {
		storeCfg := StoreConfig{
			Type:        cfg.StoreType,
			Path:        cfg.StorePath,
			WALMode:     true,
			BusyTimeout: 5000,
		}
		store, err := NewStore(storeCfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create store: %w", err)
		}
		m.store = store
		L_info("session: storage backend initialized", "type", cfg.StoreType, "path", cfg.StorePath)
	}

	// Set up JSONL reader for OpenClaw session inheritance (read-only)
	if cfg.SessionsDir != "" {
		m.reader = NewJSONLReader(cfg.SessionsDir)
		L_debug("session: JSONL reader initialized for OpenClaw inheritance", "dir", cfg.SessionsDir)
	}

	// Legacy: also create writer for backwards compatibility
	if cfg.EnablePersist && cfg.SessionsDir != "" && m.store == nil {
		m.writer = NewJSONLWriter(cfg.SessionsDir)
		L_debug("session: legacy JSONL writer initialized")
	}

	return m, nil
}

// InheritOpenClawSession loads an OpenClaw session and merges with GoClaw's own history.
// Messages from both sources are merged chronologically by timestamp.
// GoClaw always uses PrimarySession ("primary") for the owner's session.
func (m *Manager) InheritOpenClawSession(sessionsDir, inheritKey string) error {
	L_debug("session: attempting to inherit OpenClaw session",
		"sessionsDir", sessionsDir,
		"inheritKey", inheritKey)
	
	if m.reader == nil {
		m.reader = NewJSONLReader(sessionsDir)
	}
	if m.writer == nil {
		m.writer = NewJSONLWriter(sessionsDir)
	}

	// Load OpenClaw session from JSONL
	sess, records, err := m.reader.LoadSession(inheritKey)
	if err != nil {
		if err == ErrSessionNotFound {
			L_info("session: no OpenClaw session to inherit (starting fresh)",
				"inheritKey", inheritKey)
			// Create empty session for GoClaw
			sess = NewSession("goclaw-primary")
		} else {
			return fmt.Errorf("failed to load session %q: %w", inheritKey, err)
		}
	}

	openclawMsgCount := len(sess.Messages)

	// Load GoClaw's own messages from SQLite and merge
	// Note: Runtime writes go to PrimarySession ("primary"), not writeKey
	if m.store != nil {
		ctx := context.Background()
		goclawMsgs, err := m.store.GetMessages(ctx, PrimarySession, MessageQueryOpts{})
		if err != nil {
			L_warn("session: failed to load GoClaw messages from SQLite", "error", err)
		} else if len(goclawMsgs) > 0 {
			L_debug("session: loaded GoClaw messages from SQLite", "count", len(goclawMsgs))
			
			// Merge OpenClaw and GoClaw messages by timestamp
			sess.Messages = mergeMessagesByTimestamp(sess.Messages, goclawMsgs)
			
			L_info("session: merged message histories",
				"openclaw", openclawMsgCount,
				"goclaw", len(goclawMsgs),
				"merged", len(sess.Messages))
		}
	}

	// Set up the session for GoClaw use
	sess.Key = PrimarySession
	if records != nil {
		sess.LastRecordID = GetLastRecordID(records)
		sess.CompactionCount = GetCompactionCount(records)
		sess.LastCheckpoint = GetMostRecentCheckpoint(records)
	}

	// Recalculate total tokens from merged messages (not just OpenClaw records)
	// This ensures accurate token count for compaction decisions
	estimator := GetTokenEstimator()
	sess.TotalTokens = estimator.EstimateSessionTokens(sess)
	L_debug("session: recalculated tokens after merge", "totalTokens", sess.TotalTokens)
	
	// Set up our own session file for writing (separate from OpenClaw's)
	if m.writer != nil {
		sessionID := sess.ID
		if sessionID == "" {
			sessionID = "goclaw-primary"
		}
		_, sessionFile, err := m.writer.GetOrCreateEntry(PrimarySession, sessionID+"-goclaw", sessionsDir)
		if err != nil {
			L_warn("session: failed to create GoClaw session file", "error", err)
		} else {
			sess.SessionFile = sessionFile
			L_debug("session: GoClaw session file created", "file", sessionFile)
		}
	}

	// Store in session map
	m.mu.Lock()
	m.sessions[PrimarySession] = sess
	m.mu.Unlock()

	L_info("session: initialized with merged history",
		"inheritKey", inheritKey,
		"sessionKey", PrimarySession,
		"messages", len(sess.Messages),
		"totalTokens", sess.TotalTokens,
		"compactionCount", sess.CompactionCount,
		"hasCheckpoint", sess.LastCheckpoint != nil)

	return nil
}

// mergeMessagesByTimestamp combines messages from OpenClaw (JSONL) and GoClaw (SQLite)
// into a single chronologically ordered list, deduplicating by timestamp+role+content.
// This handles the case where both sources have the same message with different IDs.
func mergeMessagesByTimestamp(openclawMsgs []Message, goclawMsgs []StoredMessage) []Message {
	// Build deduplication key: timestamp (unix seconds) + role + first 50 chars of content
	makeKey := func(ts int64, role, content string) string {
		contentKey := content
		if len(contentKey) > 50 {
			contentKey = contentKey[:50]
		}
		return fmt.Sprintf("%d:%s:%s", ts, role, contentKey)
	}

	// Track seen messages by dedup key
	seen := make(map[string]bool)

	// Start with OpenClaw messages
	var result []Message
	for _, msg := range openclawMsgs {
		key := makeKey(msg.Timestamp.Unix(), msg.Role, msg.Content)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, msg)
	}

	// Add GoClaw messages, skipping duplicates
	for _, sm := range goclawMsgs {
		key := makeKey(sm.Timestamp.Unix(), sm.Role, sm.Content)
		if seen[key] {
			continue
		}
		seen[key] = true

		msg := Message{
			ID:        sm.ID,
			Role:      sm.Role,
			Content:   sm.Content,
			Source:    sm.Source,
			Timestamp: sm.Timestamp,
			ToolUseID: sm.ToolCallID,
			ToolName:  sm.ToolName,
		}
		if sm.ToolInput != nil {
			msg.ToolInput = sm.ToolInput
		}
		result = append(result, msg)
	}

	// Sort by timestamp (chronological order)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Timestamp.Before(result[j].Timestamp)
	})

	return result
}

// StartWatching begins monitoring the OpenClaw session file for changes.
// The onNewRecords callback is called when new records are detected.
func (m *Manager) StartWatching(ctx context.Context, sessionFile string, onNewRecords func([]Record)) error {
	sess := m.GetPrimary()
	if sess == nil {
		return fmt.Errorf("no primary session to watch")
	}

	callback := func(records []Record) {
		L_debug("session: received new records from OpenClaw", "count", len(records))
		if onNewRecords != nil {
			onNewRecords(records)
		}
	}

	watcher, err := NewSessionWatcher(sessionFile, sess, callback)
	if err != nil {
		return fmt.Errorf("failed to create session watcher: %w", err)
	}

	m.watcher = watcher
	return watcher.Start(ctx)
}

// StopWatching stops monitoring the OpenClaw session file
func (m *Manager) StopWatching() {
	if m.watcher != nil {
		m.watcher.Stop()
		m.watcher = nil
	}
}

// SyncFromOpenClaw forces an immediate sync from the OpenClaw session file
func (m *Manager) SyncFromOpenClaw() {
	if m.watcher != nil {
		m.watcher.ForceSync()
	}
}

// Get returns a session by ID, creating it if it doesn't exist
func (m *Manager) Get(id string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	if s, ok := m.sessions[id]; ok {
		return s
	}

	// Create new session
	s := NewSession(id)
	m.sessions[id] = s
	return s
}

// GetPrimary returns the primary session (shorthand for Get("primary"))
func (m *Manager) GetPrimary() *Session {
	return m.Get(PrimarySession)
}

// GetIfExists returns a session if it exists, nil otherwise
func (m *Manager) GetIfExists(id string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[id]
}

// List returns info about all sessions
func (m *Manager) List() []SessionInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]SessionInfo, 0, len(m.sessions))
	for _, s := range m.sessions {
		s.mu.RLock()
		infos = append(infos, SessionInfo{
			ID:           s.ID,
			MessageCount: len(s.Messages),
			InputTokens:  s.InputTokens,
			OutputTokens: s.OutputTokens,
			CreatedAt:    s.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			UpdatedAt:    s.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
		s.mu.RUnlock()
	}
	return infos
}

// History returns the messages for a specific session
func (m *Manager) History(id string) ([]Message, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.sessions[id]
	if !ok {
		return nil, false
	}
	return s.GetMessages(), true
}

// Delete removes a session
func (m *Manager) Delete(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.sessions[id]; ok {
		delete(m.sessions, id)
		return true
	}
	return false
}

// Reset clears all messages in a session but keeps the session
func (m *Manager) Reset(id string) bool {
	m.mu.RLock()
	s, ok := m.sessions[id]
	m.mu.RUnlock()

	if !ok {
		return false
	}

	s.Clear()
	return true
}

// Count returns the number of active sessions
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// GetStore returns the storage backend (may be nil if not configured)
func (m *Manager) GetStore() Store {
	return m.store
}

// GetWriter returns the legacy JSONL writer (deprecated, use GetStore)
func (m *Manager) GetWriter() *JSONLWriter {
	return m.writer
}

// Close closes the storage backend
func (m *Manager) Close() error {
	m.StopWatching()
	if m.store != nil {
		return m.store.Close()
	}
	return nil
}

// PersistMessage writes a message to the storage backend
func (m *Manager) PersistMessage(ctx context.Context, sessionKey string, msg *StoredMessage) error {
	if m.store == nil {
		return nil // No store configured
	}

	// Ensure session exists in store
	_, err := m.store.GetSession(ctx, sessionKey)
	if err == ErrSessionNotFound {
		// Create session
		sess := m.GetPrimary()
		if sess != nil {
			stored := &StoredSession{
				Key:       sessionKey,
				ID:        sess.ID,
				CreatedAt: sess.CreatedAt,
				UpdatedAt: sess.UpdatedAt,
			}
			if err := m.store.CreateSession(ctx, stored); err != nil {
				L_warn("session: failed to create session in store", "key", sessionKey, "error", err)
			}
		}
	}

	return m.store.AppendMessage(ctx, sessionKey, msg)
}

// PersistCheckpoint writes a checkpoint to the storage backend
func (m *Manager) PersistCheckpoint(ctx context.Context, sessionKey string, cp *StoredCheckpoint) error {
	if m.store == nil {
		return nil
	}
	return m.store.AppendCheckpoint(ctx, sessionKey, cp)
}

// PersistCompaction writes a compaction record to the storage backend
func (m *Manager) PersistCompaction(ctx context.Context, sessionKey string, comp *StoredCompaction) error {
	if m.store == nil {
		return nil
	}
	return m.store.AppendCompaction(ctx, sessionKey, comp)
}
