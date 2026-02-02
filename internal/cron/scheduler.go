package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	cronlib "github.com/robfig/cron/v3"
)

// NextRunTime calculates the next run time for a job.
func NextRunTime(job *CronJob, now time.Time) (*time.Time, error) {
	if !job.Enabled {
		return nil, nil
	}

	switch job.Schedule.Kind {
	case ScheduleKindAt:
		return nextRunAt(job, now)
	case ScheduleKindEvery:
		return nextRunEvery(job, now)
	case ScheduleKindCron:
		return nextRunCron(job, now)
	default:
		return nil, fmt.Errorf("unknown schedule kind: %s", job.Schedule.Kind)
	}
}

// nextRunAt calculates next run for "at" (one-shot) jobs.
func nextRunAt(job *CronJob, now time.Time) (*time.Time, error) {
	atTime := time.UnixMilli(job.Schedule.AtMs)
	
	// If already past, return nil (job won't run again)
	if atTime.Before(now) || atTime.Equal(now) {
		// Check if job has already run
		if job.State.LastRunAtMs != nil {
			return nil, nil // Already executed
		}
		// If not yet run, return the time (will execute immediately)
		return &atTime, nil
	}
	
	return &atTime, nil
}

// nextRunEvery calculates next run for "every" (interval) jobs.
func nextRunEvery(job *CronJob, now time.Time) (*time.Time, error) {
	intervalMs := job.Schedule.EveryMs
	if intervalMs <= 0 {
		return nil, fmt.Errorf("invalid interval: %d", intervalMs)
	}

	// If never run, next run is now + interval from creation
	if job.State.LastRunAtMs == nil {
		next := time.UnixMilli(job.CreatedAtMs).Add(time.Duration(intervalMs) * time.Millisecond)
		// If that's in the past, use now + interval
		if next.Before(now) {
			next = now.Add(time.Duration(intervalMs) * time.Millisecond)
		}
		return &next, nil
	}

	// Next run is last run + interval
	lastRun := time.UnixMilli(*job.State.LastRunAtMs)
	next := lastRun.Add(time.Duration(intervalMs) * time.Millisecond)

	// If we're behind, catch up to next future interval
	for next.Before(now) {
		next = next.Add(time.Duration(intervalMs) * time.Millisecond)
	}

	return &next, nil
}

// nextRunCron calculates next run for standard 5-field cron expressions.
func nextRunCron(job *CronJob, now time.Time) (*time.Time, error) {
	expr := job.Schedule.Expr
	if expr == "" {
		return nil, fmt.Errorf("empty cron expression")
	}

	// Determine timezone
	tz := time.Local
	if job.Schedule.Tz != "" {
		loc, err := time.LoadLocation(job.Schedule.Tz)
		if err != nil {
			return nil, fmt.Errorf("invalid timezone %q: %w", job.Schedule.Tz, err)
		}
		tz = loc
	}

	// Parse the cron expression using robfig/cron
	// We use a standard parser (minute, hour, day, month, weekday)
	parser := cronlib.NewParser(cronlib.Minute | cronlib.Hour | cronlib.Dom | cronlib.Month | cronlib.Dow)
	schedule, err := parser.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression %q: %w", expr, err)
	}

	// Calculate next time in the correct timezone
	nowInTz := now.In(tz)
	next := schedule.Next(nowInTz)
	
	return &next, nil
}

// ParseDuration parses human-friendly duration strings.
// Supports: "30s", "5m", "2h", "1d", "1w"
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}

	// Check for days/weeks (not supported by time.ParseDuration)
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, fmt.Errorf("invalid days: %w", err)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}

	if strings.HasSuffix(s, "w") {
		weeks, err := strconv.Atoi(strings.TrimSuffix(s, "w"))
		if err != nil {
			return 0, fmt.Errorf("invalid weeks: %w", err)
		}
		return time.Duration(weeks) * 7 * 24 * time.Hour, nil
	}

	// Use standard time.ParseDuration for s, m, h
	return time.ParseDuration(s)
}

// ParseAt parses an "at" time specification.
// Supports:
//   - Unix milliseconds: "1704067200000"
//   - ISO 8601: "2024-01-01T12:00:00Z"
//   - Relative: "+5m", "+2h", "+1d"
func ParseAt(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time specification")
	}

	// Relative time
	if strings.HasPrefix(s, "+") {
		dur, err := ParseDuration(s[1:])
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid relative time: %w", err)
		}
		return now.Add(dur), nil
	}

	// Unix milliseconds
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil && ms > 1000000000000 {
		return time.UnixMilli(ms), nil
	}

	// ISO 8601
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}

	// Try without timezone
	if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		return t, nil
	}

	return time.Time{}, fmt.Errorf("unrecognized time format: %s", s)
}
