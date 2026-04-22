package memorygraph

import (
	"strings"
	"testing"
	"time"
)

func TestBuildRoutineMetadata_Valid(t *testing.T) {
	rec := &RecurrenceInput{
		Days:      []string{"mon", "WED", "fri"},
		TimeStart: "06:30",
		TimeEnd:   "07:30",
		Location:  "  home  ",
		Person:    "self",
		StartsOn:  "2025-10-01",
		EndsOn:    "2025-12-31",
		SkipDates: []string{"2025-12-24", "2025-12-25"},
		Autonomy:  "suggest",
	}
	meta, errMsg := buildRoutineMetadata(rec)
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if meta.TriggerCron != "30 6 * * 1,3,5" {
		t.Errorf("cron derived: got %q", meta.TriggerCron)
	}
	if meta.Autonomy != "suggest" {
		t.Errorf("autonomy: got %q", meta.Autonomy)
	}
	if meta.Location != "home" {
		t.Errorf("location trim: got %q", meta.Location)
	}
	if meta.StartsOn == nil || meta.StartsOn.Format("2006-01-02") != "2025-10-01" {
		t.Errorf("starts_on: got %v", meta.StartsOn)
	}
	if len(meta.Days) != 3 {
		t.Errorf("days normalized: got %v", meta.Days)
	}
}

func TestBuildRoutineMetadata_DefaultsAutonomy(t *testing.T) {
	rec := &RecurrenceInput{Days: []string{"mon"}, TimeStart: "10:00"}
	meta, errMsg := buildRoutineMetadata(rec)
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if meta.Autonomy != "observe" {
		t.Errorf("default autonomy: got %q, want observe", meta.Autonomy)
	}
}

func TestBuildRoutineMetadata_Errors(t *testing.T) {
	cases := []struct {
		name    string
		rec     *RecurrenceInput
		wantSub string
	}{
		{"bad days only", &RecurrenceInput{Days: []string{"xyz", "abc"}, TimeStart: "10:00"}, "no recognisable day names"},
		{"bad time_start", &RecurrenceInput{Days: []string{"mon"}, TimeStart: "25:00"}, "time_start"},
		{"bad time_end", &RecurrenceInput{Days: []string{"mon"}, TimeStart: "10:00", TimeEnd: "99:99"}, "time_end"},
		{"bad starts_on", &RecurrenceInput{Days: []string{"mon"}, TimeStart: "10:00", StartsOn: "not-a-date"}, "starts_on"},
		{"bad ends_on", &RecurrenceInput{Days: []string{"mon"}, TimeStart: "10:00", EndsOn: "not-a-date"}, "ends_on"},
		{"ends before starts", &RecurrenceInput{Days: []string{"mon"}, TimeStart: "10:00", StartsOn: "2025-12-31", EndsOn: "2025-01-01"}, "before starts_on"},
		{"bad skip date", &RecurrenceInput{Days: []string{"mon"}, TimeStart: "10:00", SkipDates: []string{"bogus"}}, "skip_dates"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errMsg := buildRoutineMetadata(tc.rec)
			if errMsg == "" {
				t.Fatalf("expected error, got none")
			}
			if !strings.Contains(errMsg, tc.wantSub) {
				t.Errorf("expected error to contain %q, got %q", tc.wantSub, errMsg)
			}
		})
	}
}

func TestBuildRoutineMetadata_NilInput(t *testing.T) {
	meta, errMsg := buildRoutineMetadata(nil)
	if errMsg != "" || meta != nil {
		t.Errorf("nil input: got meta=%v err=%q", meta, errMsg)
	}
}

func TestBuildRoutineMetadata_DatesAreLocal(t *testing.T) {
	rec := &RecurrenceInput{
		Days:      []string{"mon"},
		TimeStart: "10:00",
		StartsOn:  "2025-10-06",
	}
	meta, errMsg := buildRoutineMetadata(rec)
	if errMsg != "" {
		t.Fatalf("err: %s", errMsg)
	}
	if meta.StartsOn == nil {
		t.Fatalf("starts_on nil")
	}
	if meta.StartsOn.Location() != time.Local {
		t.Errorf("starts_on location: got %v, want Local", meta.StartsOn.Location())
	}
	if meta.StartsOn.Year() != 2025 || meta.StartsOn.Month() != time.October || meta.StartsOn.Day() != 6 {
		t.Errorf("starts_on value: got %v", meta.StartsOn)
	}
}
