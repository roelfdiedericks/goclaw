package cron

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// BackupTickInterval is how often we poll even if no file changes or timers fire.
const BackupTickInterval = 5 * time.Minute

// DefaultHeartbeatPrompt is the default prompt sent to the agent during heartbeat.
const DefaultHeartbeatPrompt = `Read HEARTBEAT.md if it exists (workspace context). Follow it strictly. Do not infer or repeat old tasks from prior chats. If nothing needs attention, reply HEARTBEAT_OK.`

// HeartbeatConfig configures the heartbeat system.
type HeartbeatConfig struct {
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
	EnableThinking bool   // If true, enable extended thinking for models that support it
	SkipMirror     bool   // If true, don't mirror to other channels (caller handles delivery)
	JobName        string // Name of the cron job (for status messages)
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
}

// Channel is the interface for delivery channels.
type Channel interface {
	Name() string
	Send(ctx context.Context, msg string) error
}

// ChannelProvider provides access to channels for delivery.
type ChannelProvider interface {
	Channels() map[string]Channel
}

// Service manages cron job scheduling and execution.
type Service struct {
	store           *Store
	gateway         GatewayRunner
	history         *HistoryManager
	channelProvider ChannelProvider

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

	// Heartbeat
	heartbeatConfig *HeartbeatConfig
	heartbeatTimer  *time.Timer
	lastHeartbeat   time.Time
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

// SetChannelProvider sets the channel provider for delivery.
func (s *Service) SetChannelProvider(cp ChannelProvider) {
	s.channelProvider = cp
}

// SetHeartbeatConfig configures the heartbeat system.
func (s *Service) SetHeartbeatConfig(cfg *HeartbeatConfig) {
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
		if err := watcher.Add(jobsDir); err != nil {
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
			if err := s.store.UpdateJob(job); err != nil {
				L_error("cron: failed to clear orphaned state", "job", job.Name, "error", err)
			}
			cleared++
		}
	}

	if cleared > 0 {
		L_info("cron: cleared orphaned running state", "count", cleared)
	}
}

// initializeNextRuns calculates initial next run times for all enabled jobs.
func (s *Service) initializeNextRuns() {
	now := time.Now()
	jobs := s.store.GetEnabledJobs()

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
		job.SetNextRun(next)
		if err := s.store.UpdateJob(job); err != nil {
			L_error("cron: failed to update job", "job", job.Name, "id", job.ID, "error", err)
		}
		if next != nil {
			L_info("cron: job scheduled",
				"job", job.Name,
				"schedule", formatScheduleLog(&job.Schedule),
				"nextRun", next.Format(time.RFC3339),
				"session", job.SessionTarget)
		}
	}

	// Extend ignore window after all writes complete
	s.ignoreWatchUntil = time.Now().Add(200 * time.Millisecond)
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

		L_info("cron: starting job execution", "job", job.Name, "id", job.ID, "prompt", truncateLog(job.Payload.GetPrompt(), 100))
		// Execute in goroutine to not block other jobs
		go s.executeJob(ctx, job)
	}
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

	// Apply job timeout if configured
	if s.jobTimeoutMinutes > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(s.jobTimeoutMinutes)*time.Minute)
		defer cancel()
	}

	L_info("cron: === JOB START ===",
		"job", job.Name,
		"id", job.ID,
		"session", job.SessionTarget,
		"isolated", job.IsIsolated(),
		"timeoutMinutes", s.jobTimeoutMinutes,
		"prompt", truncateLog(job.Payload.GetPrompt(), 200))

	// Build agent request
	sessionID := ""
	if job.IsIsolated() {
		sessionID = fmt.Sprintf("cron:%s", job.ID)
	}

	// Get owner user for cron jobs
	userID := s.gateway.GetOwnerUserID()
	if userID == "" {
		L_error("cron: no owner user configured, cannot run job", "job", job.Name)
		job.SetLastRun(startTime, 0, StatusError, "no owner user configured")
		job.ClearRunning()
		s.store.UpdateJob(job)
		return
	}

	req := AgentRequest{
		Source:       "cron",
		UserMsg:      job.Payload.GetPrompt(),
		FreshContext: job.IsIsolated(),
		SessionID:    sessionID,
		UserID:       userID,
		SkipMirror:   true, // We handle delivery via ch.Send()
		JobName:      job.Name,
	}

	L_debug("cron: invoking agent",
		"job", job.Name,
		"sessionID", sessionID,
		"freshContext", req.FreshContext,
		"userID", userID)

	// Create events channel
	events := make(chan AgentEvent, 100)

	// Run the agent
	go s.gateway.RunAgentForCron(ctx, req, events)

	// Collect results
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

	// Deliver to channels if enabled
	if job.Payload.Deliver && finalContent != "" {
		s.deliverToChannels(ctx, job, finalContent)
	}
}

// deliverToChannels sends the job output to all available channels.
func (s *Service) deliverToChannels(ctx context.Context, job *CronJob, content string) {
	if s.channelProvider == nil {
		L_debug("cron: no channel provider, skipping delivery", "job", job.Name)
		return
	}

	// Note: Suppression tokens (HEARTBEAT_OK, etc.) are handled centrally in gateway.RunAgent
	// If content is empty, nothing to deliver
	if content == "" {
		L_debug("cron: empty content, skipping delivery", "job", job.Name)
		return
	}

	channels := s.channelProvider.Channels()
	if len(channels) == 0 {
		L_debug("cron: no channels available for delivery", "job", job.Name)
		return
	}

	// Format the message
	msg := fmt.Sprintf("**[Cron: %s]**\n\n%s", job.Name, content)

	// Send to all channels
	for name, ch := range channels {
		if err := ch.Send(ctx, msg); err != nil {
			L_error("cron: failed to deliver to channel", "job", job.Name, "channel", name, "error", err)
		} else {
			L_debug("cron: delivered to channel", "job", job.Name, "channel", name)
		}
	}
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
func (s *Service) RunNow(ctx context.Context, id string) error {
	job := s.store.GetJob(id)
	if job == nil {
		return fmt.Errorf("job not found: %s", id)
	}

	go s.executeJob(ctx, job)
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
		IsHeartbeat:  true, // Ephemeral - don't persist to session
		SkipMirror:   true, // We handle delivery via ch.Send()
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

	// Note: Suppression tokens (HEARTBEAT_OK, etc.) are handled centrally in gateway.RunAgent
	// finalContent will be empty if suppressed

	// Deliver response to channels
	if finalContent != "" && s.channelProvider != nil {
		channels := s.channelProvider.Channels()
		if len(channels) > 0 {
			msg := fmt.Sprintf("**[Heartbeat]**\n\n%s", finalContent)
			for name, ch := range channels {
				if err := ch.Send(ctx, msg); err != nil {
					L_error("heartbeat: failed to deliver to channel", "channel", name, "error", err)
				} else {
					L_debug("heartbeat: delivered to channel", "channel", name)
				}
			}
		}
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
