package cron

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// BackupTickInterval is how often we poll even if no file changes or timers fire.
// This serves as a fallback and can be used for heartbeat infrastructure.
const BackupTickInterval = 5 * time.Minute

// Package-level singleton
var defaultService *Service

// GetService returns the global cron service (may be nil if not started).
func GetService() *Service {
	return defaultService
}

// AgentRequest is the request to run an agent (mirrors gateway.AgentRequest).
type AgentRequest struct {
	Source       string
	UserMsg      string
	SessionID    string
	FreshContext bool
	UserID       string // User ID to run as (typically owner for cron jobs)
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
	GetOwnerUserID() string // Returns the owner user ID for cron jobs
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

	L_info("cron: service stopped")
}

// IsRunning returns true if the service is running.
func (s *Service) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// initializeNextRuns calculates initial next run times for all enabled jobs.
func (s *Service) initializeNextRuns() {
	now := time.Now()
	jobs := s.store.GetEnabledJobs()

	L_info("cron: initializing job schedules", "enabledJobs", len(jobs), "totalJobs", s.store.Count())

	// Suppress file watcher during bulk update
	s.ignoreWatchUntil = time.Now().Add(500 * time.Millisecond)

	for _, job := range jobs {
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

	jobsFile := filepath.Base(s.store.Path())

	// Debounce timer for file changes
	var fileDebounce *time.Timer
	var fileDebounceC <-chan time.Time

	for {
		// Calculate when to wake up next
		sleepDuration := s.computeNextWake()
		L_debug("cron: scheduler sleeping", "duration", sleepDuration)

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
			L_debug("cron: rescheduling due to job add")
			continue

		case event := <-watcherEvents:
			// Only react to writes on the jobs file
			if filepath.Base(event.Name) == jobsFile && (event.Op&fsnotify.Write != 0 || event.Op&fsnotify.Create != 0) {
				// Ignore events caused by our own writes
				if time.Now().Before(s.ignoreWatchUntil) {
					L_debug("cron: ignoring own file write")
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
			// Backup tick - run due jobs and can be used for heartbeat later
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

	L_info("cron: checking due jobs", "count", len(dueJobs), "time", now.Format(time.RFC3339))

	for _, job := range dueJobs {
		if job.IsRunning() {
			L_warn("cron: job already running, skipping", "job", job.Name, "id", job.ID)
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
	
	L_info("cron: === JOB START ===",
		"job", job.Name,
		"id", job.ID,
		"session", job.SessionTarget,
		"isolated", job.IsIsolated(),
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

// GetStatus returns a summary of the cron service status.
func (s *Service) GetStatus() map[string]interface{} {
	return map[string]interface{}{
		"running":      s.IsRunning(),
		"totalJobs":    s.store.Count(),
		"enabledJobs":  s.store.EnabledCount(),
		"jobsFilePath": s.store.Path(),
	}
}
