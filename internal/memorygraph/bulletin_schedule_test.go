package memorygraph

import (
	"strings"
	"testing"
	"time"
)

func makeRoutine(content, timeStart, timeEnd, location string, durMin *int) *RoutineWithMetadata {
	return &RoutineWithMetadata{
		Memory: &Memory{UUID: "u-" + content, Content: content, Type: TypeRoutine},
		Meta: &RoutineMetadata{
			Days:            []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"},
			TimeStart:       timeStart,
			TimeEnd:         timeEnd,
			DurationMinutes: durMin,
			Location:        location,
		},
	}
}

func TestFormatTodayScheduleLine_States(t *testing.T) {
	base := time.Date(2025, 10, 14, 12, 0, 0, 0, time.Local)

	// Upcoming in 2h — state "[upcoming in 2h]".
	upcoming := makeRoutine("lift", "14:00", "15:00", "gym", nil)
	line, annotated := formatTodayScheduleLine(upcoming, base, nil)
	if !strings.Contains(line, "14:00-15:00") || !strings.Contains(line, "lift") ||
		!strings.Contains(line, "@ gym") || !strings.Contains(line, "[upcoming in 2h]") {
		t.Errorf("upcoming line: %q", line)
	}
	if annotated {
		t.Errorf("upcoming: unexpected annotation")
	}

	// Coming up (within 15m).
	comingUp := makeRoutine("standup", "12:10", "12:30", "", nil)
	line2, _ := formatTodayScheduleLine(comingUp, base, nil)
	if !strings.Contains(line2, "[coming up!]") {
		t.Errorf("coming up: %q", line2)
	}

	// In progress.
	inProgress := makeRoutine("meeting", "11:30", "12:30", "", nil)
	line3, _ := formatTodayScheduleLine(inProgress, base, nil)
	if !strings.Contains(line3, "[in progress]") {
		t.Errorf("in progress: %q", line3)
	}

	// Done earlier today (before collapseHour of 18).
	done := makeRoutine("yoga", "06:30", "07:30", "", nil)
	line4, _ := formatTodayScheduleLine(done, base, nil)
	if !strings.Contains(line4, "[done]") || strings.HasPrefix(line4, "Earlier:") {
		t.Errorf("done pre-collapse: %q", line4)
	}

	// Post-18:00 collapse.
	evening := time.Date(2025, 10, 14, 19, 0, 0, 0, time.Local)
	line5, _ := formatTodayScheduleLine(done, evening, nil)
	if !strings.HasPrefix(line5, "Earlier:") || !strings.Contains(line5, "yoga") ||
		!strings.Contains(line5, "✓") {
		t.Errorf("post-collapse: %q", line5)
	}
}

func TestFormatTodayScheduleLine_DurationFallback(t *testing.T) {
	base := time.Date(2025, 10, 14, 12, 0, 0, 0, time.Local)
	dur := 45
	// No TimeEnd, use DurationMinutes.
	r := makeRoutine("call", "11:30", "", "", &dur)
	// 11:30 + 45m = 12:15 → in progress at 12:00
	line, _ := formatTodayScheduleLine(r, base, nil)
	if !strings.Contains(line, "11:30-12:15") {
		t.Errorf("duration fallback: %q", line)
	}
	if !strings.Contains(line, "[in progress]") {
		t.Errorf("duration state: %q", line)
	}
}

func TestFormatTodayScheduleLine_NoEnd(t *testing.T) {
	base := time.Date(2025, 10, 14, 12, 0, 0, 0, time.Local)
	// No end, no duration — should just show start time.
	r := makeRoutine("note", "14:00", "", "", nil)
	line, _ := formatTodayScheduleLine(r, base, nil)
	if strings.Contains(line, "-") && !strings.Contains(line, "upcoming") {
		// we allow "[upcoming in 2h]" which contains no hyphen in time part
	}
	if !strings.Contains(line, "14:00") || strings.Contains(line, "14:00-14:00") {
		t.Errorf("no-end line should contain start only: %q", line)
	}
	if !strings.Contains(line, "[upcoming in 2h]") {
		t.Errorf("state: %q", line)
	}
}

func TestFormatTomorrowScheduleLine(t *testing.T) {
	r := makeRoutine("lift", "17:45", "18:45", "gym", nil)
	line := formatTomorrowScheduleLine(r)
	if line != "17:45-18:45 lift @ gym" {
		t.Errorf("tomorrow line: %q", line)
	}
	r2 := makeRoutine("water plants", "20:00", "", "", nil)
	line2 := formatTomorrowScheduleLine(r2)
	if line2 != "20:00 water plants" {
		t.Errorf("simple tomorrow: %q", line2)
	}
}

func TestFormatRoutineCadence(t *testing.T) {
	meta := &RoutineMetadata{Days: []string{"tuesday", "thursday"}, TimeStart: "17:45"}
	got := formatRoutineCadence(meta)
	if got != " (Tue, Thu @ 17:45)" {
		t.Errorf("cadence: %q", got)
	}
	// Empty when nothing structured.
	if formatRoutineCadence(&RoutineMetadata{}) != "" {
		t.Errorf("expected empty cadence for empty meta")
	}
	// Time only.
	if got := formatRoutineCadence(&RoutineMetadata{TimeStart: "10:00"}); got != " (@ 10:00)" {
		t.Errorf("time-only cadence: %q", got)
	}
	// Days only.
	if got := formatRoutineCadence(&RoutineMetadata{Days: []string{"mon"}}); got != " (Mon)" {
		t.Errorf("days-only cadence: %q", got)
	}
}

func TestFilterRoutinesForDate_BoundsAndSkip(t *testing.T) {
	day := time.Date(2025, 10, 14, 12, 0, 0, 0, time.Local)

	// Past EndsOn — filtered out.
	past := time.Date(2025, 10, 13, 0, 0, 0, 0, time.Local)
	r1 := &RoutineWithMetadata{
		Memory: &Memory{UUID: "r1", Content: "x"},
		Meta:   &RoutineMetadata{Days: []string{"tue"}, TimeStart: "10:00", EndsOn: &past},
	}
	// Skip today — filtered out.
	r2 := &RoutineWithMetadata{
		Memory: &Memory{UUID: "r2", Content: "y"},
		Meta:   &RoutineMetadata{Days: []string{"tue"}, TimeStart: "10:00", SkipDates: []string{"2025-10-14"}},
	}
	// Active.
	r3 := &RoutineWithMetadata{
		Memory: &Memory{UUID: "r3", Content: "z"},
		Meta:   &RoutineMetadata{Days: []string{"tue"}, TimeStart: "10:00"},
	}

	out := filterRoutinesForDate([]*RoutineWithMetadata{r1, r2, r3}, day)
	if len(out) != 1 || out[0].Memory.UUID != "r3" {
		t.Errorf("filter: got %v", out)
	}
}

func TestFormatTodayScheduleLine_DoneWithOutcome(t *testing.T) {
	base := time.Date(2025, 10, 14, 12, 0, 0, 0, time.Local)
	done := makeRoutine("yoga", "06:30", "07:30", "", nil)
	scheduled := time.Date(2025, 10, 14, 6, 30, 0, 0, time.Local)

	tests := []struct {
		name       string
		outcome    string
		want       string
		annotated  bool
		checkEmpty bool
	}{
		{name: "fired no annotation", outcome: "fired", want: "[done]", annotated: false},
		{name: "silent", outcome: "silent", want: "[silent]", annotated: true},
		{name: "missed_grace maps to skipped", outcome: "missed_grace", want: "[skipped]", annotated: true},
		{name: "error maps to err", outcome: "error", want: "[err]", annotated: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fires := map[string][]*TriggerFired{
				done.Memory.UUID: {{MemoryUUID: done.Memory.UUID, ScheduledFor: scheduled, Outcome: tc.outcome}},
			}
			line, annotated := formatTodayScheduleLine(done, base, fires)
			if !strings.Contains(line, tc.want) {
				t.Errorf("expected %q in line: %q", tc.want, line)
			}
			if annotated != tc.annotated {
				t.Errorf("annotated=%v, want %v (line=%q)", annotated, tc.annotated, line)
			}
		})
	}

	// No fire row for the line → no annotation.
	lineNoFire, annotated := formatTodayScheduleLine(done, base, nil)
	if annotated {
		t.Errorf("no-fires case should not annotate (line=%q)", lineNoFire)
	}
	if !strings.Contains(lineNoFire, "[done]") {
		t.Errorf("expected [done] state without fires: %q", lineNoFire)
	}
}

func TestFormatTodayScheduleLine_EarlierCollapseWithOutcome(t *testing.T) {
	evening := time.Date(2025, 10, 14, 19, 0, 0, 0, time.Local)
	done := makeRoutine("yoga", "06:30", "07:30", "", nil)
	scheduled := time.Date(2025, 10, 14, 6, 30, 0, 0, time.Local)
	fires := map[string][]*TriggerFired{
		done.Memory.UUID: {{MemoryUUID: done.Memory.UUID, ScheduledFor: scheduled, Outcome: "silent"}},
	}
	line, annotated := formatTodayScheduleLine(done, evening, fires)
	if !strings.HasPrefix(line, "Earlier:") {
		t.Errorf("expected Earlier collapse, got %q", line)
	}
	if !strings.Contains(line, "[silent]") {
		t.Errorf("expected [silent] in collapse: %q", line)
	}
	if !annotated {
		t.Errorf("expected annotation flag true")
	}
}

func TestFormatTodayScheduleLine_FireWindowMatch(t *testing.T) {
	// A 07:00 fire must not annotate a 17:00 line on the same memory UUID
	// (±30m tolerance is far narrower than 10 hours).
	base := time.Date(2025, 10, 14, 18, 0, 0, 0, time.Local)
	done := makeRoutine("work block", "17:00", "17:30", "", nil)
	morningFire := time.Date(2025, 10, 14, 7, 0, 0, 0, time.Local)
	fires := map[string][]*TriggerFired{
		done.Memory.UUID: {{MemoryUUID: done.Memory.UUID, ScheduledFor: morningFire, Outcome: "silent"}},
	}
	line, annotated := formatTodayScheduleLine(done, base, fires)
	if annotated {
		t.Errorf("morning fire should not annotate 17:00 line (line=%q)", line)
	}
	if strings.Contains(line, "[silent]") {
		t.Errorf("unexpected [silent] leakage: %q", line)
	}

	// Picks the fire within tolerance when two exist.
	afternoonFire := time.Date(2025, 10, 14, 17, 5, 0, 0, time.Local)
	fires[done.Memory.UUID] = append(fires[done.Memory.UUID],
		&TriggerFired{MemoryUUID: done.Memory.UUID, ScheduledFor: afternoonFire, Outcome: "silent"})
	line2, annotated2 := formatTodayScheduleLine(done, base, fires)
	if !annotated2 || !strings.Contains(line2, "[silent]") {
		t.Errorf("expected afternoon fire to annotate: %q", line2)
	}
}

func TestHumaniseDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "<1m"},
		{45 * time.Minute, "45m"},
		{1 * time.Hour, "1h"},
		{90 * time.Minute, "1h30m"},
		{2 * time.Hour, "2h"},
		{2*time.Hour + 5*time.Minute, "2h05m"},
	}
	for _, tc := range cases {
		got := humaniseDuration(tc.d)
		if got != tc.want {
			t.Errorf("humaniseDuration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
