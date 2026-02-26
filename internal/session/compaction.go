package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// CompactionManager handles session compaction with background retry
type CompactionManager struct {
	// Configuration
	config *CompactionManagerConfig
	store  Store // Storage backend

	// State (in-memory, transient)
	inProgress atomic.Bool

	// Background goroutine control
	stopCh      chan struct{}
	wg          sync.WaitGroup
	shutdownCtx context.Context // Cancelled on shutdown; used for async summaries
}

// CompactionManagerConfig holds all compaction settings
type CompactionManagerConfig struct {
	// Core settings
	ReserveTokens    int  // Tokens to reserve before compaction (default: 4000)
	MaxMessages      int  // Trigger compaction if messages exceed this (default: 500, 0 = disabled)
	PreferCheckpoint bool // Use checkpoint for summary if available

	// Retention settings
	KeepPercent int // Percent of messages to keep after compaction (default: 50)
	MinMessages int // Minimum messages to always keep (default: 20)

	// Retry settings
	RetryIntervalSeconds int // Background retry interval (default: 60, 0 = disabled)
}

// CompactionResult contains the result of a compaction operation
type CompactionResult struct {
	Summary             string
	TokensBefore        int
	TokensAfter         int
	MessagesAfter       int
	FirstKeptEntryID    string
	FromCheckpoint      bool
	EmergencyTruncation bool   // True if both LLMs failed
	UsedFallback        bool   // True if main model was used instead of Ollama
	Model               string // Model used for summary generation
	Details             *CompactionDetails
}

// CompactionStatus contains the current health state of the compaction manager
type CompactionStatus struct {
	RetryInProgress bool // True if compaction is currently running
	PendingRetries  int  // Number of pending summary retries in SQLite
	ClientAvailable bool // True if LLM client is available
}

// NewCompactionManager creates a new compaction manager
func NewCompactionManager(cfg *CompactionManagerConfig) *CompactionManager {
	// Apply defaults
	if cfg.ReserveTokens == 0 {
		cfg.ReserveTokens = 4000
	}
	if cfg.KeepPercent == 0 {
		cfg.KeepPercent = 50
	}
	if cfg.MinMessages == 0 {
		cfg.MinMessages = 20
	}
	if cfg.RetryIntervalSeconds == 0 {
		cfg.RetryIntervalSeconds = 60
	}

	return &CompactionManager{
		config: cfg,
		stopCh: make(chan struct{}),
	}
}

// getClient returns the current summarization client from the LLM registry.
func (m *CompactionManager) getClient() SummarizationClient {
	reg := llm.GetRegistry()
	if reg == nil {
		return nil
	}
	provider, err := reg.GetProvider("summarization")
	if err != nil {
		L_debug("compaction: no summarization provider", "error", err)
		return nil
	}
	// The provider implements SummarizationClient interface
	if client, ok := provider.(SummarizationClient); ok {
		return client
	}
	return nil
}

// SetStore sets the store for persistence
func (m *CompactionManager) SetStore(store Store) {
	m.store = store
}

// GetMaxMessages returns the configured max messages threshold
func (m *CompactionManager) GetMaxMessages() int {
	if m == nil || m.config == nil {
		return 0
	}
	return m.config.MaxMessages
}

// GetStatus returns the current health state of the compaction manager
func (m *CompactionManager) GetStatus(ctx context.Context) CompactionStatus {
	if m == nil {
		return CompactionStatus{}
	}

	client := m.getClient()
	status := CompactionStatus{
		RetryInProgress: m.inProgress.Load(),
		ClientAvailable: client != nil && client.IsAvailable(),
	}

	// Get pending retries from store
	if m.store != nil {
		pending, err := m.store.GetPendingSummaryRetry(ctx)
		if err == nil && pending != nil {
			status.PendingRetries = 1 // We only track one at a time currently
		}
	}

	return status
}

// Start begins the background retry goroutine
func (m *CompactionManager) Start(ctx context.Context) {
	if m.config.RetryIntervalSeconds <= 0 {
		L_debug("compaction: background retry disabled (interval=0)")
		return
	}

	m.shutdownCtx = ctx
	m.wg.Add(1)
	go m.runRetryLoop(ctx)
	L_info("compaction: background retry started", "intervalSeconds", m.config.RetryIntervalSeconds)
}

// Stop stops the background retry goroutine
func (m *CompactionManager) Stop() {
	close(m.stopCh)
	m.wg.Wait()
	L_debug("compaction: background retry stopped")
}

// ShouldCompact determines if compaction is needed
func (m *CompactionManager) ShouldCompact(sess *Session) bool {
	if m == nil || m.config == nil {
		return false
	}

	messageCount := len(sess.Messages)

	// Check message count threshold first (if configured)
	if m.config.MaxMessages > 0 && messageCount > m.config.MaxMessages {
		L_info("compaction threshold reached (message count)",
			"messages", messageCount,
			"maxMessages", m.config.MaxMessages)
		return true
	}

	// Check token threshold
	maxTokens := sess.GetMaxTokens()
	if maxTokens == 0 {
		// MaxTokens should be set at startup for primary session
		// If it's 0 here, we can only rely on message count compaction
		L_debug("compaction: maxTokens not set, skipping token-based check",
			"session", sess.Key,
			"messages", messageCount,
			"totalTokens", sess.GetTotalTokens())
		return false
	}

	totalTokens := sess.GetTotalTokens()
	threshold := maxTokens - m.config.ReserveTokens

	shouldCompact := totalTokens >= threshold
	if shouldCompact {
		L_info("compaction threshold reached (tokens)",
			"totalTokens", totalTokens,
			"maxTokens", maxTokens,
			"threshold", threshold,
			"reserve", m.config.ReserveTokens,
			"messages", messageCount)
	}

	return shouldCompact
}

// Compact performs compaction on a session.
// Truncation happens immediately (fast), summary generation is async (slow).
// Returns quickly - user is not blocked waiting for LLM summary.
func (m *CompactionManager) Compact(ctx context.Context, sess *Session, sessionFile string) (*CompactionResult, error) {
	if m == nil {
		return nil, fmt.Errorf("compaction manager not initialized")
	}

	// Prevent concurrent compactions
	if !m.inProgress.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("compaction already in progress")
	}
	// Note: inProgress is cleared after truncation, not after summary generation

	tokensBefore := sess.GetTotalTokens()
	messagesBefore := len(sess.Messages)
	L_info("starting compaction", "tokensBefore", tokensBefore, "messages", messagesBefore)

	var summary string
	var fromCheckpoint bool
	var summaryModel string
	var needsAsyncSummary bool

	// Fast path: use recent checkpoint (no async needed)
	if m.config.PreferCheckpoint && sess.LastCheckpoint != nil {
		checkpointTokens := sess.LastCheckpoint.Checkpoint.TokensAtCheckpoint
		if checkpointTokens >= tokensBefore/2 {
			summary = m.buildSummaryFromCheckpoint(sess.LastCheckpoint)
			fromCheckpoint = true
			summaryModel = "checkpoint"
			L_info("using checkpoint for compaction summary",
				"checkpointTokens", checkpointTokens,
				"tokensBefore", tokensBefore)
		}
	}

	// No checkpoint available - will generate summary async
	if summary == "" {
		summary = fmt.Sprintf("[Summary pending - %d messages compacted at %s]",
			messagesBefore, time.Now().Format("15:04:05"))
		summaryModel = "pending"
		needsAsyncSummary = true
	}

	// Extract file tracking details before truncation
	details := m.extractFileDetails(sess)

	// Capture messages for async summary generation BEFORE truncation
	var messagesToSummarize []Message
	if needsAsyncSummary {
		messagesToSummarize = make([]Message, messagesBefore)
		copy(messagesToSummarize, sess.Messages)
	}

	// Calculate what to keep based on config
	firstKeptID := m.findFirstKeptID(sess, m.config.KeepPercent)

	// Get session key for storage
	sessionKey := sess.Key
	if sessionKey == "" {
		sessionKey = PrimarySession
	}

	// Write compaction record with placeholder or checkpoint summary
	var compactionID string
	if m.store != nil {
		storedComp := &StoredCompaction{
			ID:                GenerateRecordID(),
			Timestamp:         time.Now(),
			Summary:           summary,
			FirstKeptEntryID:  firstKeptID,
			TokensBefore:      tokensBefore,
			NeedsSummaryRetry: needsAsyncSummary,
		}
		compactionID = storedComp.ID

		if parentID := sess.GetLastRecordID(); parentID != nil {
			storedComp.ParentID = *parentID
		}

		if err := m.store.AppendCompaction(ctx, sessionKey, storedComp); err != nil {
			m.inProgress.Store(false)
			return nil, fmt.Errorf("failed to write compaction to store: %w", err)
		}

		sess.SetLastRecordID(storedComp.ID)
	}

	// Truncate messages immediately (fast)
	m.truncateMessages(sess, firstKeptID)

	// Invalidate stateful provider state (e.g. oai-next incremental indices).
	// After truncation the message array has shifted, so saved indices are stale.
	if m.store != nil {
		if err := m.store.DeleteProviderStates(ctx, sessionKey); err != nil {
			L_warn("compaction: failed to clear provider states", "error", err)
		}
	}

	// Recalculate token count after truncation
	estimator := GetTokenEstimator()
	sess.SetTotalTokens(estimator.EstimateSessionTokens(sess))

	// Update session metadata
	sess.CompactionCount++
	sess.ResetFlushedThresholds()

	tokensAfter := sess.GetTotalTokens()

	// Release the lock - truncation is done, summary can proceed in background
	m.inProgress.Store(false)

	// Fire off async summary generation if needed
	if needsAsyncSummary && len(messagesToSummarize) > 0 {
		asyncCtx := context.Background()
		if m.shutdownCtx != nil {
			asyncCtx = m.shutdownCtx
		}
		go m.generateSummaryAsync(asyncCtx, sessionKey, compactionID, messagesToSummarize)
	}

	result := &CompactionResult{
		Summary:             summary,
		TokensBefore:        tokensBefore,
		TokensAfter:         tokensAfter,
		MessagesAfter:       len(sess.Messages),
		FirstKeptEntryID:    firstKeptID,
		FromCheckpoint:      fromCheckpoint,
		EmergencyTruncation: false, // No longer needed - async handles failures gracefully
		UsedFallback:        false,
		Model:               summaryModel,
		Details:             details,
	}

	L_info("compaction truncation completed",
		"tokensBefore", tokensBefore,
		"tokensAfter", tokensAfter,
		"messagesAfter", len(sess.Messages),
		"summaryModel", summaryModel,
		"asyncSummary", needsAsyncSummary,
		"compactionCount", sess.CompactionCount)

	return result, nil
}

// generateSummaryAsync generates a summary in the background and updates the compaction record.
// Called after truncation is complete - user is not blocked.
func (m *CompactionManager) generateSummaryAsync(ctx context.Context, sessionKey, compactionID string, messages []Message) {
	L_info("compaction: starting async summary generation",
		"compactionID", compactionID,
		"messages", len(messages))

	startTime := time.Now()

	// Generate summary via LLM
	summary, _, model, err := m.generateSummary(ctx, messages)
	elapsed := time.Since(startTime)

	if err != nil {
		L_warn("compaction: async summary generation failed, will retry later",
			"compactionID", compactionID,
			"error", err,
			"elapsed", elapsed.Round(time.Second))
		// NeedsSummaryRetry is already true - background retry loop will handle it
		return
	}

	// Update compaction record with real summary
	if m.store != nil {
		if err := m.store.UpdateCompactionSummary(ctx, compactionID, summary); err != nil {
			L_warn("compaction: failed to update summary in store",
				"compactionID", compactionID,
				"error", err)
			return
		}
	}

	L_info("compaction: async summary completed",
		"compactionID", compactionID,
		"model", model,
		"elapsed", elapsed.Round(time.Second))
}

// generateSummary generates a summary using the registry with failover support
// Returns: summary, usedFallback, modelName, error
func (m *CompactionManager) generateSummary(ctx context.Context, messages []Message) (string, bool, string, error) {
	reg := llm.GetRegistry()
	if reg == nil {
		return "", false, "", fmt.Errorf("no LLM registry available for summary generation")
	}

	// Get configured maxInputTokens from registry (0 = use model context)
	maxInputTokens := reg.GetMaxInputTokens("summarization")

	// Get primary model name for logging
	models := reg.ListModelsForPurpose("summarization")
	primaryModel := ""
	if len(models) > 0 {
		primaryModel = models[0]
	}

	L_info("compaction: generating summary", "primaryModel", primaryModel, "messages", len(messages))
	startTime := time.Now()

	summary, modelUsed, err := GenerateSummaryWithRegistry(ctx, reg, messages, maxInputTokens)
	elapsed := time.Since(startTime)

	if err != nil {
		L_warn("compaction: summary generation failed", "error", err, "elapsed", elapsed.Round(time.Second))
		return "", false, "", fmt.Errorf("summary generation failed: %w", err)
	}

	// Check if we used a fallback model
	usedFallback := modelUsed != primaryModel

	L_info("compaction: summary completed",
		"model", modelUsed,
		"usedFallback", usedFallback,
		"elapsed", elapsed.Round(time.Second))
	return summary, usedFallback, modelUsed, nil
}

// runRetryLoop runs the background retry goroutine
func (m *CompactionManager) runRetryLoop(ctx context.Context) {
	defer m.wg.Done()

	// Immediate check on startup
	m.retryPendingSummary(ctx)

	ticker := time.NewTicker(time.Duration(m.config.RetryIntervalSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.retryPendingSummary(ctx)
		case <-m.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// retryPendingSummary checks for and retries pending summary generations
func (m *CompactionManager) retryPendingSummary(ctx context.Context) {
	if m.store == nil {
		return
	}

	// Skip if compaction is in progress
	if m.inProgress.Load() {
		L_trace("compaction: retry skipped (compaction in progress)")
		return
	}

	// Get pending retry
	pending, err := m.store.GetPendingSummaryRetry(ctx)
	if err != nil {
		L_warn("compaction: failed to check pending retries", "error", err)
		return
	}
	if pending == nil {
		L_trace("compaction: no pending summary retries")
		return
	}

	L_info("compaction: found pending summary retry",
		"compactionID", pending.ID,
		"sessionKey", pending.SessionKey,
		"timestamp", pending.Timestamp)

	// Get previous compaction to determine message range
	prevCompaction, err := m.store.GetPreviousCompaction(ctx, pending.SessionKey, pending.Timestamp)
	if err != nil {
		L_warn("compaction: failed to get previous compaction", "error", err)
		return
	}

	// Load messages from range
	var startAfterID string
	if prevCompaction != nil {
		startAfterID = prevCompaction.FirstKeptEntryID
	}

	messages, err := m.store.GetMessagesInRange(ctx, pending.SessionKey, startAfterID, pending.FirstKeptEntryID)
	if err != nil {
		L_warn("compaction: failed to load messages for retry", "error", err)
		return
	}

	if len(messages) == 0 {
		L_debug("compaction: no messages found for retry, clearing flag", "compactionID", pending.ID)
		_ = m.store.UpdateCompactionSummary(ctx, pending.ID, pending.Summary)
		return
	}

	// Convert StoredMessages to Messages for LLM
	sessionMessages := make([]Message, len(messages))
	for i, sm := range messages {
		sessionMessages[i] = Message{
			ID:        sm.ID,
			Role:      sm.Role,
			Content:   sm.Content,
			ToolName:  sm.ToolName,
			ToolInput: sm.ToolInput,
			Timestamp: sm.Timestamp,
		}
	}

	// Try to generate summary
	summary, usedFallback, model, err := m.generateSummary(ctx, sessionMessages)
	if err != nil {
		L_warn("compaction: retry failed, will try again later",
			"compactionID", pending.ID,
			"error", err)
		return // Don't clear the flag, try again next interval
	}
	_ = model // Model not stored in retry path (legacy compaction)

	// Update compaction with better summary
	if err := m.store.UpdateCompactionSummary(ctx, pending.ID, summary); err != nil {
		L_warn("compaction: failed to update summary", "compactionID", pending.ID, "error", err)
		return
	}

	L_info("compaction: recovered pending summary",
		"compactionID", pending.ID,
		"usedFallback", usedFallback,
		"messageCount", len(messages))
}

// buildSummaryFromCheckpoint builds a summary string from a checkpoint record
func (m *CompactionManager) buildSummaryFromCheckpoint(cp *CheckpointRecord) string {
	var parts []string

	if cp.Checkpoint.Summary != "" {
		parts = append(parts, cp.Checkpoint.Summary)
	}

	if len(cp.Checkpoint.Topics) > 0 {
		parts = append(parts, fmt.Sprintf("\nTopics discussed: %s", strings.Join(cp.Checkpoint.Topics, ", ")))
	}

	if len(cp.Checkpoint.KeyDecisions) > 0 {
		parts = append(parts, fmt.Sprintf("\nKey decisions:\n- %s", strings.Join(cp.Checkpoint.KeyDecisions, "\n- ")))
	}

	if len(cp.Checkpoint.OpenQuestions) > 0 {
		parts = append(parts, fmt.Sprintf("\nOpen questions:\n- %s", strings.Join(cp.Checkpoint.OpenQuestions, "\n- ")))
	}

	return strings.Join(parts, "\n")
}

// extractFileDetails extracts read/modified files from session messages
func (m *CompactionManager) extractFileDetails(sess *Session) *CompactionDetails {
	readFiles := make(map[string]bool)
	modifiedFiles := make(map[string]bool)

	for _, msg := range sess.Messages {
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
func (m *CompactionManager) findFirstKeptID(sess *Session, keepPercent int) string {
	if keepPercent <= 0 {
		keepPercent = 50
	}
	if keepPercent > 100 {
		keepPercent = 100
	}

	keepCount := (len(sess.Messages) * keepPercent) / 100

	// Apply minimum floor to prevent amnesia
	minMessages := m.config.MinMessages
	if minMessages <= 0 {
		minMessages = 20
	}
	if keepCount < minMessages {
		keepCount = minMessages
	}
	if keepCount > len(sess.Messages) {
		keepCount = len(sess.Messages)
	}

	startIdx := len(sess.Messages) - keepCount
	if startIdx < 0 {
		startIdx = 0
	}

	// Walk boundary backwards to avoid splitting tool call/result pairs.
	// The Responses API rejects orphaned function_call_output items.
	for startIdx > 0 && (sess.Messages[startIdx].Role == "tool_result" || sess.Messages[startIdx].Role == "tool_use") {
		startIdx--
	}

	L_debug("compaction: calculating keep range",
		"totalMessages", len(sess.Messages),
		"keepPercent", keepPercent,
		"minMessages", minMessages,
		"keepCount", keepCount,
		"startIdx", startIdx)

	if startIdx < len(sess.Messages) {
		return sess.Messages[startIdx].ID
	}

	return ""
}

// truncateMessages removes messages before the first kept ID
func (m *CompactionManager) truncateMessages(sess *Session, firstKeptID string) {
	if firstKeptID == "" {
		return
	}

	sess.mu.Lock()
	defer sess.mu.Unlock()

	startIdx := -1
	for i, msg := range sess.Messages {
		if msg.ID == firstKeptID {
			startIdx = i
			break
		}
	}

	if startIdx > 0 {
		sess.Messages = sess.Messages[startIdx:]
	}
}

// Legacy compatibility aliases

// Compactor is an alias for CompactionManager for backwards compatibility
type Compactor = CompactionManager

// CompactorConfig is an alias for CompactionManagerConfig
type CompactorConfig = CompactionManagerConfig

// NewCompactor creates a CompactionManager (backwards compatibility)
func NewCompactor(cfg *CompactorConfig) *CompactionManager {
	return NewCompactionManager(cfg)
}
