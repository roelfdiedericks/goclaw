package memorygraph

import (
	"testing"
	"time"
)

func TestDayNameNormalize(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Mon", "monday"},
		{"MONDAY", "monday"},
		{"mo", "monday"},
		{"tue", "tuesday"},
		{"wed", "wednesday"},
		{"thur", "thursday"},
		{"Fri", "friday"},
		{"Sat", "saturday"},
		{"sunday", "sunday"},
		{"1", "monday"},
		{"7", "sunday"},
		{"", ""},
		{"xyz", ""},
		{"8", ""},
		{"0", ""},
	}
	for _, tc := range cases {
		got := dayNameNormalize(tc.in)
		if got != tc.want {
			t.Errorf("dayNameNormalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeDaysOrderingAndDedup(t *testing.T) {
	days := []string{"Fri", "Mon", "mon", "tue", "unknown", "Sat"}
	got := normalizeDays(days)
	want := []string{"monday", "tuesday", "friday", "saturday"}
	if len(got) != len(want) {
		t.Fatalf("normalizeDays len: got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("normalizeDays[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDeriveTriggerCron(t *testing.T) {
	cases := []struct {
		name    string
		days    []string
		time    string
		want    string
		wantErr bool
	}{
		{"empty days", nil, "10:00", "", false},
		{"empty time", []string{"mon"}, "", "", false},
		{"all 7 days", []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}, "08:30", "30 8 * * *", false},
		{"tue+thu @ 17:45", []string{"tue", "thu"}, "17:45", "45 17 * * 2,4", false},
		{"mon only @ 00:00", []string{"mon"}, "00:00", "0 0 * * 1", false},
		{"sunday only", []string{"sunday"}, "06:00", "0 6 * * 0", false},
		{"invalid time", []string{"mon"}, "25:00", "", true},
		{"invalid days all", []string{"xxx"}, "10:00", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := deriveTriggerCron(tc.days, tc.time)
			if (err != nil) != tc.wantErr {
				t.Fatalf("deriveTriggerCron err = %v, wantErr=%v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("deriveTriggerCron = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNextOccurrence_Basic(t *testing.T) {
	// Tuesday Oct 14 2025 is a Tuesday.
	ref := time.Date(2025, 10, 14, 10, 0, 0, 0, time.Local) // Tue 10:00

	// Weekly on Tue @ 17:45 — today's 17:45 should be next.
	r := &RoutineMetadata{Days: []string{"tue"}, TimeStart: "17:45"}
	next := r.NextOccurrence(ref)
	want := time.Date(2025, 10, 14, 17, 45, 0, 0, time.Local)
	if !next.Equal(want) {
		t.Errorf("NextOccurrence weekly-today: got %v want %v", next, want)
	}

	// If we're past today's time, next should be next Tuesday.
	ref2 := time.Date(2025, 10, 14, 18, 0, 0, 0, time.Local) // Tue 18:00
	next2 := r.NextOccurrence(ref2)
	want2 := time.Date(2025, 10, 21, 17, 45, 0, 0, time.Local)
	if !next2.Equal(want2) {
		t.Errorf("NextOccurrence weekly-next-week: got %v want %v", next2, want2)
	}
}

func TestNextOccurrence_Bounds(t *testing.T) {
	ref := time.Date(2025, 10, 14, 10, 0, 0, 0, time.Local) // Tue

	ends := time.Date(2025, 10, 14, 0, 0, 0, 0, time.Local) // today is the last day
	r := &RoutineMetadata{Days: []string{"tue"}, TimeStart: "17:45", EndsOn: &ends}
	// Today's 17:45 is still within EndsOn bounds (EndsOn inclusive).
	next := r.NextOccurrence(ref)
	if next.IsZero() {
		t.Fatalf("expected occurrence today, got zero")
	}
	// After today's fire, the next-Tuesday would be beyond EndsOn → zero.
	next2 := r.NextOccurrence(time.Date(2025, 10, 14, 18, 0, 0, 0, time.Local))
	if !next2.IsZero() {
		t.Errorf("expected zero past EndsOn, got %v", next2)
	}

	// StartsOn in the future: today matches the day but bounds reject it.
	starts := time.Date(2025, 10, 21, 0, 0, 0, 0, time.Local)
	r2 := &RoutineMetadata{Days: []string{"tue"}, TimeStart: "17:45", StartsOn: &starts}
	n := r2.NextOccurrence(ref)
	expected := time.Date(2025, 10, 21, 17, 45, 0, 0, time.Local)
	if !n.Equal(expected) {
		t.Errorf("NextOccurrence with StartsOn: got %v want %v", n, expected)
	}
}

func TestNextOccurrence_SkipDates(t *testing.T) {
	ref := time.Date(2025, 10, 14, 10, 0, 0, 0, time.Local) // Tue
	skipToday := time.Date(2025, 10, 14, 0, 0, 0, 0, time.Local)
	r := &RoutineMetadata{
		Days:      []string{"tue"},
		TimeStart: "17:45",
		SkipDates: []string{"2025-10-14"},
	}
	_ = skipToday
	next := r.NextOccurrence(ref)
	want := time.Date(2025, 10, 21, 17, 45, 0, 0, time.Local)
	if !next.Equal(want) {
		t.Errorf("NextOccurrence skip today: got %v want %v", next, want)
	}
}

func TestNextOccurrence_InsufficientFields(t *testing.T) {
	r := &RoutineMetadata{Days: nil, TimeStart: "10:00"}
	if !r.NextOccurrence(time.Now()).IsZero() {
		t.Errorf("expected zero when Days missing")
	}
	r2 := &RoutineMetadata{Days: []string{"mon"}, TimeStart: ""}
	if !r2.NextOccurrence(time.Now()).IsZero() {
		t.Errorf("expected zero when TimeStart missing")
	}
}

func TestOccurrencesIn_WindowOverlap(t *testing.T) {
	// Tue/Thu @ 17:45 for a full week Mon Oct 13..Sun Oct 19 (7-day window).
	r := &RoutineMetadata{Days: []string{"tue", "thu"}, TimeStart: "17:45"}
	from := time.Date(2025, 10, 13, 0, 0, 0, 0, time.Local) // Mon 00:00
	until := time.Date(2025, 10, 20, 0, 0, 0, 0, time.Local)
	got := r.OccurrencesIn(from, until)
	if len(got) != 2 {
		t.Fatalf("expected 2 occurrences, got %d: %v", len(got), got)
	}
	wantTue := time.Date(2025, 10, 14, 17, 45, 0, 0, time.Local)
	wantThu := time.Date(2025, 10, 16, 17, 45, 0, 0, time.Local)
	if !got[0].Equal(wantTue) {
		t.Errorf("[0] got %v want %v", got[0], wantTue)
	}
	if !got[1].Equal(wantThu) {
		t.Errorf("[1] got %v want %v", got[1], wantThu)
	}
}

func TestOccurrencesIn_EmptyWindow(t *testing.T) {
	r := &RoutineMetadata{Days: []string{"tue"}, TimeStart: "17:45"}
	from := time.Date(2025, 10, 15, 0, 0, 0, 0, time.Local) // Wed
	until := time.Date(2025, 10, 16, 0, 0, 0, 0, time.Local)
	if got := r.OccurrencesIn(from, until); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
	// until <= from
	if got := r.OccurrencesIn(from, from); got != nil {
		t.Errorf("expected nil for zero-len window, got %v", got)
	}
}

func TestIsSkipped(t *testing.T) {
	r := &RoutineMetadata{SkipDates: []string{"2025-10-14", "2025-12-25"}}
	skipDay := time.Date(2025, 10, 14, 9, 0, 0, 0, time.Local)
	nonSkipDay := time.Date(2025, 10, 15, 9, 0, 0, 0, time.Local)
	if !r.isSkipped(skipDay) {
		t.Errorf("expected skip for %v", skipDay)
	}
	if r.isSkipped(nonSkipDay) {
		t.Errorf("expected not skipped for %v", nonSkipDay)
	}
}
