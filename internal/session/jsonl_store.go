package session

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// JSONLStore implements Store using JSONL files (OpenClaw-compatible format).
// This wraps the existing JSONLReader and JSONLWriter for full read/write support.
type JSONLStore struct {
	reader *JSONLReader
	writer *JSONLWriter
	config StoreConfig
}

// NewJSONLStore creates a new JSONL store
func NewJSONLStore(cfg StoreConfig) (*JSONLStore, error) {
	store := &JSONLStore{
		reader: NewJSONLReader(cfg.Path),
		writer: NewJSONLWriter(cfg.Path),
		config: cfg,
	}

	// Ensure sessions directory exists
	if err := store.writer.EnsureSessionsDir(); err != nil {
		return nil, fmt.Errorf("failed to create sessions directory: %w", err)
	}

	L_info("jsonl_store: opened", "path", cfg.Path)
	return store, nil
}

// GetSession retrieves a session by key
func (s *JSONLStore) GetSession(ctx context.Context, key string) (*StoredSession, error) {
	sess, _, err := s.reader.LoadSession(key)
	if err != nil {
		return nil, err
	}

	return &StoredSession{
		Key:               sess.Key,
		ID:                sess.ID,
		CreatedAt:         sess.CreatedAt,
		UpdatedAt:         sess.UpdatedAt,
		CompactionCount:   sess.CompactionCount,
		TotalTokens:       sess.TotalTokens,
		MaxTokens:         sess.MaxTokens,
		FlushedThresholds: sess.FlushedThresholds,
		FlushActioned:     sess.FlushActioned,
	}, nil
}

// CreateSession creates a new session
func (s *JSONLStore) CreateSession(ctx context.Context, sess *StoredSession) error {
	// Create session file
	sessionFile, err := s.writer.CreateSessionFile(sess.ID, "")
	if err != nil {
		return fmt.Errorf("failed to create session file: %w", err)
	}

	// Create index entry
	entry := &SessionIndexEntry{
		SessionID:       sess.ID,
		SessionFile:     sessionFile,
		UpdatedAt:       time.Now().UnixMilli(),
		CompactionCount: sess.CompactionCount,
		TotalTokens:     sess.TotalTokens,
	}

	if err := s.writer.UpdateIndex(sess.Key, entry); err != nil {
		return fmt.Errorf("failed to update index: %w", err)
	}

	L_debug("jsonl_store: session created", "key", sess.Key, "id", sess.ID)
	return nil
}

// UpdateSession updates an existing session's metadata in the index
func (s *JSONLStore) UpdateSession(ctx context.Context, sess *StoredSession) error {
	entry, err := s.reader.GetSessionEntry(sess.Key)
	if err != nil {
		return err
	}

	// Update entry fields
	entry.UpdatedAt = time.Now().UnixMilli()
	entry.CompactionCount = sess.CompactionCount
	entry.TotalTokens = sess.TotalTokens

	if err := s.writer.UpdateIndex(sess.Key, entry); err != nil {
		return fmt.Errorf("failed to update index: %w", err)
	}

	return nil
}

// ListSessions returns all sessions from the index
func (s *JSONLStore) ListSessions(ctx context.Context) ([]StoredSessionInfo, error) {
	index, err := s.reader.ReadIndex()
	if err != nil {
		return nil, err
	}

	var sessions []StoredSessionInfo
	for key, entry := range index {
		sessions = append(sessions, StoredSessionInfo{
			Key:             key,
			ID:              entry.SessionID,
			UpdatedAt:       time.UnixMilli(entry.UpdatedAt),
			CompactionCount: entry.CompactionCount,
			TotalTokens:     entry.TotalTokens,
		})
	}

	return sessions, nil
}

// AppendMessage appends a message to a session
func (s *JSONLStore) AppendMessage(ctx context.Context, sessionKey string, msg *StoredMessage) error {
	entry, err := s.reader.GetSessionEntry(sessionKey)
	if err != nil {
		return err
	}

	// Convert StoredMessage to MessageData
	content := buildMessageContent(msg)

	msgData := &MessageData{
		Role:    msg.Role,
		Content: content,
	}

	// Get parent ID
	var parentID *string
	if msg.ParentID != "" {
		parentID = &msg.ParentID
	}

	// Write the record
	record, err := s.writer.WriteMessageRecord(entry.SessionFile, parentID, msgData)
	if err != nil {
		return err
	}

	// Update the message ID to match what was written
	msg.ID = record.ID

	L_debug("jsonl_store: message appended", "session", sessionKey, "id", msg.ID, "role", msg.Role)
	return nil
}

// buildMessageContent converts StoredMessage fields to OpenClaw content format
func buildMessageContent(msg *StoredMessage) []MessageContent {
	// For simple text messages
	if msg.ToolCallID == "" && msg.ToolName == "" && msg.ToolResult == "" {
		return []MessageContent{
			{
				Type: "text",
				Text: msg.Content,
			},
		}
	}

	// For tool_use messages (toolCall type in OpenClaw format)
	if msg.Role == "tool_use" || (msg.ToolName != "" && msg.ToolInput != nil) {
		return []MessageContent{
			{
				Type:      "toolCall",
				ID:        msg.ToolCallID,
				Name:      msg.ToolName,
				Arguments: msg.ToolInput, // Already json.RawMessage
			},
		}
	}

	// For tool_result messages - use text type with the result
	if msg.Role == "tool_result" || msg.ToolResult != "" {
		return []MessageContent{
			{
				Type: "text",
				Text: msg.ToolResult,
			},
		}
	}

	// Default to text content
	return []MessageContent{
		{
			Type: "text",
			Text: msg.Content,
		},
	}
}

// GetMessages retrieves messages for a session
func (s *JSONLStore) GetMessages(ctx context.Context, sessionKey string, opts MessageQueryOpts) ([]StoredMessage, error) {
	_, records, err := s.reader.LoadSession(sessionKey)
	if err != nil {
		return nil, err
	}

	var messages []StoredMessage
	for _, rec := range records {
		msgRec, ok := rec.(*MessageRecord)
		if !ok {
			continue
		}

		// Apply role filter
		if len(opts.RolesOnly) > 0 {
			found := false
			for _, r := range opts.RolesOnly {
				if msgRec.Message.Role == r {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Apply time filter
		if !opts.AfterTime.IsZero() && msgRec.Timestamp.Before(opts.AfterTime) {
			continue
		}

		msg := StoredMessage{
			ID:        msgRec.ID,
			Timestamp: msgRec.Timestamp,
			Role:      msgRec.Message.Role,
			Content:   ExtractTextContent(msgRec.Message.Content),
		}

		if msgRec.ParentID != nil {
			msg.ParentID = *msgRec.ParentID
		}

		// Extract tool information
		toolCalls := ExtractToolCalls(msgRec.Message.Content)
		if len(toolCalls) > 0 {
			msg.ToolCallID = toolCalls[0].ID
			msg.ToolName = toolCalls[0].Name
			msg.ToolInput = toolCalls[0].Arguments // Already json.RawMessage
		}

		// Include raw JSON if requested
		if opts.IncludeRaw {
			if raw, err := json.Marshal(msgRec); err == nil {
				msg.RawJSON = raw
			}
		}

		messages = append(messages, msg)

		if opts.Limit > 0 && len(messages) >= opts.Limit {
			break
		}
	}

	return messages, nil
}

// GetMessageCount returns the number of messages
func (s *JSONLStore) GetMessageCount(ctx context.Context, sessionKey string) (int, error) {
	_, records, err := s.reader.LoadSession(sessionKey)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, rec := range records {
		if _, ok := rec.(*MessageRecord); ok {
			count++
		}
	}

	return count, nil
}

// AppendCheckpoint appends a checkpoint to a session
func (s *JSONLStore) AppendCheckpoint(ctx context.Context, sessionKey string, cp *StoredCheckpoint) error {
	entry, err := s.reader.GetSessionEntry(sessionKey)
	if err != nil {
		return err
	}

	// Convert to CheckpointData
	cpData := &CheckpointData{
		Summary:                  cp.Summary,
		TokensAtCheckpoint:       cp.TokensAtCheckpoint,
		MessageCountAtCheckpoint: cp.MessageCountAtCheckpoint,
		Topics:                   cp.Topics,
		KeyDecisions:             cp.KeyDecisions,
		OpenQuestions:            cp.OpenQuestions,
	}

	var parentID *string
	if cp.ParentID != "" {
		parentID = &cp.ParentID
	}

	record, err := s.writer.WriteCheckpointRecord(entry.SessionFile, parentID, cpData)
	if err != nil {
		return err
	}

	cp.ID = record.ID
	L_debug("jsonl_store: checkpoint appended", "session", sessionKey, "id", cp.ID)
	return nil
}

// GetLatestCheckpoint returns the most recent checkpoint
func (s *JSONLStore) GetLatestCheckpoint(ctx context.Context, sessionKey string) (*StoredCheckpoint, error) {
	_, records, err := s.reader.LoadSession(sessionKey)
	if err != nil {
		return nil, err
	}

	var latest *StoredCheckpoint
	for _, rec := range records {
		cpRec, ok := rec.(*CheckpointRecord)
		if !ok {
			continue
		}

		latest = convertCheckpointRecord(cpRec)
	}

	return latest, nil
}

// GetCheckpoints returns all checkpoints
func (s *JSONLStore) GetCheckpoints(ctx context.Context, sessionKey string) ([]StoredCheckpoint, error) {
	_, records, err := s.reader.LoadSession(sessionKey)
	if err != nil {
		return nil, err
	}

	var checkpoints []StoredCheckpoint
	for _, rec := range records {
		cpRec, ok := rec.(*CheckpointRecord)
		if !ok {
			continue
		}

		checkpoints = append(checkpoints, *convertCheckpointRecord(cpRec))
	}

	return checkpoints, nil
}

func convertCheckpointRecord(cpRec *CheckpointRecord) *StoredCheckpoint {
	cp := &StoredCheckpoint{
		ID:                       cpRec.ID,
		Timestamp:                cpRec.Timestamp,
		Summary:                  cpRec.Checkpoint.Summary,
		TokensAtCheckpoint:       cpRec.Checkpoint.TokensAtCheckpoint,
		MessageCountAtCheckpoint: cpRec.Checkpoint.MessageCountAtCheckpoint,
		Topics:                   cpRec.Checkpoint.Topics,
		KeyDecisions:             cpRec.Checkpoint.KeyDecisions,
		OpenQuestions:            cpRec.Checkpoint.OpenQuestions,
	}
	if cpRec.ParentID != nil {
		cp.ParentID = *cpRec.ParentID
	}
	return cp
}

// AppendCompaction appends a compaction record
func (s *JSONLStore) AppendCompaction(ctx context.Context, sessionKey string, comp *StoredCompaction) error {
	entry, err := s.reader.GetSessionEntry(sessionKey)
	if err != nil {
		return err
	}

	var parentID *string
	if comp.ParentID != "" {
		parentID = &comp.ParentID
	}

	compRecord := &CompactionRecord{
		Summary:          comp.Summary,
		FirstKeptEntryID: comp.FirstKeptEntryID,
		TokensBefore:     comp.TokensBefore,
	}

	if err := s.writer.WriteCompactionRecord(entry.SessionFile, parentID, compRecord); err != nil {
		return err
	}

	// Update index with new compaction count
	entry.CompactionCount++
	if err := s.writer.UpdateIndex(sessionKey, entry); err != nil {
		L_warn("jsonl_store: failed to update compaction count", "error", err)
	}

	L_info("jsonl_store: compaction appended", "session", sessionKey)
	return nil
}

// GetCompactions returns all compactions
func (s *JSONLStore) GetCompactions(ctx context.Context, sessionKey string) ([]StoredCompaction, error) {
	_, records, err := s.reader.LoadSession(sessionKey)
	if err != nil {
		return nil, err
	}

	var compactions []StoredCompaction
	for _, rec := range records {
		compRec, ok := rec.(*CompactionRecord)
		if !ok {
			continue
		}

		comp := StoredCompaction{
			ID:               compRec.ID,
			Timestamp:        compRec.Timestamp,
			Summary:          compRec.Summary,
			FirstKeptEntryID: compRec.FirstKeptEntryID,
			TokensBefore:     compRec.TokensBefore,
		}
		if compRec.ParentID != nil {
			comp.ParentID = *compRec.ParentID
		}

		compactions = append(compactions, comp)
	}

	return compactions, nil
}

// GetPendingSummaryRetry is not supported by JSONL store
func (s *JSONLStore) GetPendingSummaryRetry(ctx context.Context) (*StoredCompaction, error) {
	return nil, fmt.Errorf("GetPendingSummaryRetry not supported by JSONL store")
}

// UpdateCompactionSummary is not supported by JSONL store
func (s *JSONLStore) UpdateCompactionSummary(ctx context.Context, compactionID string, summary string) error {
	return fmt.Errorf("UpdateCompactionSummary not supported by JSONL store")
}

// GetMessagesInRange is not supported by JSONL store
func (s *JSONLStore) GetMessagesInRange(ctx context.Context, sessionKey string, startAfterID, endBeforeID string) ([]StoredMessage, error) {
	return nil, fmt.Errorf("GetMessagesInRange not supported by JSONL store")
}

// GetPreviousCompaction is not supported by JSONL store
func (s *JSONLStore) GetPreviousCompaction(ctx context.Context, sessionKey string, beforeTimestamp time.Time) (*StoredCompaction, error) {
	return nil, fmt.Errorf("GetPreviousCompaction not supported by JSONL store")
}

// DeleteOrphanedToolMessages is not supported by JSONL store
func (s *JSONLStore) DeleteOrphanedToolMessages(ctx context.Context, sessionKey string) (int, error) {
	return 0, fmt.Errorf("DeleteOrphanedToolMessages not supported by JSONL store")
}

// Close is a no-op for JSONL store (no persistent connections)
func (s *JSONLStore) Close() error {
	L_debug("jsonl_store: closed")
	return nil
}

// Migrate is a no-op for JSONL store (no schema to migrate)
func (s *JSONLStore) Migrate() error {
	return nil
}

// GetProviderState is not supported by JSONL store
func (s *JSONLStore) GetProviderState(ctx context.Context, sessionKey, providerKey string) (map[string]any, error) {
	return nil, nil // Return nil state, no error (effectively no-op)
}

// SetProviderState is not supported by JSONL store
func (s *JSONLStore) SetProviderState(ctx context.Context, sessionKey, providerKey string, state map[string]any) error {
	return nil // No-op
}

// DeleteProviderStates is not supported by JSONL store
func (s *JSONLStore) DeleteProviderStates(ctx context.Context, sessionKey string) error {
	return nil // No-op
}
