package memorygraph

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// dayNames is the canonical day-name list, lowercase, Monday..Sunday.
// Index matches ISO weekday (1 = Monday, 7 = Sunday). Index 0 unused.
var dayNames = [...]string{
	"",
	"monday",
	"tuesday",
	"wednesday",
	"thursday",
	"friday",
	"saturday",
	"sunday",
}

// dayAliases maps common short/alternative spellings to the canonical lowercase full name.
var dayAliases = map[string]string{
	"mon": "monday", "monday": "monday", "mo": "monday", "m": "monday",
	"tue": "tuesday", "tues": "tuesday", "tuesday": "tuesday", "tu": "tuesday",
	"wed": "wednesday", "weds": "wednesday", "wednesday": "wednesday", "we": "wednesday", "w": "wednesday",
	"thu": "thursday", "thur": "thursday", "thurs": "thursday", "thursday": "thursday", "th": "thursday",
	"fri": "friday", "friday": "friday", "fr": "friday", "f": "friday",
	"sat": "saturday", "saturday": "saturday", "sa": "saturday",
	"sun": "sunday", "sunday": "sunday", "su": "sunday",
}

// isoWeekday is the ISO weekday number (1 = Mon, 7 = Sun) for each canonical name.
var isoWeekday = map[string]int{
	"monday": 1, "tuesday": 2, "wednesday": 3, "thursday": 4,
	"friday": 5, "saturday": 6, "sunday": 7,
}

// cronWeekday maps canonical day name to the cron day-of-week value.
// Cron uses 0 = Sunday, 1 = Monday, ..., 6 = Saturday.
var cronWeekday = map[string]int{
	"sunday": 0, "monday": 1, "tuesday": 2, "wednesday": 3,
	"thursday": 4, "friday": 5, "saturday": 6,
}

// dayNameNormalize returns the canonical lowercase full day name for the given input.
// Accepts case-insensitive short/full English names and ISO weekday numbers ("1".."7").
// Returns an empty string if the input is not recognisable.
func dayNameNormalize(s string) string {
	raw := strings.TrimSpace(strings.ToLower(s))
	if raw == "" {
		return ""
	}
	if name, ok := dayAliases[raw]; ok {
		return name
	}
	if n, err := strconv.Atoi(raw); err == nil && n >= 1 && n <= 7 {
		return dayNames[n]
	}
	return ""
}

// normalizeDays returns the canonical names for a slice of day identifiers, preserving
// Monday..Sunday order and de-duplicating. Invalid entries are skipped.
func normalizeDays(days []string) []string {
	if len(days) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(days))
	normalized := make([]string, 0, len(days))
	for _, d := range days {
		name := dayNameNormalize(d)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		normalized = append(normalized, name)
	}
	sort.Slice(normalized, func(i, j int) bool {
		return isoWeekday[normalized[i]] < isoWeekday[normalized[j]]
	})
	return normalized
}

// parseClockTime parses "HH:MM" (24-hour). Returns hour, minute, error.
func parseClockTime(s string) (int, int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, fmt.Errorf("empty time")
	}
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid time %q (want HH:MM)", s)
	}
	h, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || h < 0 || h > 23 {
		return 0, 0, fmt.Errorf("invalid hour in %q", s)
	}
	m, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("invalid minute in %q", s)
	}
	return h, m, nil
}

// deriveTriggerCron builds a cron expression from structured recurrence fields.
// Returns "" when inputs are insufficient (no days or no time_start); callers
// should treat that as "no cron derived — rely on structured fields instead".
func deriveTriggerCron(days []string, timeStart string) (string, error) {
	normalized := normalizeDays(days)
	if len(normalized) == 0 || strings.TrimSpace(timeStart) == "" {
		return "", nil
	}
	h, min, err := parseClockTime(timeStart)
	if err != nil {
		return "", err
	}
	// Build cron day-of-week list.
	if len(normalized) == 7 {
		return fmt.Sprintf("%d %d * * *", min, h), nil
	}
	nums := make([]string, 0, len(normalized))
	for _, d := range normalized {
		nums = append(nums, strconv.Itoa(cronWeekday[d]))
	}
	return fmt.Sprintf("%d %d * * %s", min, h, strings.Join(nums, ",")), nil
}

// daySet returns a set of ISO weekday numbers (1..7) for the routine's days.
func (r *RoutineMetadata) daySet() map[int]struct{} {
	if len(r.Days) == 0 {
		return nil
	}
	out := make(map[int]struct{}, len(r.Days))
	for _, d := range r.Days {
		name := dayNameNormalize(d)
		if name == "" {
			continue
		}
		out[isoWeekday[name]] = struct{}{}
	}
	return out
}

// dateOnly returns t truncated to midnight in its own location.
func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// isSkipped returns true if the routine has a skip_dates entry for the given day (YYYY-MM-DD in local tz).
func (r *RoutineMetadata) isSkipped(t time.Time) bool {
	if len(r.SkipDates) == 0 {
		return false
	}
	day := t.Format("2006-01-02")
	for _, s := range r.SkipDates {
		if strings.TrimSpace(s) == day {
			return true
		}
	}
	return false
}

// inBounds reports whether t falls within StartsOn (inclusive) / EndsOn (inclusive).
// Bounds compared as dates in t's location.
func (r *RoutineMetadata) inBounds(t time.Time) bool {
	today := dateOnly(t)
	if r.StartsOn != nil {
		if today.Before(dateOnly(*r.StartsOn)) {
			return false
		}
	}
	if r.EndsOn != nil {
		if today.After(dateOnly(*r.EndsOn)) {
			return false
		}
	}
	return true
}

// NextOccurrence returns the next scheduled start time on or after `from` in from.Location(),
// honouring Days, TimeStart, StartsOn, EndsOn, and SkipDates.
// Returns the zero time.Time when the routine has insufficient structured fields
// (no Days or no TimeStart) or the next occurrence would fall after EndsOn.
func (r *RoutineMetadata) NextOccurrence(from time.Time) time.Time {
	if len(r.Days) == 0 || strings.TrimSpace(r.TimeStart) == "" {
		return time.Time{}
	}
	h, min, err := parseClockTime(r.TimeStart)
	if err != nil {
		return time.Time{}
	}
	daySet := r.daySet()
	if len(daySet) == 0 {
		return time.Time{}
	}

	// Search up to 14 days forward (enough to cover any weekly pattern + bounds).
	for offset := 0; offset < 14; offset++ {
		candidate := time.Date(
			from.Year(), from.Month(), from.Day()+offset,
			h, min, 0, 0, from.Location(),
		)
		if offset == 0 && !candidate.After(from) {
			continue
		}
		if !r.inBounds(candidate) {
			// Past EndsOn — nothing further possible.
			if r.EndsOn != nil && dateOnly(candidate).After(dateOnly(*r.EndsOn)) {
				return time.Time{}
			}
			continue
		}
		iso := int(candidate.Weekday())
		if iso == 0 {
			iso = 7
		}
		if _, ok := daySet[iso]; !ok {
			continue
		}
		if r.isSkipped(candidate) {
			continue
		}
		return candidate
	}
	return time.Time{}
}

// OccurrencesIn returns all scheduled start times within [from, until) in from.Location(),
// ordered ascending. Returns nil when the routine has insufficient fields.
func (r *RoutineMetadata) OccurrencesIn(from, until time.Time) []time.Time {
	if !until.After(from) {
		return nil
	}
	if len(r.Days) == 0 || strings.TrimSpace(r.TimeStart) == "" {
		return nil
	}
	var out []time.Time
	cursor := from
	for {
		next := r.NextOccurrence(cursor)
		if next.IsZero() || !next.Before(until) {
			break
		}
		out = append(out, next)
		// Advance past this occurrence.
		cursor = next.Add(time.Minute)
	}
	return out
}
