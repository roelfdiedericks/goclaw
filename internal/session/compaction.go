package session

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
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
	lookup func(string) *Session

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
	KeepPercent        int // Percent of messages to keep after compaction (default: 50)
	MinMessages        int // Minimum messages to always keep (default: 20)
	FreshTailCount     int // When > 0, keep this many newest messages instead of KeepPercent
	FreshTailMaxTokens int // Optional extra cap for the fresh tail token budget

	// Retry settings
	RetryIntervalSeconds int // Background retry interval (default: 60, 0 = disabled)

	// LCM settings
	LCMEnabled               bool
	Preset                   string // Resolved preset name (balanced/aggressive/long_term_memory/recall_heavy/custom)
	SummaryInjectionMode     string
	MaxInjectedSummaryTokens int
	SummaryMaxOverageFactor  float64
	LeafMinFanout            int
	CondensedMinFanout       int
	IncrementalMaxDepth      int
	LeafTargetTokens         int
	CondensedTargetTokens    int
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
	if cfg.LeafMinFanout == 0 {
		cfg.LeafMinFanout = 4
	}
	if cfg.CondensedMinFanout == 0 {
		cfg.CondensedMinFanout = 4
	}
	if cfg.IncrementalMaxDepth == 0 {
		cfg.IncrementalMaxDepth = 2
	}
	if cfg.LeafTargetTokens == 0 {
		cfg.LeafTargetTokens = 800
	}
	if cfg.CondensedTargetTokens == 0 {
		cfg.CondensedTargetTokens = 1200
	}
	if cfg.SummaryInjectionMode == "" {
		cfg.SummaryInjectionMode = LCMSummaryInjectionModeFrontier
	}
	if cfg.MaxInjectedSummaryTokens == 0 {
		cfg.MaxInjectedSummaryTokens = defaultLCMBudgetTokens
	}
	if cfg.SummaryMaxOverageFactor <= 0 {
		cfg.SummaryMaxOverageFactor = defaultLCMOverage
	}

	L_info("lcm: enabled",
		"enabled", cfg.LCMEnabled,
		"summaryInjectionMode", cfg.SummaryInjectionMode,
		"maxInjectedSummaryTokens", cfg.MaxInjectedSummaryTokens,
		"leafMinFanout", cfg.LeafMinFanout,
		"condensedMinFanout", cfg.CondensedMinFanout,
		"incrementalMaxDepth", cfg.IncrementalMaxDepth,
		"leafTargetTokens", cfg.LeafTargetTokens,
		"condensedTargetTokens", cfg.CondensedTargetTokens,
	)

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

// SetSessionLookup configures lookup of active sessions for live overlay refreshes.
func (m *CompactionManager) SetSessionLookup(lookup func(string) *Session) {
	m.lookup = lookup
}

// GetMaxMessages returns the configured max messages threshold
func (m *CompactionManager) GetMaxMessages() int {
	if m == nil || m.config == nil {
		return 0
	}
	return m.config.MaxMessages
}

// GetReserveTokens returns the configured token reserve floor.
func (m *CompactionManager) GetReserveTokens() int {
	if m == nil || m.config == nil {
		return 0
	}
	return m.config.ReserveTokens
}

func (m *CompactionManager) IsLCMEnabled() bool {
	if m == nil || m.config == nil {
		return false
	}
	return m.config.LCMEnabled
}

// Preset returns the resolved LCM preset name this manager was configured
// with (balanced, aggressive, long_term_memory, recall_heavy, or custom).
// Returns the empty string when the manager is nil or unconfigured, which
// callers should treat as "unknown".
func (m *CompactionManager) Preset() string {
	if m == nil || m.config == nil {
		return ""
	}
	return m.config.Preset
}

// LCMConfigSnapshot is a read-only, JSON-friendly view of the LCM-relevant
// fields of CompactionManagerConfig. Used by agent-facing tools that need to
// render the effective configuration without exposing the mutable manager.
type LCMConfigSnapshot struct {
	Enabled                  bool    `json:"enabled"`
	Preset                   string  `json:"preset"`
	SummaryInjectionMode     string  `json:"summaryInjectionMode"`
	MaxInjectedSummaryTokens int     `json:"maxInjectedSummaryTokens"`
	SummaryMaxOverageFactor  float64 `json:"summaryMaxOverageFactor"`
	FreshTailCount           int     `json:"freshTailCount"`
	FreshTailMaxTokens       int     `json:"freshTailMaxTokens"`
	LeafMinFanout            int     `json:"leafMinFanout"`
	CondensedMinFanout       int     `json:"condensedMinFanout"`
	IncrementalMaxDepth      int     `json:"incrementalMaxDepth"`
	LeafTargetTokens         int     `json:"leafTargetTokens"`
	CondensedTargetTokens    int     `json:"condensedTargetTokens"`
	RetryIntervalSeconds     int     `json:"retryIntervalSeconds"`
}

// LCMConfigSnapshot returns a read-only snapshot of the LCM-relevant
// configuration fields. Safe to call on a nil/unconfigured manager — returns
// a zero-value snapshot (Enabled=false, all fields zero).
func (m *CompactionManager) LCMConfigSnapshot() LCMConfigSnapshot {
	if m == nil || m.config == nil {
		return LCMConfigSnapshot{}
	}
	return LCMConfigSnapshot{
		Enabled:                  m.config.LCMEnabled,
		Preset:                   m.config.Preset,
		SummaryInjectionMode:     m.config.SummaryInjectionMode,
		MaxInjectedSummaryTokens: m.config.MaxInjectedSummaryTokens,
		SummaryMaxOverageFactor:  m.config.SummaryMaxOverageFactor,
		FreshTailCount:           m.config.FreshTailCount,
		FreshTailMaxTokens:       m.config.FreshTailMaxTokens,
		LeafMinFanout:            m.config.LeafMinFanout,
		CondensedMinFanout:       m.config.CondensedMinFanout,
		IncrementalMaxDepth:      m.config.IncrementalMaxDepth,
		LeafTargetTokens:         m.config.LeafTargetTokens,
		CondensedTargetTokens:    m.config.CondensedTargetTokens,
		RetryIntervalSeconds:     m.config.RetryIntervalSeconds,
	}
}

// lcmInjectionParams returns (mode, maxTokens) for summary injection, or
// sensible defaults when the CompactionManager is nil/unconfigured.
func (m *CompactionManager) lcmInjectionParams() (string, int) {
	if m == nil || m.config == nil {
		return LCMSummaryInjectionModeFrontier, defaultLCMBudgetTokens
	}
	return m.config.SummaryInjectionMode, m.config.MaxInjectedSummaryTokens
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

	keepStartIdx := m.findKeepStartIndex(sess, m.config.KeepPercent)
	firstKeptID := ""
	if keepStartIdx >= 0 && keepStartIdx < len(sess.Messages) {
		firstKeptID = sess.Messages[keepStartIdx].ID
	}

	var trimmedMessages []Message
	if keepStartIdx > 0 {
		trimmedMessages = make([]Message, keepStartIdx)
		copy(trimmedMessages, sess.Messages[:keepStartIdx])
	}

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
			len(trimmedMessages), time.Now().Format("15:04:05"))
		summaryModel = "pending"
		needsAsyncSummary = true
	}

	// Extract file tracking details before truncation
	details := m.extractFileDetails(trimmedMessages)

	// Capture messages for async summary generation BEFORE truncation
	var messagesToSummarize []Message
	if needsAsyncSummary {
		messagesToSummarize = make([]Message, len(trimmedMessages))
		copy(messagesToSummarize, trimmedMessages)
	}

	// Get session key for storage
	sessionKey := sess.Key
	if sessionKey == "" {
		sessionKey = PrimarySession
	}

	// Write compaction record with placeholder or checkpoint summary
	var compactionID string
	if m.store != nil {
		storedComp := &StoredCompaction{
			ID:                GenerateMessageID(),
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
		if m.IsLCMEnabled() {
			dag := m.buildLeafDAG(trimmedMessages)
			if err := m.store.UpdateCompactionDAG(ctx, compactionID, dag); err != nil {
				L_warn("lcm: failed to update compaction DAG", "compactionID", compactionID, "error", err)
			}
		} else {
			L_debug("lcm: skipping DAG update because LCM is disabled", "compactionID", compactionID)
		}

		sess.SetLastRecordID(storedComp.ID)
	}

	// Truncate messages immediately (fast)
	messagesRemoved := m.truncateMessages(sess, firstKeptID)

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
	rawTokensAfter := sess.GetTotalTokens()

	// Best-effort update of compaction telemetry fields after truncation.
	if m.store != nil && compactionID != "" {
		type compactionStatsUpdater interface {
			UpdateCompactionStats(ctx context.Context, compactionID string, tokensAfter, messagesRemoved int) error
		}
		if updater, ok := m.store.(compactionStatsUpdater); ok {
			if err := updater.UpdateCompactionStats(ctx, compactionID, rawTokensAfter, messagesRemoved); err != nil {
				L_warn("compaction: failed to update stats in store", "compactionID", compactionID, "error", err)
			}
		}
	}

	m.refreshLiveSessionCompactionContext(sessionKey, sess)

	tokensAfter := sess.GetTotalTokens()

	// Release the lock - truncation is done, summary can proceed in background
	m.inProgress.Store(false)

	// Fire off async summary generation if needed
	if needsAsyncSummary && len(messagesToSummarize) > 0 {
		asyncCtx := context.Background()
		if m.shutdownCtx != nil {
			asyncCtx = m.shutdownCtx
		}
		go m.generateSummaryAsync(asyncCtx, sessionKey, compactionID, messagesToSummarize, sess)
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
func (m *CompactionManager) generateSummaryAsync(ctx context.Context, sessionKey, compactionID string, messages []Message, activeSess *Session) {
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

	m.refreshLiveSessionCompactionContext(sessionKey, activeSess)

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

	summary, modelUsed, err := GenerateSummaryWithRegistry(ctx, reg, messages, maxInputTokens, m.config.LeafTargetTokens)
	elapsed := time.Since(startTime)

	if err != nil {
		L_warn("compaction: summary generation failed", "error", err, "elapsed", elapsed.Round(time.Second))
		return "", false, "", fmt.Errorf("summary generation failed: %w", err)
	}
	summary = m.enforceSummaryOverageCap(summary, m.config.LeafTargetTokens, "leaf")

	// Check if we used a fallback model
	usedFallback := modelUsed != primaryModel

	L_info("compaction: summary completed",
		"model", modelUsed,
		"usedFallback", usedFallback,
		"elapsed", elapsed.Round(time.Second))
	if !strings.Contains(summary, "Expand for details about:") {
		L_warn("compaction: summary missing expand trailer", "modelUsed", modelUsed)
	}
	return summary, usedFallback, modelUsed, nil
}

// runRetryLoop runs the background retry goroutine
func (m *CompactionManager) runRetryLoop(ctx context.Context) {
	defer m.wg.Done()

	L_debug("lcm: retry loop starting (immediate tick follows)",
		"intervalSeconds", m.config.RetryIntervalSeconds,
		"lcmEnabled", m.config.LCMEnabled,
		"leafMinFanout", m.config.LeafMinFanout,
		"condensedMinFanout", m.config.CondensedMinFanout,
		"incrementalMaxDepth", m.config.IncrementalMaxDepth)

	// Immediate check on startup
	m.retryPendingSummary(ctx)
	m.condenseTick(ctx)

	ticker := time.NewTicker(time.Duration(m.config.RetryIntervalSeconds) * time.Second)
	defer ticker.Stop()

	tickCount := 0
	for {
		select {
		case <-ticker.C:
			tickCount++
			L_trace("lcm: retry loop tick fired", "tickCount", tickCount)
			m.retryPendingSummary(ctx)
			m.condenseTick(ctx)
		case <-m.stopCh:
			L_debug("lcm: retry loop stopping (stopCh)")
			return
		case <-ctx.Done():
			L_debug("lcm: retry loop stopping (ctx done)")
			return
		}
	}
}

func (m *CompactionManager) condenseTick(ctx context.Context) {
	if !m.IsLCMEnabled() {
		L_trace("lcm: condenseTick skipped (LCM disabled)")
		return
	}
	if m.store == nil {
		L_trace("lcm: condenseTick skipped (store is nil)")
		return
	}
	if m.inProgress.Load() {
		L_trace("lcm: condenseTick skipped (compaction in progress)")
		return
	}

	// Drive iteration from the compactions table, not from the sessions
	// table. The two can diverge (e.g. historical compactions keyed to
	// `primary` while the sessions row uses a namespaced key such as
	// `goclaw:main:main`), and in that case iterating the sessions table
	// makes the background loop spin forever on a phantom session whose
	// compactions table is empty, while the real backlog never drains.
	sessionKeys, err := m.store.ListCompactionSessionKeys(ctx)
	if err != nil {
		L_warn("lcm: condenseTick failed to list compaction session keys", "error", err)
		return
	}
	L_trace("lcm: condenseTick iterating compaction session keys",
		"sessionKeyCount", len(sessionKeys),
		"sessionKeys", sessionKeys)
	for _, key := range sessionKeys {
		if err := m.condenseSession(ctx, key); err != nil {
			L_warn("lcm: condenseSession failed", "sessionKey", key, "error", err)
		}
	}
}

// AnnotateNextBatchHint populates the NextBatchSize and NextBatchNewDepth
// fields on the given stats from the manager's current fanout/depth config.
// Used by `/session` to show what the next condensation tick will do for a
// session. Safe to call on a zero-value manager — it will produce a
// (0, 0) hint.
func (m *CompactionManager) AnnotateNextBatchHint(stats *CompactionDAGStats, compactions []StoredCompaction) {
	if stats == nil {
		return
	}
	if m == nil || m.config == nil {
		return
	}
	batch, newDepth, _ := pickCondensationBatch(
		compactions,
		m.config.LeafMinFanout,
		m.config.CondensedMinFanout,
		m.config.IncrementalMaxDepth,
	)
	stats.NextBatchSize = len(batch)
	stats.NextBatchNewDepth = newDepth
}

// BuildDAGStatsForSession returns the full CompactionDAGStats for the given
// session: structural DAG counts (including un-parented backlog), FTS row
// count when the backing store supports it, and the next-tick hint populated
// from this manager's fanout/depth config.
//
// Stores that do not support compaction queries return an empty-but-valid
// CompactionDAGStats (all counters zero, nextTick idle) rather than an error,
// so callers can always render something. Fetch errors from the store are
// surfaced as errors.
//
// Single source of truth for gateway (/session) and the transcript tool
// (agent-facing stats action). They must not diverge.
func (m *CompactionManager) BuildDAGStatsForSession(ctx context.Context, sessionKey string) (CompactionDAGStats, error) {
	empty := CompactionDAGStats{
		CondensedByDepth:           map[int]int{},
		UnparentedCondensedByDepth: map[int]int{},
	}
	if m == nil || m.store == nil {
		return empty, nil
	}
	compactions, err := m.store.GetCompactions(ctx, sessionKey)
	if err != nil {
		return empty, err
	}
	stats := buildCompactionDAGStats(compactions)
	if sqliteStore, ok := m.store.(*SQLiteStore); ok {
		if err := sqliteStore.db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM compactions_fts
			JOIN compactions c ON c.rowid = compactions_fts.rowid
			WHERE c.session_key = ?
		`, sessionKey).Scan(&stats.FTSRows); err != nil {
			return stats, err
		}
	}
	m.AnnotateNextBatchHint(&stats, compactions)
	return stats, nil
}

func (m *CompactionManager) condenseSession(ctx context.Context, sessionKey string) error {
	compactions, err := m.store.GetCompactions(ctx, sessionKey)
	if err != nil {
		return err
	}
	if len(compactions) == 0 {
		L_trace("lcm: condenseSession no compactions", "sessionKey", sessionKey)
		return nil
	}

	leafCandidates, condensedByDepth := countUnparentedCandidates(compactions, m.config.IncrementalMaxDepth)

	batch, newDepth, remaining := pickCondensationBatch(
		compactions,
		m.config.LeafMinFanout,
		m.config.CondensedMinFanout,
		m.config.IncrementalMaxDepth,
	)
	if batch == nil {
		L_trace("lcm: condenseSession no batch picked",
			"sessionKey", sessionKey,
			"totalCompactions", len(compactions),
			"unparentedLeaves", leafCandidates,
			"unparentedCondensedByDepth", condensedByDepth,
			"leafMinFanout", m.config.LeafMinFanout,
			"condensedMinFanout", m.config.CondensedMinFanout,
			"incrementalMaxDepth", m.config.IncrementalMaxDepth,
		)
		return nil
	}

	L_info("lcm: condensing batch",
		"sessionKey", sessionKey,
		"newDepth", newDepth,
		"batchSize", len(batch),
		"remainingBacklog", remaining,
		"unparentedLeaves", leafCandidates,
		"unparentedCondensedByDepth", condensedByDepth,
	)
	return m.createCondensedCompaction(ctx, sessionKey, batch, newDepth)
}

// countUnparentedCandidates returns the number of un-parented leaf candidates
// and a depth->count map of un-parented condensed candidates. Used for
// diagnostic logging in condenseSession so the reason a batch wasn't picked is
// visible in the log.
func countUnparentedCandidates(compactions []StoredCompaction, maxDepth int) (int, map[int]int) {
	childSet := make(map[string]bool, len(compactions))
	for _, comp := range compactions {
		for _, childID := range comp.ChildCompactionIDs {
			childSet[childID] = true
		}
	}
	leafCount := 0
	byDepth := map[int]int{}
	for _, comp := range compactions {
		if childSet[comp.ID] || comp.NeedsSummaryRetry {
			continue
		}
		if comp.Kind == "" || comp.Kind == CompactionKindLeaf {
			leafCount++
			continue
		}
		if comp.Kind == CompactionKindCondensed && comp.Depth >= 1 && comp.Depth < maxDepth {
			byDepth[comp.Depth]++
		}
	}
	return leafCount, byDepth
}

// pickCondensationBatch returns the next fanout-sized batch of compactions to
// condense, the new depth that batch should produce, and how many eligible
// candidates remain at that level after this batch is taken. It returns
// (nil, 0, 0) when nothing is eligible.
//
// Inputs:
//   - compactions: all compactions for a session, expected in timestamp-ASC
//     order (so the first N are the oldest N).
//   - leafFanout, condensedFanout: minimum un-parented candidates required to
//     form one condensed node at depth 1 and at depth >= 2, respectively.
//   - maxDepth: the highest depth the incremental builder will produce.
//
// The selection rules, in priority order:
//  1. If there are >= leafFanout un-parented leaves, take the oldest
//     leafFanout and condense them into a depth-1 node.
//  2. Otherwise, walk depths d = 1..maxDepth-1 and at the first depth that
//     has >= condensedFanout un-parented condensed nodes, take the oldest
//     condensedFanout and promote them into a depth-(d+1) node.
//
// Only one batch is returned per call so each summarization pass stays
// bounded; the caller drains a backlog over multiple ticks.
func pickCondensationBatch(compactions []StoredCompaction, leafFanout, condensedFanout, maxDepth int) ([]StoredCompaction, int, int) {
	if len(compactions) == 0 {
		return nil, 0, 0
	}

	childSet := make(map[string]bool, len(compactions))
	for _, comp := range compactions {
		for _, childID := range comp.ChildCompactionIDs {
			childSet[childID] = true
		}
	}

	var leafCandidates []StoredCompaction
	for _, comp := range compactions {
		if childSet[comp.ID] || comp.NeedsSummaryRetry {
			continue
		}
		if comp.Kind == "" || comp.Kind == CompactionKindLeaf {
			leafCandidates = append(leafCandidates, comp)
		}
	}
	if leafFanout > 0 && len(leafCandidates) >= leafFanout {
		batch := leafCandidates[:leafFanout]
		return batch, 1, len(leafCandidates) - len(batch)
	}

	for depth := 1; depth < maxDepth; depth++ {
		var condensedCandidates []StoredCompaction
		for _, comp := range compactions {
			if childSet[comp.ID] || comp.NeedsSummaryRetry {
				continue
			}
			if comp.Kind == CompactionKindCondensed && comp.Depth == depth {
				condensedCandidates = append(condensedCandidates, comp)
			}
		}
		if condensedFanout > 0 && len(condensedCandidates) >= condensedFanout {
			batch := condensedCandidates[:condensedFanout]
			return batch, depth + 1, len(condensedCandidates) - len(batch)
		}
	}

	return nil, 0, 0
}

func (m *CompactionManager) createCondensedCompaction(ctx context.Context, sessionKey string, children []StoredCompaction, newDepth int) error {
	reg := llm.GetRegistry()
	if reg == nil {
		return fmt.Errorf("no LLM registry available for condensation")
	}

	summariesText := buildSummariesForCondensation(children)
	summary, modelUsed, err := GenerateCondensedSummaryWithRegistry(ctx, reg, summariesText, newDepth, m.config.CondensedTargetTokens)
	if err != nil {
		return err
	}
	summary = m.enforceSummaryOverageCap(summary, m.config.CondensedTargetTokens, "condensed")
	if !strings.Contains(summary, "Expand for details about:") {
		L_warn("compaction: summary missing expand trailer", "modelUsed", modelUsed)
	}

	childIDs := make([]string, 0, len(children))
	var earliestAt *time.Time
	var latestAt *time.Time
	sourceTokenCount := 0
	for _, child := range children {
		childIDs = append(childIDs, child.ID)
		sourceTokenCount += child.SourceTokenCount
		if child.EarliestMessageAt != nil && (earliestAt == nil || child.EarliestMessageAt.Before(*earliestAt)) {
			earliestAt = cloneTimePtr(child.EarliestMessageAt)
		}
		if child.LatestMessageAt != nil && (latestAt == nil || child.LatestMessageAt.After(*latestAt)) {
			latestAt = cloneTimePtr(child.LatestMessageAt)
		}
	}

	storedComp := &StoredCompaction{
		ID:                 GenerateMessageID(),
		Timestamp:          time.Now(),
		Summary:            summary,
		Kind:               CompactionKindCondensed,
		Depth:              newDepth,
		ChildCompactionIDs: childIDs,
		EarliestMessageAt:  earliestAt,
		LatestMessageAt:    latestAt,
		SourceTokenCount:   sourceTokenCount,
	}

	if err := m.store.AppendCompaction(ctx, sessionKey, storedComp); err != nil {
		return err
	}

	m.refreshLiveSessionCompactionContext(sessionKey, nil)

	L_info("lcm: condensed summaries",
		"sessionKey", sessionKey,
		"newDepth", newDepth,
		"children", len(children),
		"modelUsed", modelUsed)
	return nil
}

func buildSummariesForCondensation(children []StoredCompaction) string {
	var b strings.Builder
	for _, child := range children {
		fmt.Fprintf(&b, "<summary id=%q kind=%q depth=%d>\n", FormatSummaryID(child.ID), CompactionKindOrLeaf(child.Kind), child.Depth)
		if child.EarliestMessageAt != nil || child.LatestMessageAt != nil {
			earliest := ""
			latest := ""
			if child.EarliestMessageAt != nil {
				earliest = child.EarliestMessageAt.UTC().Format(time.RFC3339)
			}
			if child.LatestMessageAt != nil {
				latest = child.LatestMessageAt.UTC().Format(time.RFC3339)
			}
			fmt.Fprintf(&b, "window: %s -> %s\n", earliest, latest)
		}
		b.WriteString(child.Summary)
		b.WriteString("\n</summary>\n\n")
	}
	return strings.TrimSpace(b.String())
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
		m.refreshLiveSessionCompactionContext(pending.SessionKey, nil)
		return
	}

	// Convert StoredMessages to Messages for LLM
	sessionMessages := make([]Message, len(messages))
	for i, sm := range messages {
		sessionMessages[i] = Message{
			ID:              sm.ID,
			Role:            sm.Role,
			Content:         sm.Content,
			ToolName:        sm.ToolName,
			ToolInput:       sm.ToolInput,
			Timestamp:       sm.Timestamp,
			ResponseGroupID: sm.ResponseGroupID,
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

	m.refreshLiveSessionCompactionContext(pending.SessionKey, nil)

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

func (m *CompactionManager) buildLeafDAG(messages []Message) CompactionDAGUpdate {
	update := CompactionDAGUpdate{
		Kind:  CompactionKindLeaf,
		Depth: 0,
	}
	if len(messages) == 0 {
		return update
	}

	update.SourceMessageIDs = make([]string, 0, len(messages))
	estimator := GetTokenEstimator()
	for i := range messages {
		update.SourceMessageIDs = append(update.SourceMessageIDs, messages[i].ID)
		update.SourceTokenCount += estimator.EstimateMessageTokens(&messages[i])

		ts := messages[i].Timestamp.UTC()
		if update.EarliestMessageAt == nil || ts.Before(*update.EarliestMessageAt) {
			update.EarliestMessageAt = cloneTimePtr(&ts)
		}
		if update.LatestMessageAt == nil || ts.After(*update.LatestMessageAt) {
			update.LatestMessageAt = cloneTimePtr(&ts)
		}
	}

	return update
}

// extractFileDetails extracts read/modified files from compacted messages.
func (m *CompactionManager) extractFileDetails(messages []Message) *CompactionDetails {
	readFiles := make(map[string]bool)
	modifiedFiles := make(map[string]bool)

	for _, msg := range messages {
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

func (m *CompactionManager) findKeepStartIndex(sess *Session, keepPercent int) int {
	if len(sess.Messages) == 0 {
		return -1
	}

	if m.config.FreshTailCount > 0 {
		keepCount := m.config.FreshTailCount
		if keepCount > len(sess.Messages) {
			keepCount = len(sess.Messages)
		}
		startIdx := len(sess.Messages) - keepCount
		if m.config.FreshTailMaxTokens > 0 {
			estimator := GetTokenEstimator()
			totalTokens := estimator.EstimateMessageTokens(&sess.Messages[len(sess.Messages)-1])
			protectedStart := len(sess.Messages) - 1
			keptCount := 1
			for i := len(sess.Messages) - 2; i >= 0 && keptCount < keepCount; i-- {
				msgTokens := estimator.EstimateMessageTokens(&sess.Messages[i])
				if totalTokens+msgTokens > m.config.FreshTailMaxTokens {
					L_info("lcm: fresh-tail token cap reduced kept tail",
						"requestedMessages", m.config.FreshTailCount,
						"keptMessages", keptCount,
						"tokenCap", m.config.FreshTailMaxTokens)
					break
				}
				totalTokens += msgTokens
				protectedStart = i
				keptCount++
			}
			startIdx = protectedStart
		}
		startIdx = alignStartIndexForToolPairs(sess.Messages, startIdx)
		L_debug("lcm: calculating fresh-tail keep range",
			"totalMessages", len(sess.Messages),
			"freshTailCount", m.config.FreshTailCount,
			"freshTailMaxTokens", m.config.FreshTailMaxTokens,
			"startIdx", startIdx)
		return startIdx
	}

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
	startIdx = alignStartIndexForToolPairs(sess.Messages, startIdx)

	L_debug("compaction: calculating keep range",
		"totalMessages", len(sess.Messages),
		"keepPercent", keepPercent,
		"minMessages", minMessages,
		"keepCount", keepCount,
		"startIdx", startIdx)

	return startIdx
}

func alignStartIndexForToolPairs(messages []Message, startIdx int) int {
	if startIdx <= 0 || startIdx >= len(messages) {
		return startIdx
	}
	role := messages[startIdx].Role
	if role == "tool_result" {
		// Include its paired tool_use when possible, but avoid rewinding to zero.
		prev := startIdx - 1
		if prev > 0 && messages[prev].Role == "tool_use" {
			return prev
		}
	}
	if role == "tool_use" {
		// Keep boundary at tool_use so its result (if present) remains in kept range.
		return startIdx
	}
	return startIdx
}

// truncateMessages removes messages before the first kept ID and returns count removed.
func (m *CompactionManager) truncateMessages(sess *Session, firstKeptID string) int {
	if firstKeptID == "" {
		return 0
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
		removed := startIdx
		sess.Messages = sess.Messages[startIdx:]
		return removed
	}
	return 0
}

func (m *CompactionManager) refreshLiveSessionCompactionContext(sessionKey string, activeSess *Session) {
	if m.store == nil {
		return
	}

	sess := activeSess
	if sess == nil && m.lookup != nil {
		sess = m.lookup(sessionKey)
	}
	if sess == nil {
		return
	}

	mode, maxTokens := m.lcmInjectionParams()
	if err := refreshSessionCompactionContext(context.Background(), sess, m.store, sessionKey, mode, maxTokens); err != nil {
		L_warn("lcm: failed to refresh live compaction context", "sessionKey", sessionKey, "error", err)
		return
	}

	estimator := GetTokenEstimator()
	sess.SetTotalTokens(estimator.EstimateSessionTokens(sess))
	L_info("lcm: refreshed live compaction context",
		"sessionKey", sessionKey,
		"messages", len(sess.GetMessages()),
		"totalTokens", sess.GetTotalTokens())
}

func (m *CompactionManager) enforceSummaryOverageCap(summary string, targetTokens int, summaryKind string) string {
	if m == nil || m.config == nil || targetTokens <= 0 {
		return summary
	}

	maxTokens := int(math.Ceil(float64(targetTokens) * m.config.SummaryMaxOverageFactor))
	capped, changed, estimatedTokens := capSummaryToMaxTokens(summary, maxTokens)
	if changed {
		L_warn("lcm: capped oversized summary",
			"kind", summaryKind,
			"targetTokens", targetTokens,
			"maxTokens", maxTokens,
			"estimatedTokens", estimatedTokens)
	}
	return capped
}

func capSummaryToMaxTokens(summary string, maxTokens int) (string, bool, int) {
	estimatedTokens := estimateLCMTextTokens(summary)
	if maxTokens <= 0 || estimatedTokens <= maxTokens {
		return strings.TrimSpace(summary), false, estimatedTokens
	}

	const marker = "[summary truncated by configured overage cap]"
	body := strings.TrimSpace(summary)
	trailer := ""
	if idx := strings.LastIndex(summary, "Expand for details about:"); idx >= 0 {
		body = strings.TrimSpace(summary[:idx])
		trailer = strings.TrimSpace(summary[idx:])
	}

	reservedChars := len(marker) + 4
	if trailer != "" {
		reservedChars += len(trailer) + 2
	}
	bodyBudgetChars := (maxTokens * 4) - reservedChars
	if bodyBudgetChars < 0 {
		bodyBudgetChars = 0
	}
	body = truncateSummaryChars(body, bodyBudgetChars)

	var parts []string
	if body != "" {
		parts = append(parts, body)
	}
	parts = append(parts, marker)
	if trailer != "" {
		parts = append(parts, trailer)
	}

	return strings.Join(parts, "\n\n"), true, estimatedTokens
}

func truncateSummaryChars(text string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= maxChars {
		return strings.TrimSpace(text)
	}

	truncated := strings.TrimSpace(string(runes[:maxChars]))
	if idx := strings.LastIndexAny(truncated, " \n\t"); idx > maxChars/2 {
		truncated = strings.TrimSpace(truncated[:idx])
	}
	return truncated
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
