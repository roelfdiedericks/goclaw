package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Compactor handles session compaction when context limits are reached
type Compactor struct {
	config     *CompactorConfig
	writer     *JSONLWriter
	store      Store
	sessionKey string
	llmClient  CompactionLLMClient
}

// CompactorConfig holds compaction settings
type CompactorConfig struct {
	ReserveTokens    int  // Tokens to reserve before compaction
	PreferCheckpoint bool // Use checkpoint for summary if available
}

// CompactionLLMClient interface for LLM calls during compaction
type CompactionLLMClient interface {
	GenerateSummary(ctx context.Context, messages []Message, maxTokens int) (string, error)
	IsAvailable() bool
}

// CompactionResult contains the result of a compaction operation
type CompactionResult struct {
	Summary             string
	TokensBefore        int
	FirstKeptEntryID    string
	FromCheckpoint      bool
	EmergencyTruncation bool // True if LLM failed and we did simple truncation
	Details             *CompactionDetails
}

// NewCompactor creates a new compactor
func NewCompactor(cfg *CompactorConfig, writer *JSONLWriter) *Compactor {
	return &Compactor{
		config: cfg,
		writer: writer,
	}
}

// SetStore sets the store for compaction persistence
func (c *Compactor) SetStore(store Store, sessionKey string) {
	c.store = store
	c.sessionKey = sessionKey
}

// SetLLMClient sets the LLM client for summary generation
func (c *Compactor) SetLLMClient(client CompactionLLMClient) {
	c.llmClient = client
}

// ShouldCompact determines if compaction is needed
func (c *Compactor) ShouldCompact(sess *Session) bool {
	if c == nil || c.config == nil {
		return false
	}

	maxTokens := sess.GetMaxTokens()
	if maxTokens == 0 {
		return false
	}

	totalTokens := sess.GetTotalTokens()
	threshold := maxTokens - c.config.ReserveTokens

	shouldCompact := totalTokens >= threshold
	if shouldCompact {
		L_info("compaction threshold reached",
			"totalTokens", totalTokens,
			"maxTokens", maxTokens,
			"threshold", threshold,
			"reserve", c.config.ReserveTokens)
	}

	return shouldCompact
}

// Compact performs compaction on a session
func (c *Compactor) Compact(ctx context.Context, sess *Session, sessionFile string) (*CompactionResult, error) {
	if c == nil {
		return nil, fmt.Errorf("compactor not initialized")
	}

	tokensBefore := sess.GetTotalTokens()
	L_info("starting compaction", "tokensBefore", tokensBefore, "messages", len(sess.Messages))

	var summary string
	var fromCheckpoint bool
	var emergencyTruncation bool
	var details *CompactionDetails

	// Try fast path: use recent checkpoint
	if c.config.PreferCheckpoint && sess.LastCheckpoint != nil {
		// Check if checkpoint is recent enough (covered at least 50% of current context)
		checkpointTokens := sess.LastCheckpoint.Checkpoint.TokensAtCheckpoint
		if checkpointTokens >= tokensBefore/2 {
			summary = c.buildSummaryFromCheckpoint(sess.LastCheckpoint)
			fromCheckpoint = true
			L_info("using checkpoint for compaction summary",
				"checkpointTokens", checkpointTokens,
				"tokensBefore", tokensBefore)
		}
	}

	// Slow path: generate summary from current context
	if summary == "" {
		if c.llmClient == nil || !c.llmClient.IsAvailable() {
			// No LLM available - use basic summary and emergency truncation
			summary = c.buildBasicSummary(sess)
			emergencyTruncation = true
			L_warn("no LLM available for compaction summary, using emergency truncation")
		} else {
			var err error
			summary, err = c.llmClient.GenerateSummary(ctx, sess.Messages, 4000)
			if err != nil {
				// Fall back to checkpoint if available
				if sess.LastCheckpoint != nil {
					summary = c.buildSummaryFromCheckpoint(sess.LastCheckpoint)
					fromCheckpoint = true
					L_warn("LLM summary failed, falling back to checkpoint", "error", err)
				} else {
					// Emergency: no checkpoint, no LLM - do aggressive truncation
					summary = c.buildBasicSummary(sess)
					emergencyTruncation = true
					L_warn("LLM summary failed, using emergency truncation (keeping last 20%)", "error", err)
				}
			}
		}
	}

	// Extract file tracking details
	details = c.extractFileDetails(sess)

	// Find the message to keep (most recent user message as anchor)
	// Emergency truncation keeps 20%, normal keeps 10%
	keepPercent := 10
	if emergencyTruncation {
		keepPercent = 20
	}
	firstKeptID := c.findFirstKeptID(sess, keepPercent)

	// Write compaction record using store (preferred) or legacy JSONL writer
	if c.store != nil && c.sessionKey != "" {
		storedComp := &StoredCompaction{
			ID:                GenerateRecordID(),
			Timestamp:         time.Now(),
			Summary:           summary,
			FirstKeptEntryID:  firstKeptID,
			TokensBefore:      tokensBefore,
			NeedsSummaryRetry: emergencyTruncation,
		}
		if parentID := sess.GetLastRecordID(); parentID != nil {
			storedComp.ParentID = *parentID
		}

		if err := c.store.AppendCompaction(ctx, c.sessionKey, storedComp); err != nil {
			return nil, fmt.Errorf("failed to write compaction to store: %w", err)
		}

		sess.SetLastRecordID(storedComp.ID)
	} else if c.writer != nil && sessionFile != "" {
		// Legacy: Write to JSONL
		compactionRecord := &CompactionRecord{
			Summary:          summary,
			FirstKeptEntryID: firstKeptID,
			TokensBefore:     tokensBefore,
			Details:          details,
			FromHook:         false,
			FromCheckpoint:   fromCheckpoint,
		}

		err := c.writer.WriteCompactionRecord(sessionFile, sess.GetLastRecordID(), compactionRecord)
		if err != nil {
			return nil, fmt.Errorf("failed to write compaction record: %w", err)
		}

		sess.SetLastRecordID(compactionRecord.ID)
	}

	// Truncate messages in session
	c.truncateMessages(sess, firstKeptID)

	// Update session metadata
	sess.CompactionCount++
	sess.ResetFlushedThresholds()

	result := &CompactionResult{
		Summary:             summary,
		TokensBefore:        tokensBefore,
		FirstKeptEntryID:    firstKeptID,
		FromCheckpoint:      fromCheckpoint,
		EmergencyTruncation: emergencyTruncation,
		Details:             details,
	}

	L_info("compaction completed",
		"tokensBefore", tokensBefore,
		"messagesAfter", len(sess.Messages),
		"fromCheckpoint", fromCheckpoint,
		"emergencyTruncation", emergencyTruncation,
		"compactionCount", sess.CompactionCount)

	return result, nil
}

// buildSummaryFromCheckpoint builds a summary string from a checkpoint record
func (c *Compactor) buildSummaryFromCheckpoint(cp *CheckpointRecord) string {
	var parts []string

	// Main summary
	if cp.Checkpoint.Summary != "" {
		parts = append(parts, cp.Checkpoint.Summary)
	}

	// Topics
	if len(cp.Checkpoint.Topics) > 0 {
		parts = append(parts, fmt.Sprintf("\nTopics discussed: %s", strings.Join(cp.Checkpoint.Topics, ", ")))
	}

	// Key decisions
	if len(cp.Checkpoint.KeyDecisions) > 0 {
		parts = append(parts, fmt.Sprintf("\nKey decisions:\n- %s", strings.Join(cp.Checkpoint.KeyDecisions, "\n- ")))
	}

	// Open questions
	if len(cp.Checkpoint.OpenQuestions) > 0 {
		parts = append(parts, fmt.Sprintf("\nOpen questions:\n- %s", strings.Join(cp.Checkpoint.OpenQuestions, "\n- ")))
	}

	return strings.Join(parts, "\n")
}

// buildBasicSummary builds a minimal summary when no LLM is available
func (c *Compactor) buildBasicSummary(sess *Session) string {
	msgCount := len(sess.Messages)
	userCount := 0
	for _, msg := range sess.Messages {
		if msg.Role == "user" {
			userCount++
		}
	}

	return fmt.Sprintf("Summary unavailable due to context limits. Older messages were truncated.\n\n"+
		"Session contained %d messages (%d user messages) before compaction at %s.",
		msgCount, userCount, time.Now().Format(time.RFC3339))
}

// extractFileDetails extracts read/modified files from session messages
func (c *Compactor) extractFileDetails(sess *Session) *CompactionDetails {
	readFiles := make(map[string]bool)
	modifiedFiles := make(map[string]bool)

	for _, msg := range sess.Messages {
		// Check for tool uses that modify files
		if msg.ToolName == "write" || msg.ToolName == "edit" || msg.ToolName == "str_replace_editor" {
			if msg.ToolInput != nil {
				var input struct {
					Path string `json:"path"`
				}
				if err := json.Unmarshal(msg.ToolInput, &input); err == nil && input.Path != "" {
					modifiedFiles[input.Path] = true
				}
			}
		}

		// Check for read tool
		if msg.ToolName == "read" {
			if msg.ToolInput != nil {
				var input struct {
					Path string `json:"path"`
				}
				if err := json.Unmarshal(msg.ToolInput, &input); err == nil && input.Path != "" {
					readFiles[input.Path] = true
				}
			}
		}
	}

	// Convert maps to slices
	var readList, modList []string
	for f := range readFiles {
		readList = append(readList, f)
	}
	for f := range modifiedFiles {
		modList = append(modList, f)
	}

	if len(readList) == 0 && len(modList) == 0 {
		return nil
	}

	return &CompactionDetails{
		ReadFiles:     readList,
		ModifiedFiles: modList,
	}
}

// findFirstKeptID finds the ID of the first message to keep after compaction
// keepPercent: percentage of messages to keep (e.g., 10 for 10%, 20 for 20%)
func (c *Compactor) findFirstKeptID(sess *Session, keepPercent int) string {
	if keepPercent <= 0 {
		keepPercent = 10 // Default to 10%
	}
	if keepPercent > 100 {
		keepPercent = 100
	}

	// Keep the most recent messages - find a good anchor point
	// Strategy: keep at least the last N% of messages or 5 messages, whichever is more
	keepCount := (len(sess.Messages) * keepPercent) / 100
	if keepCount < 5 {
		keepCount = 5
	}
	if keepCount > len(sess.Messages) {
		keepCount = len(sess.Messages)
	}

	startIdx := len(sess.Messages) - keepCount
	if startIdx < 0 {
		startIdx = 0
	}

	L_debug("compaction: calculating keep range",
		"totalMessages", len(sess.Messages),
		"keepPercent", keepPercent,
		"keepCount", keepCount,
		"startIdx", startIdx)

	if startIdx < len(sess.Messages) {
		return sess.Messages[startIdx].ID
	}

	return ""
}

// truncateMessages removes messages before the first kept ID
func (c *Compactor) truncateMessages(sess *Session, firstKeptID string) {
	if firstKeptID == "" {
		return
	}

	sess.mu.Lock()
	defer sess.mu.Unlock()

	// Find the index of the first kept message
	startIdx := -1
	for i, msg := range sess.Messages {
		if msg.ID == firstKeptID {
			startIdx = i
			break
		}
	}

	if startIdx > 0 {
		// Keep messages from startIdx onwards
		sess.Messages = sess.Messages[startIdx:]
	}
}
