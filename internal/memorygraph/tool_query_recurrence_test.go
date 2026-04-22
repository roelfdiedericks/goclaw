package memorygraph

import (
	"strings"
	"testing"
	"time"
)

// makeSearchResult wraps a memory into a SearchResult for filter tests.
func makeSearchResult(m *Memory) SearchResult {
	if m == nil {
		return SearchResult{}
	}
	return SearchResult{Memory: *m}
}

func TestFilterRoutineRecurrence_RecursOnDay(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	store := NewStore(db)

	tueThuUUID := seedRoutine(t, store, "u", "lifting", &RoutineMetadata{
		Days: []string{"tue", "thu"}, TimeStart: "17:45",
	})
	monUUID := seedRoutine(t, store, "u", "stretch", &RoutineMetadata{
		Days: []string{"mon"}, TimeStart: "08:00",
	})

	// Non-routine should be dropped by the filter.
	fact := &Memory{Content: "sky blue", Type: TypeFact, Username: "u"}
	if err := store.CreateMemory(fact); err != nil {
		t.Fatal(err)
	}

	tueMem, _ := store.GetMemory(tueThuUUID)
	monMem, _ := store.GetMemory(monUUID)
	results := []SearchResult{
		makeSearchResult(tueMem),
		makeSearchResult(monMem),
		makeSearchResult(fact),
	}

	out, err := filterRoutineRecurrence(store, results, QueryParams{RecursOnDay: "tuesday"})
	if err != nil {
		t.Fatalf("filterRoutineRecurrence: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 tuesday match, got %d", len(out))
	}
	if out[0].Memory.UUID != tueThuUUID {
		t.Errorf("wrong match: got %s", out[0].Memory.UUID)
	}

	// Invalid day name → explicit error
	if _, err := filterRoutineRecurrence(store, results, QueryParams{RecursOnDay: "bogus"}); err == nil {
		t.Errorf("expected error for bogus day name")
	}
}

func TestFilterRoutineRecurrence_RecursAtTimeWindow(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	store := NewStore(db)

	morningUUID := seedRoutine(t, store, "u", "yoga", &RoutineMetadata{
		Days: []string{"mon"}, TimeStart: "06:30",
	})
	eveningUUID := seedRoutine(t, store, "u", "lifting", &RoutineMetadata{
		Days: []string{"tue"}, TimeStart: "17:45",
	})

	morn, _ := store.GetMemory(morningUUID)
	eve, _ := store.GetMemory(eveningUUID)
	results := []SearchResult{makeSearchResult(morn), makeSearchResult(eve)}

	// Morning window.
	out, err := filterRoutineRecurrence(store, results, QueryParams{RecursAtTime: "06:00-07:00"})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(out) != 1 || out[0].Memory.UUID != morningUUID {
		t.Errorf("expected morning match, got %v", out)
	}

	// Evening window.
	out2, err := filterRoutineRecurrence(store, results, QueryParams{RecursAtTime: "17:00-19:00"})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(out2) != 1 || out2[0].Memory.UUID != eveningUUID {
		t.Errorf("expected evening match, got %v", out2)
	}

	// Bad window format.
	if _, err := filterRoutineRecurrence(store, results, QueryParams{RecursAtTime: "not-a-window"}); err == nil {
		t.Errorf("expected error for bad window")
	}
}

func TestFilterRoutineRecurrence_InvolvesPerson(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	store := NewStore(db)

	uBob := seedRoutine(t, store, "u", "dinner with bob", &RoutineMetadata{
		Days: []string{"fri"}, TimeStart: "19:00", Person: "Bob",
	})
	uSolo := seedRoutine(t, store, "u", "solo run", &RoutineMetadata{
		Days: []string{"sat"}, TimeStart: "08:00",
	})

	bob, _ := store.GetMemory(uBob)
	solo, _ := store.GetMemory(uSolo)
	results := []SearchResult{makeSearchResult(bob), makeSearchResult(solo)}

	out, err := filterRoutineRecurrence(store, results, QueryParams{InvolvesPerson: "Bob"})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(out) != 1 || out[0].Memory.UUID != uBob {
		t.Errorf("expected bob match, got %v", out)
	}
}

func TestFilterRoutineRecurrence_NextOccurrenceWithin(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	store := NewStore(db)

	// A routine that recurs daily at an "upcoming" time.
	now := time.Now()
	soon := now.Add(30 * time.Minute)
	soonTime := soon.Format("15:04")

	dailyUUID := seedRoutine(t, store, "u", "water plants", &RoutineMetadata{
		Days:      []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"},
		TimeStart: soonTime,
	})
	// A routine that recurs only on a far-away weekday/time (ensure it won't
	// be within 1h window regardless of when tests run).
	farUUID := seedRoutine(t, store, "u", "annual thing", &RoutineMetadata{
		Days:      []string{"mon"},
		TimeStart: "03:00",
		// Set StartsOn 1 year out so NextOccurrence > 1h regardless.
		StartsOn: ptrTime(now.AddDate(1, 0, 0)),
	})

	daily, _ := store.GetMemory(dailyUUID)
	far, _ := store.GetMemory(farUUID)
	results := []SearchResult{makeSearchResult(daily), makeSearchResult(far)}

	out, err := filterRoutineRecurrence(store, results, QueryParams{NextOccurrenceWithin: "1h"})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 match, got %d: %+v", len(out), out)
	}
	if out[0].Memory.UUID != dailyUUID {
		t.Errorf("expected daily uuid=%s, got %s", dailyUUID, out[0].Memory.UUID)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestFormatRoutineRecurrenceLine_Basic(t *testing.T) {
	next := time.Date(2026, 4, 21, 17, 45, 0, 0, time.Local)
	meta := &RoutineMetadata{
		Days:      []string{"tuesday", "thursday"},
		TimeStart: "17:45",
		TimeEnd:   "18:45",
		Location:  "gym",
		Person:    "Bob",
	}
	got := formatRoutineRecurrenceLine(meta, &next, false)
	if !strings.Contains(got, "recurrence: Tue,Thu @ 17:45-18:45 @ gym") {
		t.Errorf("cadence missing in %q", got)
	}
	if !strings.Contains(got, "person: Bob") {
		t.Errorf("person missing in %q", got)
	}
	if !strings.Contains(got, "next: Tue 2026-04-21 17:45") {
		t.Errorf("next missing in %q", got)
	}
}

func TestFormatRoutineRecurrenceLine_Ended(t *testing.T) {
	past := time.Date(2026, 1, 15, 0, 0, 0, 0, time.Local)
	meta := &RoutineMetadata{
		Days:      []string{"monday"},
		TimeStart: "09:00",
		EndsOn:    &past,
	}
	got := formatRoutineRecurrenceLine(meta, nil, false)
	if !strings.Contains(got, "(ended 2026-01-15)") {
		t.Errorf("expected ended annotation, got %q", got)
	}
	if strings.Contains(got, "next:") {
		t.Errorf("ended routine should not show next: %q", got)
	}
}

func TestFormatRoutineRecurrenceLine_EmptyOnLegacy(t *testing.T) {
	// Legacy routine without days/time_start produces no recurrence line.
	meta := &RoutineMetadata{Action: "log"}
	if got := formatRoutineRecurrenceLine(meta, nil, false); got != "" {
		t.Errorf("expected empty for legacy meta, got %q", got)
	}
}

func TestFormatRoutineRecurrenceLine_FullBoundsAndSkip(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	end := time.Date(2026, 6, 30, 0, 0, 0, 0, time.Local)
	meta := &RoutineMetadata{
		Days:      []string{"mon", "wed"},
		TimeStart: "07:00",
		StartsOn:  &start,
		EndsOn:    &end,
		SkipDates: []string{"2026-04-15", "2026-04-22"},
	}
	got := formatRoutineRecurrenceLine(meta, nil, true)
	if !strings.Contains(got, "bounds: starts 2026-01-01 / ends 2026-06-30") {
		t.Errorf("bounds missing: %q", got)
	}
	if !strings.Contains(got, "skip: 2026-04-15, 2026-04-22") {
		t.Errorf("skip missing: %q", got)
	}
	// Standard (full=false) should omit bounds + skip.
	std := formatRoutineRecurrenceLine(meta, nil, false)
	if strings.Contains(std, "bounds:") || strings.Contains(std, "skip:") {
		t.Errorf("standard should omit bounds/skip: %q", std)
	}
}

func TestFormatQueryResults_IncludesRecurrenceForRoutine(t *testing.T) {
	next := time.Date(2026, 4, 21, 17, 45, 0, 0, time.Local)
	mem := Memory{
		UUID: "u-lifting", Type: TypeRoutine, Content: "lifting at gym",
		Importance: 0.7, NextTriggerAt: &next,
	}
	meta := &RoutineMetadata{
		Days:      []string{"tuesday", "thursday"},
		TimeStart: "17:45",
		TimeEnd:   "18:45",
		Location:  "gym",
	}
	results := []SearchResult{{Memory: mem, Score: 0.7}}
	metas := map[string]*RoutineMetadata{"u-lifting": meta}

	out := formatQueryResults(results, nil, "standard", metas)
	if !strings.Contains(out, "recurrence: Tue,Thu @ 17:45-18:45 @ gym") {
		t.Errorf("expected recurrence line in output:\n%s", out)
	}
	if !strings.Contains(out, "next: Tue 2026-04-21 17:45") {
		t.Errorf("expected next in output:\n%s", out)
	}

	// Summary should not include the recurrence line.
	summary := formatQueryResults(results, nil, "summary", metas)
	if strings.Contains(summary, "recurrence:") {
		t.Errorf("summary should omit recurrence: %q", summary)
	}
}

func TestFormatRecallResults_IncludesRecurrenceForRoutine(t *testing.T) {
	next := time.Date(2026, 4, 22, 6, 30, 0, 0, time.Local)
	mem := Memory{
		UUID: "u-yoga", Type: TypeRoutine, Content: "yoga",
		Importance: 0.6, NextTriggerAt: &next,
	}
	meta := &RoutineMetadata{
		Days:      []string{"monday", "wednesday", "friday"},
		TimeStart: "06:30",
	}
	results := []SearchResult{{Memory: mem, Score: 0.6}}
	metas := map[string]*RoutineMetadata{"u-yoga": meta}

	out := formatRecallResults(results, metas)
	if !strings.Contains(out, "recurrence: Mon,Wed,Fri @ 06:30") {
		t.Errorf("expected recurrence line:\n%s", out)
	}
	if !strings.Contains(out, "next: Wed 2026-04-22 06:30") {
		t.Errorf("expected next line:\n%s", out)
	}
}
