// Package cron provides scheduled task execution for GoClaw.
package cron

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// CronJob represents a scheduled assistant task.
type CronJob struct {
	ID             string       `json:"id"`
	AgentID        string       `json:"agentId,omitempty"`
	Name           string       `json:"name"`
	Description    string       `json:"description,omitempty"`
	Enabled        bool         `json:"enabled"`
	CreatedAtMs    int64        `json:"createdAtMs"`
	UpdatedAtMs    int64        `json:"updatedAtMs"`
	Schedule       Schedule     `json:"schedule"`
	Prompt         string       `json:"prompt"`
	Result         ResultPolicy `json:"result"`
	DeleteAfterRun bool         `json:"deleteAfterRun,omitempty"`
	State          JobState     `json:"state"`
}

// Schedule defines when a job should run.
type Schedule struct {
	Kind    string `json:"kind"`              // "at", "every", "cron"
	AtMs    int64  `json:"atMs,omitempty"`    // for "at": unix ms timestamp
	EveryMs int64  `json:"everyMs,omitempty"` // for "every": interval in ms
	Expr    string `json:"expr,omitempty"`    // for "cron": 5-field cron expression
	Tz      string `json:"tz,omitempty"`      // for "cron": IANA timezone
}

type ResultMode string

const (
	ResultModeStoreOnly   ResultMode = "store_only"
	ResultModeDeliver     ResultMode = "deliver"
	ResultModeHandoffMain ResultMode = "handoff_main"
)

// ResultPolicy controls what happens after the assistant task completes.
type ResultPolicy struct {
	Mode           ResultMode `json:"mode"`
	Persist        *bool      `json:"persist,omitempty"` // nil = use smart default
	Channel        string     `json:"channel,omitempty"`
	To             string     `json:"to,omitempty"`
	BestEffort     bool       `json:"bestEffort,omitempty"`
	TimeoutSeconds int        `json:"timeoutSeconds,omitempty"`
	Model          string     `json:"model,omitempty"`
	Thinking       string     `json:"thinking,omitempty"`
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

// Job status constants
const (
	StatusOK    = "ok"
	StatusError = "error"
)

func (j *CronJob) ResultMode() ResultMode {
	mode := ResultMode(strings.TrimSpace(string(j.Result.Mode)))
	if mode == "" {
		return ResultModeStoreOnly
	}
	return mode
}

func (j *CronJob) ShouldPersistResult() bool {
	if j.Result.Persist != nil {
		return *j.Result.Persist
	}
	switch j.ResultMode() {
	case ResultModeStoreOnly, ResultModeDeliver, ResultModeHandoffMain:
		return true
	default:
		return false
	}
}

func (j *CronJob) ShouldDeliverResult() bool {
	return j.ResultMode() == ResultModeDeliver
}

func (j *CronJob) ShouldHandoffResult() bool {
	return j.ResultMode() == ResultModeHandoffMain
}

func (j *CronJob) Validate() error {
	if strings.TrimSpace(j.Name) == "" {
		return fmt.Errorf("job name is required")
	}
	if strings.TrimSpace(j.Prompt) == "" {
		return fmt.Errorf("job prompt is required")
	}
	switch j.ResultMode() {
	case ResultModeStoreOnly:
		if j.Result.Channel != "" || j.Result.To != "" || j.Result.BestEffort {
			return fmt.Errorf("store_only mode cannot specify delivery targeting")
		}
	case ResultModeDeliver:
		// Channel/To are optional for future use.
	case ResultModeHandoffMain:
		if j.Result.Channel != "" || j.Result.To != "" || j.Result.BestEffort {
			return fmt.Errorf("handoff_main mode cannot specify delivery targeting")
		}
	default:
		return fmt.Errorf("invalid result mode: %q", j.Result.Mode)
	}
	return nil
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
	if err := json.Unmarshal(data, &clone); err != nil {
		L_warn("cron: failed to unmarshal job clone", "job", j.ID, "error", err)
	}
	return &clone
}
