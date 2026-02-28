package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

var (
	ErrSessionNotFound = errors.New("session not found")
	ErrInvalidIndex    = errors.New("invalid session index")
)

// JSONLReader reads OpenClaw-compatible JSONL session files
type JSONLReader struct {
	sessionsDir string
	mu          sync.RWMutex
}

// NewJSONLReader creates a new JSONL reader
func NewJSONLReader(sessionsDir string) *JSONLReader {
	return &JSONLReader{
		sessionsDir: sessionsDir,
	}
}

// ReadIndex reads and parses the sessions.json index file
func (r *JSONLReader) ReadIndex() (SessionIndex, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	indexPath := filepath.Join(r.sessionsDir, "sessions.json")
	L_trace("jsonl: reading session index", "path", indexPath)

	info, err := os.Stat(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			L_debug("jsonl: session index not found, starting fresh", "path", indexPath)
			return make(SessionIndex), nil
		}
		return nil, fmt.Errorf("failed to stat session index: %w", err)
	}

	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read session index: %w", err)
	}

	var index SessionIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("failed to parse session index: %w", err)
	}

	L_debug("jsonl: session index loaded",
		"entries", len(index),
		"size", len(data),
		"modified", info.ModTime().Format(time.RFC3339))
	return index, nil
}

// GetSessionEntry returns the index entry for a session key
func (r *JSONLReader) GetSessionEntry(key string) (*SessionIndexEntry, error) {
	index, err := r.ReadIndex()
	if err != nil {
		return nil, err
	}

	entry, ok := index[key]
	if !ok {
		return nil, ErrSessionNotFound
	}

	// Log file modification time for debugging
	if info, err := os.Stat(entry.SessionFile); err == nil {
		L_trace("jsonl: session file stats",
			"key", key,
			"file", filepath.Base(entry.SessionFile),
			"size", info.Size(),
			"lastModified", info.ModTime().Format(time.RFC3339))
	}

	return entry, nil
}

// ParseJSONLFile parses a JSONL session file into records
func (r *JSONLReader) ParseJSONLFile(filePath string) ([]Record, error) {
	L_trace("jsonl: opening session file", "path", filePath)

	info, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat session file: %w", err)
	}

	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open session file: %w", err)
	}
	defer file.Close()

	var records []Record
	scanner := bufio.NewScanner(file)
	// Increase buffer size for large lines (tool results can be huge)
	const maxLineSize = 10 * 1024 * 1024 // 10MB
	buf := make([]byte, maxLineSize)
	scanner.Buffer(buf, maxLineSize)

	// Track record types for logging
	typeCounts := make(map[RecordType]int)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		record, err := ParseRecord(line)
		if err != nil {
			L_warn("jsonl: failed to parse record", "line", lineNum, "error", err)
			continue
		}
		records = append(records, record)
		typeCounts[record.GetType()]++
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading session file: %w", err)
	}

	L_debug("jsonl: parsed session file",
		"path", filepath.Base(filePath),
		"size", info.Size(),
		"modified", info.ModTime().Format(time.RFC3339),
		"records", len(records),
		"messages", typeCounts[RecordTypeMessage],
		"compactions", typeCounts[RecordTypeCompaction],
		"checkpoints", typeCounts[RecordTypeCheckpoint])

	return records, nil
}

// LoadSession loads a session by key from the index
func (r *JSONLReader) LoadSession(key string) (*Session, []Record, error) {
	L_debug("jsonl: loading session", "key", key)

	entry, err := r.GetSessionEntry(key)
	if err != nil {
		if err == ErrSessionNotFound {
			L_debug("jsonl: session not found in index", "key", key)
		}
		return nil, nil, err
	}

	L_trace("jsonl: found session entry",
		"key", key,
		"sessionId", entry.SessionID,
		"sessionFile", entry.SessionFile,
		"compactionCount", entry.CompactionCount)

	records, err := r.ParseJSONLFile(entry.SessionFile)
	if err != nil {
		return nil, nil, err
	}

	// Build session from records
	sess, err := BuildSessionFromRecords(entry.SessionID, records)
	if err != nil {
		return nil, nil, err
	}

	L_info("jsonl: session loaded",
		"key", key,
		"sessionId", entry.SessionID,
		"messages", len(sess.Messages),
		"compactionCount", entry.CompactionCount,
		"hasCheckpoint", sess.LastCheckpoint != nil)

	return sess, records, nil
}

// JSONLWriter writes OpenClaw-compatible JSONL session files
type JSONLWriter struct {
	sessionsDir string
	mu          sync.Mutex
}

// NewJSONLWriter creates a new JSONL writer
func NewJSONLWriter(sessionsDir string) *JSONLWriter {
	return &JSONLWriter{
		sessionsDir: sessionsDir,
	}
}

// EnsureSessionsDir creates the sessions directory if it doesn't exist
func (w *JSONLWriter) EnsureSessionsDir() error {
	return os.MkdirAll(w.sessionsDir, 0750)
}

// AppendRecord appends a record to a JSONL session file
func (w *JSONLWriter) AppendRecord(sessionFile string, record Record) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("failed to marshal record: %w", err)
	}

	f, err := os.OpenFile(sessionFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("failed to open session file: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(string(data) + "\n"); err != nil {
		return fmt.Errorf("failed to write record: %w", err)
	}

	L_trace("appended record to JSONL", "type", record.GetType(), "id", record.GetID())
	return nil
}

// CreateSessionFile creates a new session JSONL file with header
func (w *JSONLWriter) CreateSessionFile(sessionID, cwd string) (string, error) {
	if err := w.EnsureSessionsDir(); err != nil {
		return "", err
	}

	filename := fmt.Sprintf("%s.jsonl", sessionID)
	filePath := filepath.Join(w.sessionsDir, filename)

	// Create session header record
	header := &SessionRecord{
		BaseRecord: BaseRecord{
			Type:      RecordTypeSession,
			ID:        sessionID,
			ParentID:  nil,
			Timestamp: time.Now(),
		},
		Version: 3,
		CWD:     cwd,
	}

	if err := w.AppendRecord(filePath, header); err != nil {
		return "", err
	}

	L_debug("created session file", "path", filePath, "sessionId", sessionID)
	return filePath, nil
}

// WriteMessageRecord writes a message record to a session file
func (w *JSONLWriter) WriteMessageRecord(sessionFile string, parentID *string, msg *MessageData) (*MessageRecord, error) {
	record := &MessageRecord{
		BaseRecord: BaseRecord{
			Type:      RecordTypeMessage,
			ID:        GenerateMessageID(),
			ParentID:  parentID,
			Timestamp: time.Now(),
		},
		Message: *msg,
	}

	if err := w.AppendRecord(sessionFile, record); err != nil {
		return nil, err
	}

	return record, nil
}

// WriteCompactionRecord writes a compaction record to a session file
func (w *JSONLWriter) WriteCompactionRecord(sessionFile string, parentID *string, compaction *CompactionRecord) error {
	compaction.BaseRecord = BaseRecord{
		Type:      RecordTypeCompaction,
		ID:        GenerateMessageID(),
		ParentID:  parentID,
		Timestamp: time.Now(),
	}
	return w.AppendRecord(sessionFile, compaction)
}

// WriteCheckpointRecord writes a checkpoint record to a session file (GoClaw-only)
func (w *JSONLWriter) WriteCheckpointRecord(sessionFile string, parentID *string, checkpoint *CheckpointData) (*CheckpointRecord, error) {
	record := &CheckpointRecord{
		BaseRecord: BaseRecord{
			Type:      RecordTypeCheckpoint,
			ID:        GenerateMessageID(),
			ParentID:  parentID,
			Timestamp: time.Now(),
		},
		Checkpoint: *checkpoint,
	}

	if err := w.AppendRecord(sessionFile, record); err != nil {
		return nil, err
	}

	return record, nil
}

// ReadIndex reads the sessions.json index
func (w *JSONLWriter) ReadIndex() (SessionIndex, error) {
	indexPath := filepath.Join(w.sessionsDir, "sessions.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return make(SessionIndex), nil
		}
		return nil, fmt.Errorf("failed to read session index: %w", err)
	}

	var index SessionIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("failed to parse session index: %w", err)
	}

	return index, nil
}

// UpdateIndex updates the sessions.json index file
func (w *JSONLWriter) UpdateIndex(key string, entry *SessionIndexEntry) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	index, err := w.ReadIndex()
	if err != nil {
		return err
	}

	entry.UpdatedAt = time.Now().UnixMilli()
	index[key] = entry

	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal session index: %w", err)
	}

	indexPath := filepath.Join(w.sessionsDir, "sessions.json")
	if err := os.WriteFile(indexPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write session index: %w", err)
	}

	L_debug("updated session index", "key", key, "sessionId", entry.SessionID)
	return nil
}

// GetOrCreateEntry gets an existing entry or creates a new one
func (w *JSONLWriter) GetOrCreateEntry(key, sessionID, cwd string) (*SessionIndexEntry, string, error) {
	index, err := w.ReadIndex()
	if err != nil {
		return nil, "", err
	}

	if entry, ok := index[key]; ok {
		return entry, entry.SessionFile, nil
	}

	// Create new session file
	sessionFile, err := w.CreateSessionFile(sessionID, cwd)
	if err != nil {
		return nil, "", err
	}

	entry := &SessionIndexEntry{
		SessionID:   sessionID,
		SessionFile: sessionFile,
		UpdatedAt:   time.Now().UnixMilli(),
	}

	if err := w.UpdateIndex(key, entry); err != nil {
		return nil, "", err
	}

	return entry, sessionFile, nil
}
