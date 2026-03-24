package cron

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/google/uuid"
	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/delegatedrun"
	"github.com/roelfdiedericks/goclaw/internal/delivery"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// BackupTickInterval is how often we poll even if no file changes or timers fire.
const BackupTickInterval = 5 * time.Minute

// DefaultHeartbeatPrompt is the default prompt sent to the agent during heartbeat.
const DefaultHeartbeatPrompt = `Read HEARTBEAT.md if it exists (workspace context). Follow it strictly. Do not infer or repeat old tasks from prior chats.
If nothing needs attention, reply exactly: SILENT_OK
GoClaw treats "SILENT_OK" as a heartbeat no-op and suppresses delivery. If something needs attention, do NOT include "SILENT_OK" — reply with the alert text instead.`

// HeartbeatState holds runtime state for the heartbeat system.
// Separate from HeartbeatConfig (JSON config) as it includes runtime fields like WorkspaceDir.
type HeartbeatState struct {
	Enabled         bool
	IntervalMinutes int
	Prompt          string
	WorkspaceDir    string // For checking HEARTBEAT.md
}

// Package-level singleton
var defaultService *Service

// GetService returns the global cron service (may be nil if not started).
func GetService() *Service {
	return defaultService
}

// AgentRequest is the request to run an agent (mirrors gateway.AgentRequest).
type AgentRequest struct {
	Source         string
	UserMsg        string
	SessionID      string
	FreshContext   bool
	UserID         string // User ID to run as (typically owner for cron jobs)
	IsHeartbeat    bool   // If true, run is ephemeral - don't persist to session
	Ephemeral      bool   // If true, skip persistence and roll back in-memory session changes
	EnableThinking bool   // If true, enable extended thinking for models that support it
	SkipMirror     bool   // If true, don't mirror to other channels (caller handles delivery)
	JobName        string // Name of the cron job (for status messages)
	Purpose        string // LLM routing purpose (e.g., "heartbeat", "cron", "subagent"). Empty = "agent"
}

// AgentEvent is a marker interface for agent events.
type AgentEvent interface {
	IsAgentEvent()
}

// AgentEndEvent indicates the agent run completed successfully.
type AgentEndEvent struct {
	FinalText string
}

func (AgentEndEvent) IsAgentEvent() {}

// AgentErrorEvent indicates the agent run failed.
type AgentErrorEvent struct {
	Error string
}

func (AgentErrorEvent) IsAgentEvent() {}

// GatewayRunner is the interface the cron service uses to run agents.
// The gateway must implement this and convert between its types and cron types.
type GatewayRunner interface {
	RunAgentForCron(ctx context.Context, req AgentRequest, events chan<- AgentEvent)
	GetOwnerUserID() string                                   // Returns the owner user ID for cron jobs
	InjectSystemEvent(ctx context.Context, text string) error // Inject system event into primary session
	DeliverAssistantOutput(ctx context.Context, userID string, msg delivery.AssistantMessage) delivery.Report
	DeliverSystemMessage(ctx context.Context, userID string, msg delivery.SystemMessage) delivery.Report
	HandoffCronResult(ctx context.Context, jobName, result string) error
}

type delegatedMessageInjector interface {
	InjectMessage(ctx context.Context, sessionKey, message string, invokeLLM bool, supervisor *user.User) error
}

// Service manages cron job scheduling and execution.
type Service struct {
	store           *Store
	gateway         GatewayRunner
	history         *HistoryManager
	execJob         func(ctx context.Context, job *CronJob)
	runner          delegatedrun.Runner
	registry        delegatedrun.Registry
	delegatedLimits delegatedrun.SpawnLimits

	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
	doneCh  chan struct{}

	timer            *time.Timer       // Timer for next scheduled job
	backupTicker     *time.Ticker      // Backup tick every BackupTickInterval
	watcher          *fsnotify.Watcher // File watcher for jobs.json
	ignoreWatchUntil time.Time         // Ignore watcher events until this time (debounce our own writes)
	rescheduleCh     chan struct{}     // Signal to recalculate wake time (for in-process job adds)

	// Job execution
	jobTimeoutMinutes int // Timeout for job execution (0 = no timeout)
	delegatedEnabled  bool
	delegatedSQLite   *delegatedrun.SQLiteRegistry

	// Heartbeat
	heartbeatConfig *HeartbeatState
	heartbeatTimer  *time.Timer
	lastHeartbeat   time.Time

	// Event subscriptions
	configEventSub bus.SubscriptionID
}

// NewService creates a new cron service and sets it as the global singleton.
func NewService(store *Store, gw GatewayRunner) *Service {
	s := &Service{
		store:   store,
		gateway: gw,
		history: NewHistoryManager(""),
	}
	defaultService = s
	return s
}

// SetDelegatedRunsEnabled toggles cron execution via delegated runner.
// When enabled, runs are executed through delegatedrun.DefaultRunner while
// preserving cron's external behavior.
func (s *Service) SetDelegatedRunsEnabled(enabled bool, sqlitePath string, limits delegatedrun.SpawnLimits) {
	s.delegatedEnabled = enabled
	s.delegatedLimits = limits
	L_info("cron: delegated runs config", "enabled", enabled, "sqlitePath", sqlitePath, "maxSpawnDepth", limits.MaxSpawnDepth, "maxActiveChildrenPerParent", limits.MaxActiveChildrenPerParent, "maxConcurrentRuns", limits.MaxConcurrentRuns)
	if !enabled || s.runner != nil {
		return
	}

	memReg := delegatedrun.NewMemoryRegistry()
	var reg delegatedrun.Registry = memReg
	if sqlitePath != "" {
		sqlReg, err := delegatedrun.NewSQLiteRegistry(sqlitePath)
		if err != nil {
			L_warn("cron: delegated sqlite registry unavailable; using memory only", "path", sqlitePath, "error", err)
		} else {
			s.delegatedSQLite = sqlReg
			reg = delegatedrun.NewCompositeRegistry(memReg, sqlReg)
		}
	}
	emitter := delegatedrun.NewCompositeEmitter(
		delegatedrun.NewRegistryEmitter(reg),
		delegatedrun.NewBusBridgeEmitter(),
	)
	s.registry = reg

	s.runner = delegatedrun.NewDefaultRunnerWithConcurrency(
		func(ctx context.Context, spec delegatedrun.RunSpec) (delegatedrun.RunResult, error) {
			req := AgentRequest{
				Source:         spec.RequesterType,
				UserMsg:        spec.Prompt,
				FreshContext:   spec.FreshContext,
				SessionID:      spec.SessionKey,
				UserID:         spec.UserID,
				Ephemeral:      spec.Ephemeral,
				SkipMirror:     spec.SkipMirror,
				EnableThinking: spec.EnableThinking,
				JobName:        spec.JobName,
				Purpose:        spec.LLMPurpose,
			}

			events := make(chan AgentEvent, 100)
			go s.gateway.RunAgentForCron(ctx, req, events)
			var finalContent string
			var execErr error
			for event := range events {
				switch e := event.(type) {
				case AgentEndEvent:
					finalContent = e.FinalText
				case AgentErrorEvent:
					execErr = fmt.Errorf("%s", e.Error)
				}
			}
			return delegatedrun.RunResult{
				FinalText: finalContent,
				Error:     "",
			}, execErr
		},
		reg,
		emitter,
		limits.MaxConcurrentRuns,
	)
	L_info("cron: delegated runner initialized", "sqliteBacked", s.delegatedSQLite != nil, "laneMaxConcurrentRuns", limits.MaxConcurrentRuns)
}

// ListDelegatedRuns returns delegated run records (newest first if backed by sqlite).
func (s *Service) ListDelegatedRuns() []delegatedrun.RunRecord {
	if s.delegatedSQLite != nil {
		return s.delegatedSQLite.List()
	}
	if s.registry != nil {
		return s.registry.List()
	}
	return nil
}

// StartDelegatedRun starts a delegated run asynchronously and returns run ID.
func (s *Service) StartDelegatedRun(ctx context.Context, spec delegatedrun.RunSpec) (string, error) {
	if s.runner == nil {
		return "", fmt.Errorf("delegated runner not enabled")
	}
	if err := s.prepareDelegatedSpec(&spec); err != nil {
		return "", err
	}
	L_debug("cron: delegated start requested", "requesterType", spec.RequesterType, "requesterID", spec.RequesterID, "parentRunID", spec.ParentRunID, "purpose", spec.Purpose, "llmPurpose", spec.LLMPurpose, "resultMode", spec.ResultMode, "sessionKey", spec.SessionKey)
	if err := s.enforceDelegatedSpawnLimits(spec); err != nil {
		L_warn("cron: delegated start denied", "requesterType", spec.RequesterType, "requesterID", spec.RequesterID, "parentRunID", spec.ParentRunID, "purpose", spec.Purpose, "llmPurpose", spec.LLMPurpose, "error", err)
		return "", err
	}
	runID, err := s.runner.Start(ctx, spec)
	if err != nil {
		L_error("cron: delegated start failed", "requesterType", spec.RequesterType, "requesterID", spec.RequesterID, "parentRunID", spec.ParentRunID, "purpose", spec.Purpose, "llmPurpose", spec.LLMPurpose, "error", err)
		return "", err
	}
	L_info("cron: delegated start accepted", "runID", runID, "requesterType", spec.RequesterType, "parentRunID", spec.ParentRunID, "purpose", spec.Purpose, "llmPurpose", spec.LLMPurpose)
	return runID, nil
}

func (s *Service) prepareDelegatedSpec(spec *delegatedrun.RunSpec) error {
	if strings.TrimSpace(spec.Prompt) == "" {
		return fmt.Errorf("prompt is required")
	}
	if spec.RequesterType == "" {
		spec.RequesterType = "subagent"
	}
	if spec.RequesterID == "" {
		spec.RequesterID = "tool"
	}
	if spec.Purpose == "" {
		spec.Purpose = "subagent"
	}
	if spec.LLMPurpose == "" {
		if strings.TrimSpace(spec.RequesterType) == "cron" {
			spec.LLMPurpose = "cron"
		} else {
			spec.LLMPurpose = "subagent"
		}
	}
	spec.LLMPurpose = normalizeDelegatedLLMPurpose(spec.LLMPurpose)
	if strings.TrimSpace(spec.ResultMode) == "" {
		spec.ResultMode = "store_only"
	}
	delegatedrun.DefaultRequesterBinding(spec, time.Now())
	if spec.ResultMode == "return_to_requester" {
		spec.ExpectsCompletionMessage = true
	}
	if strings.TrimSpace(spec.CleanupState) == "" {
		if spec.ExpectsCompletionMessage {
			spec.CleanupState = "pending"
		} else {
			spec.CleanupState = "none"
		}
	}
	if strings.TrimSpace(spec.DispatchOrder) == "" {
		if spec.ExpectsCompletionMessage {
			spec.DispatchOrder = "direct_first"
		} else {
			spec.DispatchOrder = "queue_first"
		}
	}
	if strings.TrimSpace(spec.FallbackMode) == "" {
		if spec.ExpectsCompletionMessage {
			spec.FallbackMode = "queue_fallback"
		} else {
			spec.FallbackMode = "direct_fallback"
		}
	}
	if strings.TrimSpace(spec.InjectMode) == "" {
		spec.InjectMode = "tool_result"
	}
	if spec.CompletionDispatchSeq <= 0 {
		spec.CompletionDispatchSeq = 1
	}
	if spec.UserID == "" && s.gateway != nil {
		spec.UserID = s.gateway.GetOwnerUserID()
	}
	if spec.UserID == "" {
		return fmt.Errorf("user ID is required")
	}
	if spec.TimeoutSeconds <= 0 && s.delegatedLimits.DefaultTimeoutSeconds > 0 {
		spec.TimeoutSeconds = s.delegatedLimits.DefaultTimeoutSeconds
		L_debug("cron: delegated timeout default applied", "timeoutSeconds", spec.TimeoutSeconds, "requesterType", spec.RequesterType, "purpose", spec.Purpose)
	}
	if s.delegatedLimits.MaxTimeoutSeconds > 0 && spec.TimeoutSeconds > s.delegatedLimits.MaxTimeoutSeconds {
		original := spec.TimeoutSeconds
		spec.TimeoutSeconds = s.delegatedLimits.MaxTimeoutSeconds
		L_warn("cron: delegated timeout capped", "requestedTimeoutSeconds", original, "maxTimeoutSeconds", s.delegatedLimits.MaxTimeoutSeconds, "requesterType", spec.RequesterType, "purpose", spec.Purpose)
	}
	return nil
}

// CreateSyntheticDelegatedCompletion creates a completed delegated run record without starting a new agent/model execution.
func (s *Service) CreateSyntheticDelegatedCompletion(spec delegatedrun.RunSpec, result delegatedrun.RunResult, state delegatedrun.RunState) (string, error) {
	if s.registry == nil {
		return "", fmt.Errorf("delegated registry unavailable")
	}
	if err := s.prepareDelegatedSpec(&spec); err != nil {
		return "", err
	}
	runID := uuid.NewString()
	startedAt := time.Now()
	record := delegatedrun.RunRecord{
		RunID:                        runID,
		ParentRunID:                  spec.ParentRunID,
		RequesterType:                spec.RequesterType,
		RequesterID:                  spec.RequesterID,
		RequesterSessionKey:          spec.RequesterSessionKey,
		RequesterBindingState:        spec.RequesterBindingState,
		RequesterBindingReason:       spec.RequesterBindingReason,
		RequesterBindingUpdatedAt:    spec.RequesterBindingUpdatedAt,
		RequesterBindingLastActiveAt: spec.RequesterBindingLastActiveAt,
		SessionKey:                   spec.SessionKey,
		Purpose:                      spec.Purpose,
		ResultMode:                   spec.ResultMode,
		ExpectsCompletionMessage:     spec.ExpectsCompletionMessage,
		DispatchOrder:                spec.DispatchOrder,
		FallbackMode:                 spec.FallbackMode,
		InjectMode:                   spec.InjectMode,
		CompletionDispatchSeq:        spec.CompletionDispatchSeq,
		CleanupState:                 spec.CleanupState,
		DeferredReason:               spec.DeferredReason,
		ContinuationState:            spec.ContinuationState,
		ContinuationReason:           spec.ContinuationReason,
		State:                        delegatedrun.RunStateQueued,
		StartedAt:                    startedAt,
	}
	if err := s.registry.Create(record); err != nil {
		return "", err
	}
	if err := s.registry.Complete(runID, result, state); err != nil {
		return "", err
	}
	L_info("cron: synthetic delegated completion recorded", "runID", runID, "purpose", spec.Purpose, "state", state)
	return runID, nil
}

// InjectDelegatedSessionMessage injects a message into a delegated run session and can optionally trigger one agent turn.
func (s *Service) InjectDelegatedSessionMessage(ctx context.Context, sessionKey, message string, invokeLLM bool, supervisor *user.User) error {
	if s.gateway == nil {
		return fmt.Errorf("gateway unavailable")
	}
	injector, ok := s.gateway.(delegatedMessageInjector)
	if !ok {
		return fmt.Errorf("gateway does not support delegated session injection")
	}
	return injector.InjectMessage(ctx, sessionKey, message, invokeLLM, supervisor)
}

func normalizeDelegatedLLMPurpose(purpose string) string {
	switch strings.TrimSpace(strings.ToLower(purpose)) {
	case "agent":
		return "agent"
	case "subagent":
		return "subagent"
	case "cron":
		return "cron"
	case "heartbeat":
		return "heartbeat"
	case "hass":
		return "hass"
	case "summarization":
		return "summarization"
	case "embeddings":
		return "embeddings"
	case "memory_extraction", "memoryextraction":
		return "memory_extraction"
	default:
		L_warn("cron: delegated unknown llmPurpose normalized", "requested", purpose, "normalized", "subagent")
		return "subagent"
	}
}

func (s *Service) enforceDelegatedSpawnLimits(spec delegatedrun.RunSpec) error {
	// Preserve cron behavior; apply spawn policy to subagent-style delegation.
	if strings.TrimSpace(spec.RequesterType) == "cron" {
		return nil
	}
	recs := s.ListDelegatedRuns()
	parentRunID := strings.TrimSpace(spec.ParentRunID)
	if parentRunID == "" {
		return nil
	}

	activeChildren := 0
	for _, rec := range recs {
		if rec.ParentRunID == parentRunID && delegatedrun.IsActiveState(rec.State) {
			activeChildren++
		}
	}
	if parentRunID != "" {
		L_trace("cron: delegated spawn limit check", "parentRunID", parentRunID, "activeChildren", activeChildren, "maxActiveChildrenPerParent", s.delegatedLimits.MaxActiveChildrenPerParent, "maxSpawnDepth", s.delegatedLimits.MaxSpawnDepth)
	}
	if s.delegatedLimits.MaxActiveChildrenPerParent > 0 && activeChildren >= s.delegatedLimits.MaxActiveChildrenPerParent {
		return fmt.Errorf("spawn denied: active child limit reached for parent %s (%d)", parentRunID, s.delegatedLimits.MaxActiveChildrenPerParent)
	}

	if s.delegatedLimits.MaxSpawnDepth > 0 {
		parentDepth, err := s.resolveDelegatedDepth(parentRunID)
		if err != nil {
			return err
		}
		childDepth := parentDepth + 1
		if childDepth > s.delegatedLimits.MaxSpawnDepth {
			return fmt.Errorf("spawn denied: maxSpawnDepth exceeded (depth=%d, limit=%d)", childDepth, s.delegatedLimits.MaxSpawnDepth)
		}
	}
	return nil
}

func (s *Service) resolveDelegatedDepth(parentRunID string) (int, error) {
	depth := 0
	seen := map[string]struct{}{}
	cur := strings.TrimSpace(parentRunID)
	for cur != "" {
		if _, ok := seen[cur]; ok {
			return 0, fmt.Errorf("spawn denied: parent run chain has a cycle at %s", cur)
		}
		seen[cur] = struct{}{}
		rec, ok := s.GetDelegatedRun(cur)
		if !ok {
			return 0, fmt.Errorf("spawn denied: parent run not found: %s", cur)
		}
		depth++
		cur = strings.TrimSpace(rec.ParentRunID)
	}
	return depth, nil
}

// WaitDelegatedRun waits for a delegated run to finish or context cancellation.
func (s *Service) WaitDelegatedRun(ctx context.Context, runID string) (delegatedrun.RunResult, delegatedrun.RunState, error) {
	if s.runner == nil {
		return delegatedrun.RunResult{}, "", fmt.Errorf("delegated runner not enabled")
	}
	return s.runner.Wait(ctx, runID)
}

// MarkDelegatedCompletionDispatched stores the completion dispatch idempotency key.
func (s *Service) MarkDelegatedCompletionDispatched(runID, dispatchKey string) error {
	if s.registry == nil {
		return fmt.Errorf("delegated registry unavailable")
	}
	return s.registry.MarkCompletionDispatched(runID, dispatchKey)
}

func (s *Service) ClaimDelegatedCompletionDispatch(runID, claimToken string, seq int) (bool, error) {
	if s.registry == nil {
		return false, fmt.Errorf("delegated registry unavailable")
	}
	return s.registry.ClaimCompletionDispatch(runID, claimToken, seq)
}

func (s *Service) ReleaseDelegatedCompletionDispatch(runID, claimToken string) error {
	if s.registry == nil {
		return fmt.Errorf("delegated registry unavailable")
	}
	return s.registry.ReleaseCompletionDispatch(runID, claimToken)
}

// RecordDelegatedDispatchPhase appends a dispatch phase event for observability.
func (s *Service) RecordDelegatedDispatchPhase(runID, phase, status, detail string) error {
	if s.registry == nil {
		return fmt.Errorf("delegated registry unavailable")
	}
	return s.registry.RecordDispatchPhase(runID, phase, status, detail)
}

// AdvanceDelegatedCompletionDispatchSeq increments completion dispatch sequence for retries.
func (s *Service) AdvanceDelegatedCompletionDispatchSeq(runID string) (int, error) {
	if s.registry == nil {
		return 0, fmt.Errorf("delegated registry unavailable")
	}
	return s.registry.AdvanceCompletionDispatchSeq(runID)
}

// UpdateDelegatedCompletionLifecycle persists cleanup/defer/continuation lifecycle state.
func (s *Service) UpdateDelegatedCompletionLifecycle(runID string, update delegatedrun.CompletionLifecycleUpdate) error {
	if s.registry == nil {
		return fmt.Errorf("delegated registry unavailable")
	}
	return s.registry.UpdateCompletionLifecycle(runID, update)
}

// UpdateDelegatedRequesterBinding persists requester binding focus/idle/age routing metadata.
func (s *Service) UpdateDelegatedRequesterBinding(runID string, update delegatedrun.RequesterBindingUpdate) error {
	if s.registry == nil {
		return fmt.Errorf("delegated registry unavailable")
	}
	return s.registry.UpdateRequesterBinding(runID, update)
}

// GetDelegatedRun returns a specific delegated run by ID.
func (s *Service) GetDelegatedRun(runID string) (delegatedrun.RunRecord, bool) {
	if s.registry == nil {
		return delegatedrun.RunRecord{}, false
	}
	return s.registry.Get(runID)
}

// CancelDelegatedRun cancels an active delegated run by ID.
func (s *Service) CancelDelegatedRun(runID string) error {
	if s.runner == nil {
		return fmt.Errorf("delegated runner not enabled")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return fmt.Errorf("run ID is required")
	}
	L_info("cron: delegated cancel requested", "runID", runID, "cascade", false)
	err := s.runner.Cancel(runID)
	if err != nil {
		L_warn("cron: delegated cancel failed", "runID", runID, "error", err)
		return err
	}
	L_debug("cron: delegated cancel accepted", "runID", runID)
	return nil
}

// KillDelegatedRun performs an immediate hard-stop request for an active delegated run.
// Unlike cancel semantics, this also marks requester direct-delivery binding as unfocused
// so no direct completion delivery is attempted after the hard kill request.
func (s *Service) KillDelegatedRun(runID string) error {
	if s.runner == nil {
		return fmt.Errorf("delegated runner not enabled")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return fmt.Errorf("run ID is required")
	}
	rec, ok := s.GetDelegatedRun(runID)
	if !ok {
		return delegatedrun.ErrRunNotFound
	}
	if !delegatedrun.IsActiveState(rec.State) {
		return fmt.Errorf("run not active: %s", rec.State)
	}
	now := time.Now()
	_ = s.UpdateDelegatedRequesterBinding(runID, delegatedrun.RequesterBindingUpdate{
		State:     delegatedrun.RequesterBindingUnfocused,
		Reason:    "hard_kill_requested",
		UpdatedAt: &now,
	})
	L_info("cron: delegated hard kill requested", "runID", runID)
	err := s.runner.Cancel(runID)
	if err != nil {
		L_warn("cron: delegated hard kill failed", "runID", runID, "error", err)
		return err
	}
	L_debug("cron: delegated hard kill accepted", "runID", runID)
	return nil
}

// CancelDelegatedRunCascade cancels a delegated run and all its descendants.
// Descendants are canceled first to avoid orphan active children.
func (s *Service) CancelDelegatedRunCascade(runID string) error {
	if s.runner == nil {
		return fmt.Errorf("delegated runner not enabled")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return fmt.Errorf("run ID is required")
	}
	if _, ok := s.GetDelegatedRun(runID); !ok {
		return delegatedrun.ErrRunNotFound
	}
	L_info("cron: delegated cancel requested", "runID", runID, "cascade", true)

	recs := s.ListDelegatedRuns()
	childrenByParent := make(map[string][]string)
	for _, rec := range recs {
		parent := strings.TrimSpace(rec.ParentRunID)
		if parent == "" {
			continue
		}
		childrenByParent[parent] = append(childrenByParent[parent], rec.RunID)
	}

	// Collect subtree in parent-first order, then cancel in reverse for child-first semantics.
	order := []string{runID}
	for i := 0; i < len(order); i++ {
		id := order[i]
		order = append(order, childrenByParent[id]...)
	}
	L_debug("cron: delegated cascade subtree collected", "rootRunID", runID, "subtreeSize", len(order))

	var firstErr error
	for i := len(order) - 1; i >= 0; i-- {
		id := order[i]
		if err := s.runner.Cancel(id); err != nil {
			// Non-active descendants may already be finished and return not found; ignore.
			if errors.Is(err, delegatedrun.ErrRunNotFound) {
				L_trace("cron: delegated cascade skip non-active child", "runID", id)
				continue
			}
			L_warn("cron: delegated cascade cancel failed", "runID", id, "error", err)
			if firstErr == nil {
				firstErr = err
			}
		} else {
			L_trace("cron: delegated cascade cancel accepted", "runID", id)
		}
	}
	if firstErr != nil {
		L_warn("cron: delegated cascade completed with errors", "rootRunID", runID, "error", firstErr)
	} else {
		L_info("cron: delegated cascade completed", "rootRunID", runID, "subtreeSize", len(order))
	}
	return firstErr
}

// ListDelegatedRunEvents returns delegated run events after the given event ID.
func (s *Service) ListDelegatedRunEvents(sinceID int64, limit int) []delegatedrun.RunEvent {
	if s.delegatedSQLite == nil {
		return nil
	}
	return s.delegatedSQLite.ListEventsSince(sinceID, limit)
}

// SetHeartbeatConfig configures the heartbeat system.
func (s *Service) SetHeartbeatConfig(cfg *HeartbeatState) {
	s.heartbeatConfig = cfg
}

// SetJobTimeout sets the job execution timeout in minutes (0 = no timeout).
func (s *Service) SetJobTimeout(minutes int) {
	s.jobTimeoutMinutes = minutes
}

// TriggerHeartbeatNow manually triggers a heartbeat check (for /heartbeat command)
// Uses background context since heartbeat runs independently of the caller
func (s *Service) TriggerHeartbeatNow(_ context.Context) error {
	if s.heartbeatConfig == nil || !s.heartbeatConfig.Enabled {
		return fmt.Errorf("heartbeat not enabled")
	}
	go s.runHeartbeat(context.Background())
	return nil
}

// Wake injects a system event into the primary session and optionally triggers heartbeat.
// mode can be "now" (trigger heartbeat immediately) or "next-heartbeat" (wait for scheduled).
func (s *Service) Wake(ctx context.Context, text string, mode string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("wake text is required")
	}

	// Inject system event
	if s.gateway != nil {
		if err := s.gateway.InjectSystemEvent(ctx, text); err != nil {
			return fmt.Errorf("failed to inject system event: %w", err)
		}
		L_info("cron: wake event injected", "mode", mode, "textLen", len(text))
	}

	// If mode is "now", trigger heartbeat immediately
	if mode == "now" {
		if s.heartbeatConfig != nil && s.heartbeatConfig.Enabled {
			go s.runHeartbeat(context.Background())
			L_debug("cron: wake triggered immediate heartbeat")
		} else {
			L_debug("cron: wake mode=now but heartbeat not enabled")
		}
	}

	return nil
}

// Start begins the cron scheduler.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("cron service already running")
	}
	s.running = true
	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	s.rescheduleCh = make(chan struct{}, 1) // Buffered so sends don't block
	s.mu.Unlock()

	// Load jobs from store
	if err := s.store.Load(); err != nil {
		return fmt.Errorf("failed to load cron jobs: %w", err)
	}

	// Clear stale running state from previous process
	// Any jobs marked as "running" are orphaned - the previous process died
	s.clearOrphanedRunningState()

	// Set up file watcher on jobs.json
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		L_warn("cron: failed to create file watcher, external changes won't be detected", "error", err)
	} else {
		s.watcher = watcher
		// Watch the directory containing jobs.json (fsnotify watches dirs better than files)
		jobsDir := filepath.Dir(s.store.Path())
		// Ensure directory exists before watching (fsnotify can't watch non-existent dirs)
		if err := os.MkdirAll(jobsDir, 0750); err != nil {
			L_warn("cron: failed to create jobs directory", "dir", jobsDir, "error", err)
		} else if err := watcher.Add(jobsDir); err != nil {
			L_warn("cron: failed to watch jobs directory", "dir", jobsDir, "error", err)
		} else {
			L_debug("cron: watching for job file changes", "dir", jobsDir)
		}
	}

	// Set up backup ticker
	s.backupTicker = time.NewTicker(BackupTickInterval)

	// Set up heartbeat timer if enabled
	if s.heartbeatConfig != nil && s.heartbeatConfig.Enabled && s.heartbeatConfig.IntervalMinutes > 0 {
		interval := time.Duration(s.heartbeatConfig.IntervalMinutes) * time.Minute
		s.heartbeatTimer = time.NewTimer(interval)
		L_info("cron: heartbeat enabled", "interval", interval)
	}

	// Initialize next run times for all jobs
	s.initializeNextRuns()

	L_info("cron: service started", "jobs", s.store.EnabledCount(), "backupInterval", BackupTickInterval)

	go s.runLoop(ctx)
	return nil
}

// Stop gracefully stops the cron scheduler.
func (s *Service) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	close(s.stopCh)
	s.mu.Unlock()

	// Wait for run loop to finish
	<-s.doneCh

	// Clean up watcher and ticker
	if s.watcher != nil {
		s.watcher.Close()
		s.watcher = nil
	}
	if s.backupTicker != nil {
		s.backupTicker.Stop()
		s.backupTicker = nil
	}
	if s.delegatedSQLite != nil {
		_ = s.delegatedSQLite.Close()
		s.delegatedSQLite = nil
	}
	if s.heartbeatTimer != nil {
		s.heartbeatTimer.Stop()
		s.heartbeatTimer = nil
	}

	L_info("cron: service stopped")
}

// IsRunning returns true if the service is running.
func (s *Service) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// RegisterOperationalCommands registers runtime commands and event subscriptions.
func (s *Service) RegisterOperationalCommands() {
	bus.RegisterCommand("cron", "list", s.handleList)
	bus.RegisterCommand("cron", "run", s.handleRun)

	// Subscribe to config changes
	s.configEventSub = bus.SubscribeEvent("cron.config.applied", s.onConfigApplied)

	L_debug("cron: operational commands registered")
}

// UnregisterOperationalCommands removes commands and subscriptions.
func (s *Service) UnregisterOperationalCommands() {
	bus.UnregisterCommand("cron", "list")
	bus.UnregisterCommand("cron", "run")
	if s.configEventSub != 0 {
		bus.UnsubscribeEvent(s.configEventSub)
		s.configEventSub = 0
	}
}

// onConfigApplied handles cron config changes
func (s *Service) onConfigApplied(e bus.Event) {
	cfg, ok := e.Data.(CronConfig)
	if !ok {
		cfgPtr, okPtr := e.Data.(*CronConfig)
		if okPtr {
			cfg = *cfgPtr
			ok = true
		}
	}
	if !ok {
		L_error("cron: invalid config event data type", "type", fmt.Sprintf("%T", e.Data))
		return
	}

	L_info("cron: config changed", "enabled", cfg.Enabled, "heartbeat", cfg.Heartbeat.Enabled)

	// Update job timeout
	s.jobTimeoutMinutes = cfg.JobTimeoutMinutes

	// Update heartbeat config
	if s.heartbeatConfig != nil {
		s.heartbeatConfig.Enabled = cfg.Heartbeat.Enabled
		s.heartbeatConfig.IntervalMinutes = cfg.Heartbeat.IntervalMinutes
		if cfg.Heartbeat.Prompt != "" {
			s.heartbeatConfig.Prompt = cfg.Heartbeat.Prompt
		}
	}
}

// handleList returns a list of scheduled jobs
func (s *Service) handleList(cmd bus.Command) bus.CommandResult {
	jobs := s.store.GetAllJobs()
	var names []string
	for _, j := range jobs {
		status := ""
		if !j.Enabled {
			status = " (disabled)"
		}
		sched := j.Schedule.Kind
		if j.Schedule.Expr != "" {
			sched = j.Schedule.Expr
		}
		names = append(names, fmt.Sprintf("%s: %s%s", j.Name, sched, status))
	}

	return bus.CommandResult{
		Success: true,
		Message: fmt.Sprintf("%d scheduled jobs", len(jobs)),
		Data:    names,
	}
}

// handleRun forces execution of a named job
func (s *Service) handleRun(cmd bus.Command) bus.CommandResult {
	jobName, ok := cmd.Payload.(string)
	if !ok {
		return bus.CommandResult{
			Error:   fmt.Errorf("expected job name string, got %T", cmd.Payload),
			Message: "invalid payload type",
		}
	}

	job := s.store.GetJob(jobName)
	if job == nil {
		return bus.CommandResult{
			Success: false,
			Message: fmt.Sprintf("Job not found: %s", jobName),
		}
	}

	// Run the job asynchronously
	s.launchJob(context.Background(), job)

	return bus.CommandResult{
		Success: true,
		Message: fmt.Sprintf("Job '%s' started", jobName),
	}
}

// clearOrphanedRunningState clears running state from jobs that were "running"
// in a previous process. At startup, no jobs can actually be running.
func (s *Service) clearOrphanedRunningState() {
	jobs := s.store.GetAllJobs()
	cleared := 0

	for _, job := range jobs {
		if job.IsRunning() {
			L_warn("cron: clearing orphaned running state", "job", job.Name, "id", job.ID)
			job.ClearRunning()
			// Also clear NextRunAtMs - it will be recalculated by initializeNextRuns
			job.SetNextRun(nil)
			cleared++
		}
	}

	if cleared > 0 {
		if err := s.store.Save(); err != nil {
			L_error("cron: failed to persist cleared orphaned state", "count", cleared, "error", err)
		}
		L_info("cron: cleared orphaned running state", "count", cleared)
	}
}

// initializeNextRuns calculates initial next run times for all enabled jobs.
func (s *Service) initializeNextRuns() {
	now := time.Now()
	jobs := s.store.GetEnabledJobs()
	changed := false

	L_info("cron: initializing job schedules", "enabledJobs", len(jobs), "totalJobs", s.store.Count())

	// Suppress file watcher during bulk update
	s.ignoreWatchUntil = time.Now().Add(500 * time.Millisecond)

	for _, job := range jobs {
		// Skip jobs that are currently running - don't reset their NextRunAtMs
		// Otherwise we'd create a tight loop (job overdue but running)
		if job.IsRunning() {
			L_debug("cron: skipping running job during init", "job", job.Name)
			continue
		}

		next, err := NextRunTime(job, now)
		if err != nil {
			L_error("cron: failed to calculate next run", "job", job.Name, "id", job.ID, "error", err)
			continue
		}
		if nextRunChanged(job.State.NextRunAtMs, next) {
			changed = true
		}
		job.SetNextRun(next)
		if next != nil {
			L_trace("cron: job scheduled",
				"job", job.Name,
				"schedule", formatScheduleLog(&job.Schedule),
				"nextRun", next.Format(time.RFC3339),
				"resultMode", job.ResultMode())
		}
	}

	if changed {
		if err := s.store.Save(); err != nil {
			L_error("cron: failed to persist initialized schedules", "error", err)
		}
	}

	// Extend ignore window after all writes complete
	s.ignoreWatchUntil = time.Now().Add(200 * time.Millisecond)
}

func nextRunChanged(current *int64, next *time.Time) bool {
	switch {
	case current == nil && next == nil:
		return false
	case current == nil || next == nil:
		return true
	default:
		return *current != next.UnixMilli()
	}
}

func formatScheduleLog(s *Schedule) string {
	switch s.Kind {
	case ScheduleKindAt:
		return fmt.Sprintf("at %s", time.UnixMilli(s.AtMs).Format(time.RFC3339))
	case ScheduleKindEvery:
		return fmt.Sprintf("every %s", time.Duration(s.EveryMs)*time.Millisecond)
	case ScheduleKindCron:
		if s.Tz != "" {
			return fmt.Sprintf("cron '%s' (%s)", s.Expr, s.Tz)
		}
		return fmt.Sprintf("cron '%s'", s.Expr)
	default:
		return "unknown"
	}
}

// FileChangeDebounce is how long to wait after a file change before reloading.
// This allows multiple rapid writes (e.g., from another process) to settle.
const FileChangeDebounce = 150 * time.Millisecond

// runLoop is the main scheduler loop.
func (s *Service) runLoop(ctx context.Context) {
	defer close(s.doneCh)

	// Get watcher channels (may be nil if watcher failed to create)
	var watcherEvents <-chan fsnotify.Event
	var watcherErrors <-chan error
	if s.watcher != nil {
		watcherEvents = s.watcher.Events
		watcherErrors = s.watcher.Errors
	}

	// Get heartbeat timer channel (may be nil if heartbeat disabled)
	var heartbeatC <-chan time.Time
	if s.heartbeatTimer != nil {
		heartbeatC = s.heartbeatTimer.C
	}

	jobsFile := filepath.Base(s.store.Path())

	// Debounce timer for file changes
	var fileDebounce *time.Timer
	var fileDebounceC <-chan time.Time

	for {
		// Calculate when to wake up next
		sleepDuration := s.computeNextWake()
		L_trace("cron: scheduler sleeping", "duration", sleepDuration)

		if s.timer == nil {
			s.timer = time.NewTimer(sleepDuration)
		} else {
			s.timer.Reset(sleepDuration)
		}

		select {
		case <-ctx.Done():
			s.timer.Stop()
			if fileDebounce != nil {
				fileDebounce.Stop()
			}
			return
		case <-s.stopCh:
			s.timer.Stop()
			if fileDebounce != nil {
				fileDebounce.Stop()
			}
			return

		case <-s.rescheduleCh:
			// In-process job add, just recalculate wake time
			s.timer.Stop()
			L_trace("cron: rescheduling due to job add")
			continue

		case <-heartbeatC:
			// Heartbeat timer fired
			s.timer.Stop()
			go s.runHeartbeat(ctx)
			// Reset heartbeat timer for next interval
			if s.heartbeatConfig != nil && s.heartbeatConfig.IntervalMinutes > 0 {
				interval := time.Duration(s.heartbeatConfig.IntervalMinutes) * time.Minute
				s.heartbeatTimer.Reset(interval)
			}

		case event := <-watcherEvents:
			// Only react to writes on the jobs file
			if filepath.Base(event.Name) == jobsFile && (event.Op&fsnotify.Write != 0 || event.Op&fsnotify.Create != 0) {
				// Ignore events caused by our own writes
				if time.Now().Before(s.ignoreWatchUntil) {
					L_trace("cron: ignoring own file write")
					continue
				}
				// Start/reset debounce timer - wait for writes to settle
				if fileDebounce == nil {
					fileDebounce = time.NewTimer(FileChangeDebounce)
					fileDebounceC = fileDebounce.C
					L_debug("cron: file change detected, debouncing")
				} else {
					fileDebounce.Reset(FileChangeDebounce)
					L_debug("cron: file change detected, extending debounce")
				}
			}

		case <-fileDebounceC:
			// Debounce period elapsed, now reload
			s.timer.Stop()
			fileDebounce = nil
			fileDebounceC = nil
			L_info("cron: reloading jobs after file change")
			if err := s.store.Load(); err != nil {
				L_error("cron: failed to reload jobs after file change", "error", err)
			} else {
				s.initializeNextRuns()
			}

		case err := <-watcherErrors:
			L_warn("cron: file watcher error", "error", err)

		case <-s.backupTicker.C:
			// Backup tick - run due jobs
			s.timer.Stop()
			L_debug("cron: backup tick fired")
			s.runDueJobs(ctx)

		case <-s.timer.C:
			s.runDueJobs(ctx)
		}
	}
}

// computeNextWake returns how long to sleep until the next job is due.
func (s *Service) computeNextWake() time.Duration {
	now := time.Now()
	minWait := 1 * time.Hour // Max sleep time

	for _, job := range s.store.GetEnabledJobs() {
		if job.State.NextRunAtMs == nil {
			continue
		}
		nextRun := time.UnixMilli(*job.State.NextRunAtMs)
		wait := nextRun.Sub(now)
		if wait < 0 {
			// Job is overdue, run immediately
			return 0
		}
		if wait < minWait {
			minWait = wait
		}
	}

	// Add a small buffer to avoid timing edge cases
	if minWait > 100*time.Millisecond {
		return minWait
	}
	return 100 * time.Millisecond
}

// runDueJobs executes all jobs that are due.
func (s *Service) runDueJobs(ctx context.Context) {
	now := time.Now()
	dueJobs := s.store.GetDueJobs(now)

	if len(dueJobs) == 0 {
		return
	}

	L_debug("cron: checking due jobs", "count", len(dueJobs))

	for _, job := range dueJobs {
		if job.IsRunning() {
			L_debug("cron: job already running, skipping", "job", job.Name)
			continue
		}

		// IMPORTANT: Clear nextRunAtMs immediately to prevent re-triggering
		// before the goroutine can mark it as running
		job.SetNextRun(nil)
		job.SetRunning()
		if err := s.store.UpdateJob(job); err != nil {
			L_error("cron: failed to mark job starting", "job", job.Name, "error", err)
			continue
		}

		L_info("cron: starting job execution", "job", job.Name, "id", job.ID, "prompt", truncateLog(job.Prompt, 100))
		// Execute in goroutine to not block other jobs
		s.launchJob(ctx, job)
	}
}

func (s *Service) launchJob(ctx context.Context, job *CronJob) {
	runner := s.executeJob
	if s.execJob != nil {
		runner = s.execJob
	}
	go runner(ctx, job)
}

func truncateLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// executeJob runs a single cron job.
// Note: job is already marked as running by runDueJobs before this is called.
func (s *Service) executeJob(ctx context.Context, job *CronJob) {
	startTime := time.Now()

	timeout := time.Duration(0)
	if job.Result.TimeoutSeconds > 0 {
		timeout = time.Duration(job.Result.TimeoutSeconds) * time.Second
	} else if s.jobTimeoutMinutes > 0 {
		timeout = time.Duration(s.jobTimeoutMinutes) * time.Minute
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	L_info("cron: === JOB START ===",
		"job", job.Name,
		"id", job.ID,
		"resultMode", job.ResultMode(),
		"persist", job.ShouldPersistResult(),
		"timeout", timeout,
		"prompt", truncateLog(job.Prompt, 200))

	// Get owner user for cron jobs
	userID := s.gateway.GetOwnerUserID()
	if userID == "" {
		L_error("cron: no owner user configured, cannot run job", "job", job.Name)
		job.SetLastRun(startTime, 0, StatusError, "no owner user configured")
		job.ClearRunning()
		if err := s.store.UpdateJob(job); err != nil {
			L_warn("cron: failed to update job after error", "job", job.Name, "error", err)
		}
		return
	}

	finalContent := ""
	eventCount := 0
	var execErr error
	if s.delegatedEnabled && s.runner != nil {
		finalContent, eventCount, execErr = s.runDelegatedTask(ctx, job, userID)
	} else {
		finalContent, eventCount, execErr = s.runAssistantTask(ctx, job, userID)
	}

	duration := time.Since(startTime)

	// Update job state
	status := StatusOK
	errStr := ""
	if execErr != nil {
		status = StatusError
		errStr = execErr.Error()
		L_error("cron: === JOB FAILED ===",
			"job", job.Name,
			"id", job.ID,
			"error", execErr,
			"duration", duration,
			"events", eventCount)
	} else {
		L_info("cron: === JOB COMPLETED ===",
			"job", job.Name,
			"id", job.ID,
			"duration", duration,
			"responseLen", len(finalContent),
			"events", eventCount)
	}

	job.SetLastRun(startTime, duration, status, errStr)

	// Log run to history
	entry := CreateRunEntry(startTime, duration, status, finalContent, errStr)
	if err := s.history.LogRun(job.ID, entry); err != nil {
		L_error("cron: failed to log run", "job", job.Name, "error", err)
	}

	// Calculate next run time
	if job.IsOneShot() {
		// One-shot job: disable after run
		job.Enabled = false
		job.SetNextRun(nil)
		L_info("cron: one-shot job completed and disabled", "job", job.Name, "id", job.ID)
	} else {
		// Recurring job: calculate next run
		next, err := NextRunTime(job, time.Now())
		if err != nil {
			L_error("cron: failed to calculate next run", "job", job.Name, "error", err)
		}
		job.SetNextRun(next)
		if next != nil {
			L_info("cron: next run scheduled", "job", job.Name, "nextRun", next.Format(time.RFC3339))
		}
	}

	if err := s.store.UpdateJob(job); err != nil {
		L_error("cron: failed to save job state", "job", job.Name, "error", err)
	}

	if execErr == nil {
		if err := s.handleResult(ctx, job, finalContent); err != nil {
			L_error("cron: result handling failed", "job", job.Name, "error", err)
		}
	}
}

func (s *Service) runAssistantTask(ctx context.Context, job *CronJob, userID string) (string, int, error) {
	req := AgentRequest{
		Source:       "cron",
		UserMsg:      job.Prompt,
		FreshContext: true,
		SessionID:    fmt.Sprintf("cron:%s", job.ID),
		UserID:       userID,
		Ephemeral:    !job.ShouldPersistResult(),
		SkipMirror:   true,
		JobName:      job.Name,
		Purpose:      "cron",
	}

	L_debug("cron: invoking agent",
		"job", job.Name,
		"sessionID", req.SessionID,
		"freshContext", req.FreshContext,
		"ephemeral", req.Ephemeral,
		"userID", userID)

	events := make(chan AgentEvent, 100)
	go s.gateway.RunAgentForCron(ctx, req, events)

	var finalContent string
	var execErr error
	eventCount := 0
	for event := range events {
		eventCount++
		switch e := event.(type) {
		case AgentEndEvent:
			finalContent = e.FinalText
			L_debug("cron: received agent end event", "job", job.Name, "contentLen", len(finalContent))
		case AgentErrorEvent:
			execErr = fmt.Errorf("%s", e.Error)
			L_error("cron: received agent error event", "job", job.Name, "error", e.Error)
		}
	}
	return finalContent, eventCount, execErr
}

func (s *Service) runDelegatedTask(ctx context.Context, job *CronJob, userID string) (string, int, error) {
	timeoutSeconds := 0
	if job.Result.TimeoutSeconds > 0 {
		timeoutSeconds = job.Result.TimeoutSeconds
	} else if s.jobTimeoutMinutes > 0 {
		timeoutSeconds = s.jobTimeoutMinutes * 60
	}
	spec := delegatedrun.RunSpec{
		ParentRunID:    "",
		RequesterType:  "cron",
		RequesterID:    job.ID,
		SessionKey:     fmt.Sprintf("cron:%s", job.ID),
		Prompt:         job.Prompt,
		Purpose:        "cron",
		LLMPurpose:     "cron",
		FreshContext:   true,
		Ephemeral:      !job.ShouldPersistResult(),
		TimeoutSeconds: timeoutSeconds,
		UserID:         userID,
		EnableThinking: false,
		SkipMirror:     true,
		JobName:        job.Name,
	}
	runID, err := s.StartDelegatedRun(ctx, spec)
	if err != nil {
		return "", 0, err
	}
	result, state, waitErr := s.runner.Wait(ctx, runID)
	if waitErr != nil {
		return result.FinalText, 0, waitErr
	}
	switch state {
	case delegatedrun.RunStateCompleted:
		return result.FinalText, 0, nil
	case delegatedrun.RunStateTimeout:
		return result.FinalText, 0, fmt.Errorf("delegated run timed out")
	case delegatedrun.RunStateCanceled:
		return result.FinalText, 0, fmt.Errorf("delegated run canceled")
	default:
		if result.Error != "" {
			return result.FinalText, 0, fmt.Errorf("%s", result.Error)
		}
		return result.FinalText, 0, fmt.Errorf("delegated run failed")
	}
}

func (s *Service) handleResult(ctx context.Context, job *CronJob, finalContent string) error {
	switch job.ResultMode() {
	case ResultModeStoreOnly:
		L_debug("cron: result stored only", "job", job.Name, "responseLen", len(finalContent))
		return nil
	case ResultModeDeliver:
		s.deliverAssistantOutput(ctx, "cron", finalContent, job.ShouldPersistResult())
		return nil
	case ResultModeHandoffMain:
		if finalContent == "" {
			L_debug("cron: handoff skipped for empty result", "job", job.Name)
			return nil
		}
		if s.gateway == nil {
			return fmt.Errorf("gateway unavailable for handoff")
		}
		return s.gateway.HandoffCronResult(ctx, job.Name, finalContent)
	default:
		return fmt.Errorf("unsupported result mode: %s", job.ResultMode())
	}
}

func (s *Service) deliverAssistantOutput(ctx context.Context, source, content string, persist bool) {
	// Note: silent/no-op tokens are handled centrally in gateway.RunAgent.
	// If content is empty, nothing to deliver
	if content == "" {
		L_debug("cron: empty assistant output, skipping delivery", "source", source)
		return
	}
	if s.gateway == nil {
		L_debug("cron: no gateway available for assistant delivery", "source", source)
		return
	}
	userID := s.gateway.GetOwnerUserID()
	if userID == "" {
		L_warn("cron: no owner user configured for assistant delivery", "source", source)
		return
	}
	report := s.gateway.DeliverAssistantOutput(ctx, userID, delivery.AssistantMessage{
		Source:         source,
		Content:        content,
		Persist:        persist,
		PersistKind:    "delivered",
		PersistContent: content,
	})
	if report.Delivered() {
		L_info("cron: assistant delivery succeeded", "source", source, "deliveredTo", report.DeliveredTo)
		return
	}
	L_warn("cron: assistant delivery produced no successful channels", "source", source)
}

// Store returns the underlying store.
func (s *Service) Store() *Store {
	return s.store
}

// History returns the history manager.
func (s *Service) History() *HistoryManager {
	return s.history
}

// AddJob adds a new job and schedules it.
func (s *Service) AddJob(job *CronJob) error {
	// Calculate initial next run
	next, err := NextRunTime(job, time.Now())
	if err != nil {
		return fmt.Errorf("invalid schedule: %w", err)
	}
	job.SetNextRun(next)

	// Suppress file watcher for our own write
	s.ignoreWatchUntil = time.Now().Add(200 * time.Millisecond)

	if err := s.store.AddJob(job); err != nil {
		return err
	}

	L_info("cron: job added", "job", job.Name, "id", job.ID, "nextRun", next)

	// Wake scheduler to recalculate
	s.triggerReschedule()
	return nil
}

// triggerReschedule signals the scheduler to recalculate its next wake time.
func (s *Service) triggerReschedule() {
	select {
	case s.rescheduleCh <- struct{}{}:
	default:
		// Already has pending signal
	}
}

// RemoveJob removes a job.
func (s *Service) RemoveJob(id string) error {
	return s.store.DeleteJob(id)
}

// RunNow triggers immediate execution of a job.
// The run is detached from the caller context so chat/tool request cancellation
// does not kill the scheduled task after it has been accepted.
func (s *Service) RunNow(_ context.Context, id string) error {
	job := s.store.GetJob(id)
	if job == nil {
		return fmt.Errorf("job not found: %s", id)
	}

	s.launchJob(context.Background(), job)
	return nil
}

// runHeartbeat executes the periodic heartbeat check.
func (s *Service) runHeartbeat(ctx context.Context) {
	if s.heartbeatConfig == nil || !s.heartbeatConfig.Enabled {
		return
	}

	s.lastHeartbeat = time.Now()
	L_info("heartbeat: starting")

	// Check if HEARTBEAT.md has content
	if s.heartbeatConfig.WorkspaceDir != "" {
		heartbeatFile := filepath.Join(s.heartbeatConfig.WorkspaceDir, "HEARTBEAT.md")
		content, err := os.ReadFile(heartbeatFile)
		if err != nil {
			if os.IsNotExist(err) {
				L_debug("heartbeat: HEARTBEAT.md not found, skipping", "path", heartbeatFile)
				return
			}
			L_warn("heartbeat: failed to read HEARTBEAT.md", "error", err)
			// Continue anyway - the agent might handle it
		} else {
			// Check if file is effectively empty (only comments/whitespace)
			trimmed := strings.TrimSpace(string(content))
			lines := strings.Split(trimmed, "\n")
			hasContent := false
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line != "" && !strings.HasPrefix(line, "#") {
					hasContent = true
					break
				}
			}
			if !hasContent {
				L_debug("heartbeat: HEARTBEAT.md is empty, skipping")
				return
			}
		}
	}

	// Get owner user for heartbeat
	userID := s.gateway.GetOwnerUserID()
	if userID == "" {
		L_error("heartbeat: no owner user configured")
		return
	}

	// Build the prompt
	prompt := s.heartbeatConfig.Prompt
	if prompt == "" {
		prompt = DefaultHeartbeatPrompt
	}

	// Run on main session (not isolated), but ephemeral (don't persist)
	req := AgentRequest{
		Source:       "heartbeat",
		UserMsg:      prompt,
		FreshContext: false, // Use main session with history (for reading)
		SessionID:    "",    // Empty = main session
		UserID:       userID,
		IsHeartbeat:  true,        // Ephemeral - don't persist to session
		SkipMirror:   true,        // Delivery handled via gateway non-conversation seam
		Purpose:      "heartbeat", // Use heartbeat model chain (falls back to agent)
	}

	L_debug("heartbeat: invoking agent", "prompt", truncateLog(prompt, 100))

	events := make(chan AgentEvent, 100)
	go s.gateway.RunAgentForCron(ctx, req, events)

	// Collect response
	var finalContent string
	for event := range events {
		switch e := event.(type) {
		case AgentEndEvent:
			finalContent = e.FinalText
		case AgentErrorEvent:
			L_error("heartbeat: agent error", "error", e.Error)
			return
		}
	}

	L_info("heartbeat: completed", "responseLen", len(finalContent))

	// Note: silent/no-op tokens are handled centrally in gateway.RunAgent.
	// finalContent will be empty if suppressed

	// Deliver response to channels
	if finalContent != "" {
		s.deliverAssistantOutput(ctx, "heartbeat", finalContent, true)
	}
}

// GetStatus returns a summary of the cron service status.
func (s *Service) GetStatus() map[string]interface{} {
	return map[string]interface{}{
		"running":      s.IsRunning(),
		"totalJobs":    s.store.Count(),
		"enabledJobs":  s.store.EnabledCount(),
		"jobsFilePath": s.store.Path(),
	}
}
