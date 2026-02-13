// Package cron provides scheduled task execution for GoClaw.
package cron

import (
	"encoding/json"
	"time"
)

// CronJob represents a scheduled task (OpenClaw compatible).
type CronJob struct {
	ID             string     `json:"id"`
	AgentID        string     `json:"agentId,omitempty"`
	Name           string     `json:"name"`
	Description    string     `json:"description,omitempty"`
	Enabled        bool       `json:"enabled"`
	CreatedAtMs    int64      `json:"createdAtMs"`
	UpdatedAtMs    int64      `json:"updatedAtMs"`
	Schedule       Schedule   `json:"schedule"`
	SessionTarget  string     `json:"sessionTarget"`      // "main" or "isolated"
	WakeMode       string     `json:"wakeMode,omitempty"` // Legacy, ignored by GoClaw
	Payload        Payload    `json:"payload"`
	DeleteAfterRun bool       `json:"deleteAfterRun,omitempty"`
	Isolation      *Isolation `json:"isolation,omitempty"`
	State          JobState   `json:"state"`
}

// Schedule defines when a job should run.
type Schedule struct {
	Kind    string `json:"kind"`              // "at", "every", "cron"
	AtMs    int64  `json:"atMs,omitempty"`    // for "at": unix ms timestamp
	EveryMs int64  `json:"everyMs,omitempty"` // for "every": interval in ms
	Expr    string `json:"expr,omitempty"`    // for "cron": 5-field cron expression
	Tz      string `json:"tz,omitempty"`      // for "cron": IANA timezone
}

// Payload defines what the job should do.
type Payload struct {
	Kind              string `json:"kind"` // "systemEvent" or "agentTurn"
	Text              string `json:"text,omitempty"`
	Message           string `json:"message,omitempty"`
	Model             string `json:"model,omitempty"`
	Thinking          string `json:"thinking,omitempty"`
	TimeoutSeconds    int    `json:"timeoutSeconds,omitempty"`
	Deliver           bool   `json:"deliver,omitempty"`
	Channel           string `json:"channel,omitempty"`
	To                string `json:"to,omitempty"`
	BestEffortDeliver bool   `json:"bestEffortDeliver,omitempty"`
}

// Isolation controls how isolated job output is posted to main session.
type Isolation struct {
	PostToMainPrefix   string `json:"postToMainPrefix,omitempty"`   // Default: "Cron"
	PostToMainMode     string `json:"postToMainMode,omitempty"`     // "summary" (default) or "full"
	PostToMainMaxChars int    `json:"postToMainMaxChars,omitempty"` // Default: 8000
}

// JobState tracks the runtime state of a job.
type JobState struct {
	NextRunAtMs    *int64 `json:"nextRunAtMs,omitempty"`
	RunningAtMs    *int64 `json:"runningAtMs,omitempty"`
	LastRunAtMs    *int64 `json:"lastRunAtMs,omitempty"`
	LastStatus     string `json:"lastStatus,omitempty"` // "ok", "error"
	LastError      string `json:"lastError,omitempty"`
	LastDurationMs int64  `json:"lastDurationMs,omitempty"`
}

// StoreFile is the root structure of the jobs.json file.
type StoreFile struct {
	Version int        `json:"version"`
	Jobs    []*CronJob `json:"jobs"`
}

// RunLogEntry represents a single run in the history log.
type RunLogEntry struct {
	Ts         int64  `json:"ts"`     // Unix timestamp (ms) when run started
	Status     string `json:"status"` // "ok" or "error"
	DurationMs int64  `json:"durationMs,omitempty"`
	Summary    string `json:"summary,omitempty"` // Agent output, truncated to 2000 chars
	Error      string `json:"error,omitempty"`
}

// Schedule kind constants
const (
	ScheduleKindAt    = "at"
	ScheduleKindEvery = "every"
	ScheduleKindCron  = "cron"
)

// Session target constants
const (
	SessionTargetMain     = "main"
	SessionTargetIsolated = "isolated"
)

// Payload kind constants
const (
	PayloadKindSystemEvent = "systemEvent"
	PayloadKindAgentTurn   = "agentTurn"
)

// Job status constants
const (
	StatusOK    = "ok"
	StatusError = "error"
)

// GetPrompt returns the prompt text from the payload.
func (p *Payload) GetPrompt() string {
	if p.Message != "" {
		return p.Message
	}
	return p.Text
}

// IsIsolated returns true if the job runs in an isolated session.
func (j *CronJob) IsIsolated() bool {
	return j.SessionTarget == SessionTargetIsolated
}

// IsOneShot returns true if this is a one-shot job (at schedule).
func (j *CronJob) IsOneShot() bool {
	return j.Schedule.Kind == ScheduleKindAt
}

// SetNextRun updates the next run time.
func (j *CronJob) SetNextRun(t *time.Time) {
	if t == nil {
		j.State.NextRunAtMs = nil
	} else {
		ms := t.UnixMilli()
		j.State.NextRunAtMs = &ms
	}
}

// SetLastRun updates the last run state.
func (j *CronJob) SetLastRun(startTime time.Time, duration time.Duration, status, errStr string) {
	ms := startTime.UnixMilli()
	j.State.LastRunAtMs = &ms
	j.State.LastDurationMs = duration.Milliseconds()
	j.State.LastStatus = status
	j.State.LastError = errStr
	j.State.RunningAtMs = nil
	j.UpdatedAtMs = time.Now().UnixMilli()
}

// SetRunning marks the job as currently running.
func (j *CronJob) SetRunning() {
	now := time.Now().UnixMilli()
	j.State.RunningAtMs = &now
}

// ClearRunning clears the running state.
func (j *CronJob) ClearRunning() {
	j.State.RunningAtMs = nil
}

// IsRunning returns true if the job is currently running.
func (j *CronJob) IsRunning() bool {
	return j.State.RunningAtMs != nil
}

// Clone creates a deep copy of the job.
func (j *CronJob) Clone() *CronJob {
	data, _ := json.Marshal(j)
	var clone CronJob
	json.Unmarshal(data, &clone)
	return &clone
}
