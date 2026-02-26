package session

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

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
	StoreType string // "jsonl" or "sqlite"
	StorePath string // Path for storage (DB file or sessions dir)

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
	store    Store        // Primary storage backend (SQLite)
	reader   *JSONLReader // For reading OpenClaw sessions (inheritance)
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

	return m, nil
}

// LoadPrimarySession loads the primary session from SQLite, respecting compaction boundaries.
// This is the standard startup path when OpenClaw inheritance is disabled.
func (m *Manager) LoadPrimarySession() error {
	sess := NewSession("goclaw-primary")

	goclawMsgs, latestCompaction := m.loadSQLiteMessages()

	if len(goclawMsgs) > 0 {
		sess.Messages = storedToMessages(goclawMsgs)
	}

	m.applyCompactionContext(sess, latestCompaction)

	sess.Key = PrimarySession

	estimator := GetTokenEstimator()
	sess.TotalTokens = estimator.EstimateSessionTokens(sess)

	m.mu.Lock()
	m.sessions[PrimarySession] = sess
	m.mu.Unlock()

	L_info("session: loaded from SQLite",
		"sessionKey", PrimarySession,
		"messages", len(sess.Messages),
		"totalTokens", sess.TotalTokens,
		"compactionCount", sess.CompactionCount)

	return nil
}

// InheritOpenClawSession loads an OpenClaw session and merges with GoClaw's own history.
// Messages from both sources are merged chronologically by timestamp.
// GoClaw always uses PrimarySession ("primary") for the owner's session.
// OpenClaw messages are also stored in SQLite (with source='openclaw') for transcript indexing.
func (m *Manager) InheritOpenClawSession(sessionsDir, inheritKey string) error {
	L_debug("session: attempting to inherit OpenClaw session",
		"sessionsDir", sessionsDir,
		"inheritKey", inheritKey)

	if m.reader == nil {
		m.reader = NewJSONLReader(sessionsDir)
	}

	// Load OpenClaw session from JSONL
	sess, records, err := m.reader.LoadSession(inheritKey)
	if err != nil {
		if err == ErrSessionNotFound {
			L_info("session: no OpenClaw session to inherit (starting fresh)",
				"inheritKey", inheritKey)
			sess = NewSession("goclaw-primary")
		} else {
			return fmt.Errorf("failed to load session %q: %w", inheritKey, err)
		}
	}

	openclawMsgCount := len(sess.Messages)

	goclawMsgs, latestCompaction := m.loadSQLiteMessages()

	// Store OpenClaw messages in SQLite for transcript indexing
	if m.store != nil && openclawMsgCount > 0 {
		imported := m.importOpenClawMessages(sess.Messages, goclawMsgs)
		if imported > 0 {
			L_info("session: imported OpenClaw messages to SQLite for transcript indexing",
				"imported", imported,
				"total", openclawMsgCount)
		}
	}

	// Merge for in-memory session
	if len(goclawMsgs) > 0 {
		sess.Messages = mergeMessagesByTimestamp(sess.Messages, goclawMsgs)
		L_info("session: merged message histories",
			"openclaw", openclawMsgCount,
			"goclaw", len(goclawMsgs),
			"merged", len(sess.Messages))
	}

	m.applyCompactionContext(sess, latestCompaction)

	// Set up the session for GoClaw use
	sess.Key = PrimarySession
	if records != nil {
		sess.LastRecordID = GetLastRecordID(records)
		sess.CompactionCount = GetCompactionCount(records)
		sess.LastCheckpoint = GetMostRecentCheckpoint(records)
	}

	estimator := GetTokenEstimator()
	sess.TotalTokens = estimator.EstimateSessionTokens(sess)
	L_debug("session: recalculated tokens after merge", "totalTokens", sess.TotalTokens)

	// Set SessionFile to OpenClaw's file path (for watcher to monitor)
	if m.reader != nil {
		if entry, err := m.reader.GetSessionEntry(inheritKey); err == nil {
			sess.SessionFile = entry.SessionFile
			L_debug("session: will watch OpenClaw session file", "file", sess.SessionFile)
		}
	}

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
		// For tool_result, use ToolResult field (Content is empty for tool results)
		content := sm.Content
		if sm.Role == "tool_result" && sm.ToolResult != "" {
			content = sm.ToolResult
		}

		key := makeKey(sm.Timestamp.Unix(), sm.Role, content)
		if seen[key] {
			continue
		}
		seen[key] = true

		msg := Message{
			ID:        sm.ID,
			Role:      sm.Role,
			Content:   content, // Use resolved content (handles tool_result)
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

// importOpenClawMessages stores OpenClaw messages in SQLite for transcript indexing.
// Only imports messages that don't already exist in goclawMsgs (by timestamp+role+content).
// Returns the number of messages imported.
func (m *Manager) importOpenClawMessages(openclawMsgs []Message, goclawMsgs []StoredMessage) int {
	if m.store == nil {
		return 0
	}

	// Build set of existing message keys (timestamp+role+content prefix)
	makeKey := func(ts int64, role, content string) string {
		contentKey := content
		if len(contentKey) > 50 {
			contentKey = contentKey[:50]
		}
		return fmt.Sprintf("%d:%s:%s", ts, role, contentKey)
	}

	existing := make(map[string]bool)
	for _, sm := range goclawMsgs {
		key := makeKey(sm.Timestamp.Unix(), sm.Role, sm.Content)
		existing[key] = true
	}

	// Also check for messages already imported (source='openclaw')
	ctx := context.Background()
	existingOpenClaw, _ := m.store.GetMessages(ctx, PrimarySession, MessageQueryOpts{})
	for _, sm := range existingOpenClaw {
		if sm.Source == "openclaw" {
			key := makeKey(sm.Timestamp.Unix(), sm.Role, sm.Content)
			existing[key] = true
		}
	}

	imported := 0
	for _, msg := range openclawMsgs {
		// Skip if already exists
		key := makeKey(msg.Timestamp.Unix(), msg.Role, msg.Content)
		if existing[key] {
			continue
		}

		// Skip system messages and tool interactions for transcript indexing
		// Focus on user and assistant messages which are most useful for search
		if msg.Role != "user" && msg.Role != "assistant" {
			continue
		}

		// Create StoredMessage for SQLite
		stored := &StoredMessage{
			ID:         fmt.Sprintf("oc-%s", msg.ID), // Prefix to avoid ID collision
			SessionKey: PrimarySession,
			Timestamp:  msg.Timestamp,
			Role:       msg.Role,
			Content:    msg.Content,
			Source:     "openclaw",
			ToolCallID: msg.ToolUseID,
			ToolName:   msg.ToolName,
		}
		if msg.ToolInput != nil {
			stored.ToolInput = msg.ToolInput
		}

		if err := m.store.AppendMessage(ctx, PrimarySession, stored); err != nil {
			L_debug("session: failed to import OpenClaw message", "id", msg.ID, "error", err)
			continue
		}

		existing[key] = true // Mark as imported to avoid duplicates in same batch
		imported++
	}

	return imported
}

// loadSQLiteMessages loads GoClaw messages from SQLite, respecting compaction boundaries.
func (m *Manager) loadSQLiteMessages() ([]StoredMessage, *StoredCompaction) {
	if m.store == nil {
		return nil, nil
	}

	ctx := context.Background()

	latestCompaction, err := m.store.GetLatestCompaction(ctx, PrimarySession)
	if err != nil {
		L_warn("session: failed to check compaction boundary", "error", err)
	}

	opts := MessageQueryOpts{}
	if latestCompaction != nil && latestCompaction.FirstKeptEntryID != "" {
		opts.SinceID = latestCompaction.FirstKeptEntryID
		L_info("session: applying compaction boundary",
			"firstKeptEntryID", latestCompaction.FirstKeptEntryID,
			"compactionTime", latestCompaction.Timestamp,
			"summaryLen", len(latestCompaction.Summary))
	} else {
		L_debug("session: no compaction boundary, loading all messages")
	}

	msgs, err := m.store.GetMessages(ctx, PrimarySession, opts)
	if err != nil {
		L_warn("session: failed to load GoClaw messages from SQLite", "error", err)
		return nil, latestCompaction
	}
	if len(msgs) > 0 {
		L_debug("session: loaded GoClaw messages from SQLite", "count", len(msgs))
	}

	return msgs, latestCompaction
}

// applyCompactionContext prepends the compaction summary and sets compaction metadata on the session.
func (m *Manager) applyCompactionContext(sess *Session, comp *StoredCompaction) {
	if comp == nil {
		return
	}

	if comp.Summary != "" {
		summaryMsg := Message{
			ID:        "compaction-summary",
			Role:      "user",
			Content:   fmt.Sprintf("[Previous context summary]\n%s", comp.Summary),
			Source:    "system",
			Timestamp: comp.Timestamp,
		}
		sess.Messages = append([]Message{summaryMsg}, sess.Messages...)
		L_info("session: prepended compaction summary",
			"summaryLen", len(comp.Summary),
			"totalMessages", len(sess.Messages))
	}

	compID := comp.ID
	sess.LastRecordID = &compID
	if m.store != nil {
		if compactions, err := m.store.GetCompactions(context.Background(), PrimarySession); err == nil {
			sess.CompactionCount = len(compactions)
		}
	}
}

// storedToMessages converts StoredMessage slice to Message slice.
func storedToMessages(stored []StoredMessage) []Message {
	msgs := make([]Message, len(stored))
	for i, sm := range stored {
		msgs[i] = Message{
			ID:        sm.ID,
			Role:      sm.Role,
			Content:   sm.Content,
			Source:    sm.Source,
			Timestamp: sm.Timestamp,
			ToolUseID: sm.ToolCallID,
			ToolName:  sm.ToolName,
			Thinking:  sm.Thinking,
		}
		if sm.ToolInput != nil {
			msgs[i].ToolInput = sm.ToolInput
		}
		if sm.ToolResult != "" {
			msgs[i].Content = sm.ToolResult
		}
	}
	return msgs
}

// StartWatching begins monitoring the OpenClaw session file for changes.
// The onNewRecords callback is called when new records are detected.
// New messages are also stored in SQLite (source='openclaw') for transcript indexing.
func (m *Manager) StartWatching(ctx context.Context, sessionFile string, onNewRecords func([]Record)) error {
	sess := m.GetPrimary()
	if sess == nil {
		return fmt.Errorf("no primary session to watch")
	}

	callback := func(records []Record) {
		L_debug("session: received new records from OpenClaw", "count", len(records))

		// Store new messages in SQLite for transcript indexing
		m.storeWatchedMessages(records)

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

// storeWatchedMessages stores new OpenClaw messages in SQLite for transcript indexing
func (m *Manager) storeWatchedMessages(records []Record) {
	if m.store == nil {
		return
	}

	ctx := context.Background()
	stored := 0

	for _, r := range records {
		msgRec, ok := r.(*MessageRecord)
		if !ok {
			continue
		}

		// Only store user and assistant messages (skip system/tool for transcript)
		if msgRec.Message.Role != "user" && msgRec.Message.Role != "assistant" {
			continue
		}

		// Extract content from message record
		content := ""
		for _, c := range msgRec.Message.Content {
			if c.Type == "text" && c.Text != "" {
				content = c.Text
				break
			}
		}

		if content == "" {
			continue
		}

		// Create StoredMessage
		msg := &StoredMessage{
			ID:         fmt.Sprintf("oc-%s", msgRec.ID),
			SessionKey: PrimarySession,
			Timestamp:  time.Unix(msgRec.Message.Timestamp/1000, (msgRec.Message.Timestamp%1000)*1000000), // Convert ms to time.Time
			Role:       msgRec.Message.Role,
			Content:    content,
			Source:     "openclaw",
		}

		if err := m.store.AppendMessage(ctx, PrimarySession, msg); err != nil {
			// Likely duplicate - that's fine
			L_trace("session: failed to store watched OpenClaw message", "id", msgRec.ID, "error", err)
			continue
		}
		stored++
	}

	if stored > 0 {
		L_info("session: stored new OpenClaw messages for transcript indexing", "count", stored)
	}
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

// GetFresh returns a fresh session with no prior messages.
// Used for isolated cron jobs that need a clean context.
// The session is stored for persistence but has no conversation history.
func (m *Manager) GetFresh(id string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Always create a new session to ensure it's clean
	s := NewSession(id)
	m.sessions[id] = s
	return s
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

// CleanOrphanedToolMessages deletes tool_use/tool_result messages from store AND memory
func (m *Manager) CleanOrphanedToolMessages(ctx context.Context, sessionKey string) (int, error) {
	totalDeleted := 0

	// Clear from database
	if m.store != nil {
		dbDeleted, err := m.store.DeleteOrphanedToolMessages(ctx, sessionKey)
		if err != nil {
			return 0, err
		}
		totalDeleted += dbDeleted
	}

	// Clear from in-memory session
	m.mu.RLock()
	sess, ok := m.sessions[sessionKey]
	m.mu.RUnlock()
	if ok {
		memDeleted := sess.ClearToolMessages()
		L_info("session: cleared in-memory tool messages", "count", memDeleted, "sessionKey", sessionKey)
		if memDeleted > totalDeleted {
			totalDeleted = memDeleted // Return the higher count
		}
	}

	return totalDeleted, nil
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
