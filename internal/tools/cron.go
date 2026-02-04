package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/cron"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// CronTool allows the agent to manage scheduled tasks.
type CronTool struct{}

// NewCronTool creates a new cron tool.
func NewCronTool() *CronTool {
	return &CronTool{}
}

func (t *CronTool) Name() string {
	return "cron"
}

func (t *CronTool) Description() string {
	return `Manage scheduled tasks (cron jobs). Actions:
- status: Get cron service status
- list: List all jobs (shows running state)
- add: Create a new job
- update: Modify an existing job
- remove: Delete a job
- run: Execute a job immediately
- runs: View job execution history
- kill: Clear stuck running state for a job`
}

func (t *CronTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"status", "list", "add", "update", "remove", "run", "runs", "kill"},
				"description": "Action to perform",
			},
			"id": map[string]interface{}{
				"type":        "string",
				"description": "Job ID (for update/remove/run/runs/kill actions)",
			},
			"name": map[string]interface{}{
				"type":        "string",
				"description": "Job name (for add/update)",
			},
			"description": map[string]interface{}{
				"type":        "string",
				"description": "Job description (for add/update)",
			},
			"enabled": map[string]interface{}{
				"type":        "boolean",
				"description": "Whether the job is enabled (for add/update)",
			},
			"scheduleType": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"at", "every", "cron"},
				"description": "Schedule type: 'at' for one-shot, 'every' for interval, 'cron' for cron expression",
			},
			"at": map[string]interface{}{
				"type":        "string",
				"description": "For 'at' schedule: Unix ms, ISO 8601 datetime, or relative time (+5m, +2h)",
			},
			"every": map[string]interface{}{
				"type":        "string",
				"description": "For 'every' schedule: Interval duration (30s, 5m, 2h, 1d)",
			},
			"cronExpr": map[string]interface{}{
				"type":        "string",
				"description": "For 'cron' schedule: Standard 5-field cron expression (e.g., '0 9 * * 1-5')",
			},
			"timezone": map[string]interface{}{
				"type":        "string",
				"description": "IANA timezone for cron schedule (e.g., 'America/New_York')",
			},
			"sessionTarget": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"main", "isolated"},
				"description": "Session target: 'main' runs in primary session with context, 'isolated' runs fresh",
			},
			"message": map[string]interface{}{
				"type":        "string",
				"description": "The prompt/message to execute when job runs",
			},
			"deliver": map[string]interface{}{
				"type":        "boolean",
				"description": "Whether to deliver output to channels",
			},
		},
		"required": []string{"action"},
	}
}

type cronInput struct {
	Action        string `json:"action"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Enabled       *bool  `json:"enabled"`
	ScheduleType  string `json:"scheduleType"`
	At            string `json:"at"`
	Every         string `json:"every"`
	CronExpr      string `json:"cronExpr"`
	Timezone      string `json:"timezone"`
	SessionTarget string `json:"sessionTarget"`
	Message       string `json:"message"`
	Deliver       *bool  `json:"deliver"`
}

func (t *CronTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var in cronInput
	if err := json.Unmarshal(input, &in); err != nil {
		L_error("cron tool: invalid input", "error", err, "raw", string(input))
		return "", fmt.Errorf("invalid input: %w", err)
	}

	L_info("cron tool invoked",
		"action", in.Action,
		"id", in.ID,
		"name", in.Name,
		"scheduleType", in.ScheduleType,
		"every", in.Every,
		"at", in.At,
		"cronExpr", in.CronExpr,
		"sessionTarget", in.SessionTarget,
		"message", truncateForLog(in.Message, 100))

	service := cron.GetService()
	if service == nil {
		L_warn("cron tool: service not running")
		return "Cron service is not enabled. Enable it in config with cron.enabled: true", nil
	}

	var result string
	var err error

	switch in.Action {
	case "status":
		result, err = t.handleStatus(service)
	case "list":
		result, err = t.handleList(service)
	case "add":
		result, err = t.handleAdd(service, in)
	case "update":
		result, err = t.handleUpdate(service, in)
	case "remove":
		result, err = t.handleRemove(service, in)
	case "run":
		result, err = t.handleRun(ctx, service, in)
	case "runs":
		result, err = t.handleRuns(service, in)
	case "kill":
		result, err = t.handleKill(service, in)
	default:
		err = fmt.Errorf("unknown action: %s", in.Action)
	}

	if err != nil {
		L_error("cron tool failed", "action", in.Action, "error", err)
		return "", err
	}

	L_info("cron tool completed", "action", in.Action, "resultLen", len(result))
	return result, nil
}

func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func (t *CronTool) handleStatus(service *cron.Service) (string, error) {
	status := service.GetStatus()
	data, _ := json.MarshalIndent(status, "", "  ")
	return string(data), nil
}

func (t *CronTool) handleList(service *cron.Service) (string, error) {
	store := service.Store()
	jobs := store.GetAllJobs()

	if len(jobs) == 0 {
		return "No cron jobs configured.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d job(s):\n\n", len(jobs)))

	for _, job := range jobs {
		status := "enabled"
		if !job.Enabled {
			status = "disabled"
		}

		sb.WriteString(fmt.Sprintf("**%s** (%s)\n", job.Name, status))
		sb.WriteString(fmt.Sprintf("  ID: %s\n", job.ID))
		sb.WriteString(fmt.Sprintf("  Session: %s\n", job.SessionTarget))
		sb.WriteString(fmt.Sprintf("  Schedule: %s\n", formatSchedule(&job.Schedule)))

		if job.IsRunning() {
			runningFor := time.Since(time.UnixMilli(*job.State.RunningAtMs))
			sb.WriteString(fmt.Sprintf("  ⚠️ RUNNING: for %s (use kill action to clear)\n", runningFor.Round(time.Second)))
		}
		if job.State.NextRunAtMs != nil {
			next := time.UnixMilli(*job.State.NextRunAtMs)
			sb.WriteString(fmt.Sprintf("  Next run: %s\n", next.Format(time.RFC3339)))
		}
		if job.State.LastRunAtMs != nil {
			last := time.UnixMilli(*job.State.LastRunAtMs)
			sb.WriteString(fmt.Sprintf("  Last run: %s (%s)\n", last.Format(time.RFC3339), job.State.LastStatus))
		}
		sb.WriteString("\n")
	}

	return sb.String(), nil
}

func (t *CronTool) handleAdd(service *cron.Service, in cronInput) (string, error) {
	L_debug("cron add: validating input", "name", in.Name, "message", truncateForLog(in.Message, 50), "scheduleType", in.ScheduleType)

	if in.Name == "" {
		L_warn("cron add: name is required")
		return "", fmt.Errorf("name is required")
	}
	if in.Message == "" {
		L_warn("cron add: message is required")
		return "", fmt.Errorf("message is required")
	}
	if in.ScheduleType == "" {
		L_warn("cron add: scheduleType is required")
		return "", fmt.Errorf("scheduleType is required")
	}

	L_debug("cron add: building schedule", "type", in.ScheduleType, "every", in.Every, "at", in.At, "cron", in.CronExpr)
	schedule, err := t.buildSchedule(in)
	if err != nil {
		L_error("cron add: invalid schedule", "error", err)
		return "", fmt.Errorf("invalid schedule: %w", err)
	}

	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}

	sessionTarget := cron.SessionTargetMain
	if in.SessionTarget == "isolated" {
		sessionTarget = cron.SessionTargetIsolated
	}

	deliver := false
	if in.Deliver != nil {
		deliver = *in.Deliver
	}

	job := &cron.CronJob{
		Name:          in.Name,
		Description:   in.Description,
		Enabled:       enabled,
		Schedule:      schedule,
		SessionTarget: sessionTarget,
		Payload: cron.Payload{
			Kind:    cron.PayloadKindAgentTurn,
			Message: in.Message,
			Deliver: deliver,
		},
	}

	if err := service.AddJob(job); err != nil {
		return "", fmt.Errorf("failed to add job: %w", err)
	}

	return fmt.Sprintf("Job created successfully.\nID: %s\nName: %s\nSchedule: %s",
		job.ID, job.Name, formatSchedule(&job.Schedule)), nil
}

func (t *CronTool) handleUpdate(service *cron.Service, in cronInput) (string, error) {
	if in.ID == "" {
		return "", fmt.Errorf("id is required")
	}

	store := service.Store()
	job := store.GetJob(in.ID)
	if job == nil {
		return "", fmt.Errorf("job not found: %s", in.ID)
	}

	// Update fields if provided
	if in.Name != "" {
		job.Name = in.Name
	}
	if in.Description != "" {
		job.Description = in.Description
	}
	if in.Enabled != nil {
		job.Enabled = *in.Enabled
	}
	if in.SessionTarget != "" {
		if in.SessionTarget == "isolated" {
			job.SessionTarget = cron.SessionTargetIsolated
		} else {
			job.SessionTarget = cron.SessionTargetMain
		}
	}
	if in.Message != "" {
		job.Payload.Message = in.Message
	}
	if in.Deliver != nil {
		job.Payload.Deliver = *in.Deliver
	}

	// Update schedule if any schedule fields provided
	if in.ScheduleType != "" || in.At != "" || in.Every != "" || in.CronExpr != "" {
		schedule, err := t.buildSchedule(in)
		if err != nil {
			return "", fmt.Errorf("invalid schedule: %w", err)
		}
		job.Schedule = schedule
	}

	// Recalculate next run
	next, err := cron.NextRunTime(job, time.Now())
	if err != nil {
		return "", fmt.Errorf("failed to calculate next run: %w", err)
	}
	job.SetNextRun(next)

	if err := store.UpdateJob(job); err != nil {
		return "", fmt.Errorf("failed to update job: %w", err)
	}

	return fmt.Sprintf("Job updated successfully.\nID: %s\nName: %s", job.ID, job.Name), nil
}

func (t *CronTool) handleRemove(service *cron.Service, in cronInput) (string, error) {
	if in.ID == "" {
		return "", fmt.Errorf("id is required")
	}

	store := service.Store()
	job := store.GetJob(in.ID)
	if job == nil {
		return "", fmt.Errorf("job not found: %s", in.ID)
	}

	name := job.Name
	if err := service.RemoveJob(in.ID); err != nil {
		return "", fmt.Errorf("failed to remove job: %w", err)
	}

	return fmt.Sprintf("Job '%s' removed successfully.", name), nil
}

func (t *CronTool) handleRun(ctx context.Context, service *cron.Service, in cronInput) (string, error) {
	if in.ID == "" {
		return "", fmt.Errorf("id is required")
	}

	store := service.Store()
	job := store.GetJob(in.ID)
	if job == nil {
		return "", fmt.Errorf("job not found: %s", in.ID)
	}

	if err := service.RunNow(ctx, in.ID); err != nil {
		return "", fmt.Errorf("failed to run job: %w", err)
	}

	return fmt.Sprintf("Job '%s' triggered for immediate execution.", job.Name), nil
}

func (t *CronTool) handleRuns(service *cron.Service, in cronInput) (string, error) {
	if in.ID == "" {
		return "", fmt.Errorf("id is required")
	}

	store := service.Store()
	job := store.GetJob(in.ID)
	if job == nil {
		return "", fmt.Errorf("job not found: %s", in.ID)
	}

	// TODO Phase 4: Read from run history file
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Run history for '%s' (ID: %s)\n\n", job.Name, job.ID))

	if job.State.LastRunAtMs != nil {
		last := time.UnixMilli(*job.State.LastRunAtMs)
		sb.WriteString(fmt.Sprintf("Last run: %s\n", last.Format(time.RFC3339)))
		sb.WriteString(fmt.Sprintf("Status: %s\n", job.State.LastStatus))
		sb.WriteString(fmt.Sprintf("Duration: %dms\n", job.State.LastDurationMs))
		if job.State.LastError != "" {
			sb.WriteString(fmt.Sprintf("Error: %s\n", job.State.LastError))
		}
	} else {
		sb.WriteString("No runs recorded yet.\n")
	}

	sb.WriteString("\n(Full run history will be available in a future update)")

	return sb.String(), nil
}

func (t *CronTool) handleKill(service *cron.Service, in cronInput) (string, error) {
	if in.ID == "" {
		return "", fmt.Errorf("id is required")
	}

	store := service.Store()
	job := store.GetJob(in.ID)
	if job == nil {
		return "", fmt.Errorf("job not found: %s", in.ID)
	}

	if !job.IsRunning() {
		return fmt.Sprintf("Job '%s' is not currently marked as running.", job.Name), nil
	}

	// Get running duration for info
	runningFor := time.Since(time.UnixMilli(*job.State.RunningAtMs))

	// Clear the running state
	job.ClearRunning()
	if err := store.UpdateJob(job); err != nil {
		return "", fmt.Errorf("failed to update job: %w", err)
	}

	L_info("cron kill: cleared running state", "jobID", job.ID, "name", job.Name, "wasRunningFor", runningFor)

	return fmt.Sprintf("Cleared running state for job '%s' (was running for %s).\nNote: If the job is actually still executing, it will continue until completion or timeout.",
		job.Name, runningFor.Round(time.Second)), nil
}

func (t *CronTool) buildSchedule(in cronInput) (cron.Schedule, error) {
	L_debug("cron buildSchedule", "type", in.ScheduleType, "at", in.At, "every", in.Every, "cronExpr", in.CronExpr)

	switch in.ScheduleType {
	case "at":
		if in.At == "" {
			L_warn("cron buildSchedule: 'at' parameter missing")
			return cron.Schedule{}, fmt.Errorf("'at' parameter required for 'at' schedule")
		}
		atTime, err := cron.ParseAt(in.At, time.Now())
		if err != nil {
			L_error("cron buildSchedule: failed to parse 'at' time", "at", in.At, "error", err)
			return cron.Schedule{}, err
		}
		L_debug("cron buildSchedule: parsed 'at' time", "time", atTime)
		return cron.Schedule{
			Kind: cron.ScheduleKindAt,
			AtMs: atTime.UnixMilli(),
		}, nil

	case "every":
		if in.Every == "" {
			L_warn("cron buildSchedule: 'every' parameter missing")
			return cron.Schedule{}, fmt.Errorf("'every' parameter required for 'every' schedule")
		}
		dur, err := cron.ParseDuration(in.Every)
		if err != nil {
			L_error("cron buildSchedule: failed to parse 'every' duration", "every", in.Every, "error", err)
			return cron.Schedule{}, err
		}
		L_debug("cron buildSchedule: parsed 'every' duration", "duration", dur)
		return cron.Schedule{
			Kind:    cron.ScheduleKindEvery,
			EveryMs: dur.Milliseconds(),
		}, nil

	case "cron":
		if in.CronExpr == "" {
			L_warn("cron buildSchedule: 'cronExpr' parameter missing")
			return cron.Schedule{}, fmt.Errorf("'cronExpr' parameter required for 'cron' schedule")
		}
		L_debug("cron buildSchedule: using cron expression", "expr", in.CronExpr, "tz", in.Timezone)
		return cron.Schedule{
			Kind: cron.ScheduleKindCron,
			Expr: in.CronExpr,
			Tz:   in.Timezone,
		}, nil

	default:
		L_error("cron buildSchedule: unknown schedule type", "type", in.ScheduleType)
		return cron.Schedule{}, fmt.Errorf("unknown schedule type: %s", in.ScheduleType)
	}
}

func formatSchedule(s *cron.Schedule) string {
	switch s.Kind {
	case cron.ScheduleKindAt:
		return fmt.Sprintf("at %s", time.UnixMilli(s.AtMs).Format(time.RFC3339))
	case cron.ScheduleKindEvery:
		return fmt.Sprintf("every %s", time.Duration(s.EveryMs)*time.Millisecond)
	case cron.ScheduleKindCron:
		if s.Tz != "" {
			return fmt.Sprintf("cron '%s' (%s)", s.Expr, s.Tz)
		}
		return fmt.Sprintf("cron '%s'", s.Expr)
	default:
		return "unknown"
	}
}
