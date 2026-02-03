package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// CompactionManager handles session compaction with background retry and fallback logic
type CompactionManager struct {
	// Configuration
	config *CompactionManagerConfig

	// Clients
	ollamaClient   CompactionLLMClient    // Primary: Ollama (can be nil)
	fallbackClient CompactionLLMClient    // Fallback: Anthropic model
	failover       *OllamaFailoverManager // Shared failover state
	store          Store                  // Storage backend

	// State (in-memory, transient)
	inProgress atomic.Bool
	mu         sync.Mutex

	// Background goroutine control
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// CompactionManagerConfig holds all compaction settings
type CompactionManagerConfig struct {
	// Core settings
	ReserveTokens    int  // Tokens to reserve before compaction (default: 4000)
	MaxMessages      int  // Trigger compaction if messages exceed this (default: 500, 0 = disabled)
	PreferCheckpoint bool // Use checkpoint for summary if available

	// Retry settings
	RetryIntervalSeconds int // Background retry interval (default: 60, 0 = disabled)
}

// CompactionLLMClient interface for LLM calls during compaction
type CompactionLLMClient interface {
	GenerateSummary(ctx context.Context, messages []Message, maxTokens int) (string, error)
	IsAvailable() bool
	Model() string
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
	OllamaFailures    int       // Current consecutive failure count
	OllamaThreshold   int       // Threshold before fallback
	UsingFallback     bool      // True if currently using main model due to failures
	LastOllamaAttempt time.Time // Time of last Ollama attempt
	ResetMinutes      int       // Minutes until Ollama retry reset
	MinutesUntilReset int       // Remaining minutes until Ollama is tried again
	RetryInProgress   bool      // True if compaction is currently running
	PendingRetries    int       // Number of pending summary retries in SQLite
	OllamaConfigured  bool      // True if Ollama client is configured
	OllamaAvailable   bool      // True if Ollama is currently available
}

// NewCompactionManager creates a new compaction manager
func NewCompactionManager(cfg *CompactionManagerConfig) *CompactionManager {
	// Apply defaults
	if cfg.ReserveTokens == 0 {
		cfg.ReserveTokens = 4000
	}
	if cfg.RetryIntervalSeconds == 0 {
		cfg.RetryIntervalSeconds = 60
	}

	return &CompactionManager{
		config: cfg,
		stopCh: make(chan struct{}),
	}
}

// SetOllamaClient sets the primary (Ollama) LLM client
func (m *CompactionManager) SetOllamaClient(client CompactionLLMClient) {
	m.ollamaClient = client
}

// SetFallbackClient sets the fallback (Anthropic) LLM client
func (m *CompactionManager) SetFallbackClient(client CompactionLLMClient) {
	m.fallbackClient = client
}

// SetFailover sets the shared failover manager
func (m *CompactionManager) SetFailover(failover *OllamaFailoverManager) {
	m.failover = failover
}

// SetStore sets the store for persistence
func (m *CompactionManager) SetStore(store Store) {
	m.store = store
}

// GetStatus returns the current health state of the compaction manager
func (m *CompactionManager) GetStatus(ctx context.Context) CompactionStatus {
	if m == nil {
		return CompactionStatus{}
	}

	status := CompactionStatus{
		RetryInProgress:  m.inProgress.Load(),
		OllamaConfigured: m.ollamaClient != nil,
	}

	// Get failover status if available
	if m.failover != nil {
		failoverStatus := m.failover.GetStatus()
		status.OllamaFailures = failoverStatus.Failures
		status.OllamaThreshold = failoverStatus.Threshold
		status.UsingFallback = failoverStatus.UsingFallback
		status.LastOllamaAttempt = failoverStatus.LastAttempt
		status.ResetMinutes = failoverStatus.ResetMinutes
		status.MinutesUntilReset = failoverStatus.ResetMinutes - failoverStatus.MinutesSince
		if status.MinutesUntilReset < 0 {
			status.MinutesUntilReset = 0
		}
	}

	// Check Ollama availability
	if m.ollamaClient != nil {
		status.OllamaAvailable = m.ollamaClient.IsAvailable()
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

// Compact performs compaction on a session (immediate/direct call)
func (m *CompactionManager) Compact(ctx context.Context, sess *Session, sessionFile string) (*CompactionResult, error) {
	if m == nil {
		return nil, fmt.Errorf("compaction manager not initialized")
	}

	// Prevent concurrent compactions
	if !m.inProgress.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("compaction already in progress")
	}
	defer m.inProgress.Store(false)

	tokensBefore := sess.GetTotalTokens()
	L_info("starting compaction", "tokensBefore", tokensBefore, "messages", len(sess.Messages))

	var summary string
	var fromCheckpoint bool
	var emergencyTruncation bool
	var usedFallback bool
	var summaryModel string
	var details *CompactionDetails

	// Try fast path: use recent checkpoint
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

	// Slow path: generate summary via LLM
	if summary == "" {
		L_info("compaction: generating summary via LLM", "messages", len(sess.Messages), "tokens", tokensBefore)
		var err error
		summary, usedFallback, summaryModel, err = m.generateSummaryWithFallback(ctx, sess.Messages)
		if err != nil {
			// Fall back to checkpoint if available
			if sess.LastCheckpoint != nil {
				summary = m.buildSummaryFromCheckpoint(sess.LastCheckpoint)
				fromCheckpoint = true
				summaryModel = "checkpoint"
				L_warn("LLM summary failed, falling back to checkpoint", "error", err)
			} else {
				// Emergency: no checkpoint, no LLM - aggressive truncation
				summary = m.buildBasicSummary(sess)
				emergencyTruncation = true
				summaryModel = "truncation"
				L_warn("all LLMs failed, using emergency truncation (keeping last 20%)", "error", err)
			}
		}
	}

	// Extract file tracking details
	details = m.extractFileDetails(sess)

	// Normal compaction keeps 50%, emergency keeps 70%
	// This preserves more context and reduces amnesia after compaction
	keepPercent := 50
	if emergencyTruncation {
		keepPercent = 70
	}
	firstKeptID := m.findFirstKeptID(sess, keepPercent)

	// Write compaction record (use sess.Key for multi-user support)
	sessionKey := sess.Key
	if sessionKey == "" {
		sessionKey = PrimarySession // fallback for legacy sessions without Key set
	}
	if m.store != nil {
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

		if err := m.store.AppendCompaction(ctx, sessionKey, storedComp); err != nil {
			return nil, fmt.Errorf("failed to write compaction to store: %w", err)
		}

		sess.SetLastRecordID(storedComp.ID)
	}

	// Truncate messages in session
	m.truncateMessages(sess, firstKeptID)

	// Update session metadata
	sess.CompactionCount++
	sess.ResetFlushedThresholds()

	tokensAfter := sess.GetTotalTokens()
	result := &CompactionResult{
		Summary:             summary,
		TokensBefore:        tokensBefore,
		TokensAfter:         tokensAfter,
		MessagesAfter:       len(sess.Messages),
		FirstKeptEntryID:    firstKeptID,
		FromCheckpoint:      fromCheckpoint,
		EmergencyTruncation: emergencyTruncation,
		UsedFallback:        usedFallback,
		Model:               summaryModel,
		Details:             details,
	}

	L_info("compaction completed",
		"tokensBefore", tokensBefore,
		"tokensAfter", tokensAfter,
		"messagesAfter", len(sess.Messages),
		"model", summaryModel,
		"fromCheckpoint", fromCheckpoint,
		"emergencyTruncation", emergencyTruncation,
		"usedFallback", usedFallback,
		"compactionCount", sess.CompactionCount)

	return result, nil
}

// generateSummaryWithFallback tries Ollama first, then falls back to fallback model
// Returns: summary, usedFallback, modelName, error
func (m *CompactionManager) generateSummaryWithFallback(ctx context.Context, messages []Message) (string, bool, string, error) {
	// Check if we should try Ollama (failover manager handles reset logic)
	shouldTryOllama := m.ollamaClient != nil && m.ollamaClient.IsAvailable() &&
		(m.failover == nil || m.failover.ShouldTryOllama())

	// Try Ollama first (if available and failover allows)
	if shouldTryOllama {
		if m.failover != nil {
			m.failover.RecordAttempt()
		}

		model := m.ollamaClient.Model()
		L_info("compaction: calling ollama for summary", "model", model, "messages", len(messages))
		startTime := time.Now()
		summary, err := m.ollamaClient.GenerateSummary(ctx, messages, 4000)
		elapsed := time.Since(startTime)
		if err == nil {
			// Success - reset failure count
			if m.failover != nil {
				m.failover.RecordSuccess()
			}
			L_info("compaction: ollama summary completed", "model", model, "elapsed", elapsed.Round(time.Second))
			return summary, false, model, nil
		}

		// Ollama failed - record failure
		var failures int
		if m.failover != nil {
			failures = m.failover.RecordFailure()
		}
		L_warn("compaction: ollama failed, trying fallback",
			"error", err,
			"elapsed", elapsed.Round(time.Second),
			"failures", failures)
	} else if m.ollamaClient != nil && m.failover != nil && m.failover.IsUsingFallback() {
		status := m.failover.GetStatus()
		L_debug("compaction: skipping ollama (too many failures)",
			"failures", status.Failures,
			"threshold", status.Threshold)
	}

	// Try fallback model
	if m.fallbackClient != nil && m.fallbackClient.IsAvailable() {
		model := m.fallbackClient.Model()
		L_info("compaction: calling fallback model for summary", "model", model, "messages", len(messages))
		startTime := time.Now()
		summary, err := m.fallbackClient.GenerateSummary(ctx, messages, 4000)
		elapsed := time.Since(startTime)
		if err == nil {
			L_info("compaction: fallback model summary completed", "model", model, "elapsed", elapsed.Round(time.Second))
			return summary, true, model, nil
		}
		L_warn("compaction: fallback model also failed", "error", err, "elapsed", elapsed.Round(time.Second))
		return "", false, "", fmt.Errorf("all LLM clients failed: %w", err)
	}

	return "", false, "", fmt.Errorf("no LLM clients available for summary generation")
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

	// Note: Ollama failure reset is handled by failover manager's ShouldTryOllama()

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
		L_warn("compaction: no messages found for retry, clearing flag", "compactionID", pending.ID)
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
	summary, usedFallback, model, err := m.generateSummaryWithFallback(ctx, sessionMessages)
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

// buildBasicSummary builds a minimal summary when no LLM is available
func (m *CompactionManager) buildBasicSummary(sess *Session) string {
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
	// Minimum floor of 20 messages to prevent amnesia
	if keepCount < 20 {
		keepCount = 20
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

// SetLLMClient sets both Ollama and fallback client to the same client (backwards compatibility)
func (m *CompactionManager) SetLLMClient(client CompactionLLMClient) {
	m.SetOllamaClient(client)
	m.SetFallbackClient(client)
}
