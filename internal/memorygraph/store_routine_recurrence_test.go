package memorygraph

import (
	"testing"
	"time"
)

// seedRoutine creates a routine memory + routine_metadata with structured
// recurrence fields, using the given store. Returns the memory UUID.
func seedRoutine(t *testing.T, store *Store, username, content string, meta *RoutineMetadata) string {
	t.Helper()
	mem := &Memory{
		Content:    content,
		Type:       TypeRoutine,
		Importance: 0.7,
		Confidence: 0.9,
		Username:   username,
	}
	if err := store.CreateMemory(mem); err != nil {
		t.Fatalf("CreateMemory: %v", err)
	}
	if meta == nil {
		return mem.UUID
	}
	meta.MemoryUUID = mem.UUID
	if meta.TriggerType == "" {
		meta.TriggerType = "time"
	}
	if err := store.SetRoutineMetadata(meta); err != nil {
		t.Fatalf("SetRoutineMetadata: %v", err)
	}
	return mem.UUID
}

func TestStoreRoutineMetadata_RoundTrip(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	store := NewStore(db)

	starts := time.Date(2025, 10, 1, 0, 0, 0, 0, time.Local)
	ends := time.Date(2025, 12, 31, 0, 0, 0, 0, time.Local)
	dur := 60
	meta := &RoutineMetadata{
		TriggerType:     "time",
		TriggerCron:     "45 17 * * 2,4",
		Days:            []string{"tuesday", "thursday"},
		TimeStart:       "17:45",
		TimeEnd:         "18:45",
		DurationMinutes: &dur,
		Location:        "gym",
		Person:          "alice",
		StartsOn:        &starts,
		EndsOn:          &ends,
		SkipDates:       []string{"2025-12-24", "2025-12-25"},
	}
	uuid := seedRoutine(t, store, "alice", "evening workout", meta)

	got, err := store.GetRoutineMetadata(uuid)
	if err != nil {
		t.Fatalf("GetRoutineMetadata: %v", err)
	}
	if got == nil {
		t.Fatalf("expected metadata, got nil")
	}

	if len(got.Days) != 2 || got.Days[0] != "tuesday" || got.Days[1] != "thursday" {
		t.Errorf("Days round-trip: got %v want [tuesday thursday]", got.Days)
	}
	if got.TimeStart != "17:45" || got.TimeEnd != "18:45" {
		t.Errorf("times round-trip: got %q/%q", got.TimeStart, got.TimeEnd)
	}
	if got.DurationMinutes == nil || *got.DurationMinutes != 60 {
		t.Errorf("DurationMinutes round-trip: got %v", got.DurationMinutes)
	}
	if got.Location != "gym" || got.Person != "alice" {
		t.Errorf("location/person round-trip: got %q/%q", got.Location, got.Person)
	}
	if got.StartsOn == nil || !got.StartsOn.Equal(starts) {
		t.Errorf("StartsOn round-trip: got %v want %v", got.StartsOn, starts)
	}
	if got.EndsOn == nil || !got.EndsOn.Equal(ends) {
		t.Errorf("EndsOn round-trip: got %v want %v", got.EndsOn, ends)
	}
	if len(got.SkipDates) != 2 {
		t.Errorf("SkipDates round-trip: got %v", got.SkipDates)
	}
	if got.TriggerCron != "45 17 * * 2,4" {
		t.Errorf("TriggerCron round-trip: got %q", got.TriggerCron)
	}
}

func TestStoreGetRoutinesForDay(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	store := NewStore(db)

	// Tue/Thu routine
	_ = seedRoutine(t, store, "bob", "lifting", &RoutineMetadata{
		Days: []string{"tue", "thu"}, TimeStart: "17:45",
	})
	// Mon/Wed/Fri routine
	_ = seedRoutine(t, store, "bob", "yoga", &RoutineMetadata{
		Days: []string{"mon", "wed", "fri"}, TimeStart: "06:30",
	})
	// Different user
	_ = seedRoutine(t, store, "carol", "stretch", &RoutineMetadata{
		Days: []string{"tue"}, TimeStart: "08:00",
	})

	got, err := store.GetRoutinesForDay("bob", "Tuesday")
	if err != nil {
		t.Fatalf("GetRoutinesForDay: %v", err)
	}
	if len(got) != 1 || got[0].Memory.Content != "lifting" {
		t.Errorf("Tuesday/bob: got %d routines, expected 1 'lifting'", len(got))
	}

	got2, err := store.GetRoutinesForDay("bob", "mon")
	if err != nil {
		t.Fatalf("GetRoutinesForDay mon: %v", err)
	}
	if len(got2) != 1 || got2[0].Memory.Content != "yoga" {
		t.Errorf("Monday/bob: got %v", got2)
	}

	got3, err := store.GetRoutinesForDay("bob", "saturday")
	if err != nil {
		t.Fatalf("GetRoutinesForDay sat: %v", err)
	}
	if len(got3) != 0 {
		t.Errorf("Saturday/bob: expected empty, got %d", len(got3))
	}
}

func TestStoreListDueRoutines_AndClaim(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	store := NewStore(db)

	// Routine due 5 minutes ago.
	past := time.Now().Add(-5 * time.Minute)
	uuid := seedRoutine(t, store, "alice", "take meds", &RoutineMetadata{
		Days: []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"},
		TimeStart: "08:00",
	})
	if err := store.SetNextTriggerAt(uuid, &past); err != nil {
		t.Fatalf("SetNextTriggerAt: %v", err)
	}

	// Routine due in the future.
	future := time.Now().Add(30 * time.Minute)
	uuid2 := seedRoutine(t, store, "alice", "walk", &RoutineMetadata{
		Days: []string{"sun"}, TimeStart: "17:00",
	})
	if err := store.SetNextTriggerAt(uuid2, &future); err != nil {
		t.Fatalf("SetNextTriggerAt future: %v", err)
	}

	due, err := store.ListDueRoutines(time.Now())
	if err != nil {
		t.Fatalf("ListDueRoutines: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("expected 1 due, got %d", len(due))
	}
	if due[0].Memory.UUID != uuid {
		t.Errorf("expected due uuid=%s, got %s", uuid, due[0].Memory.UUID)
	}

	// Claim it — first attempt succeeds, second is rejected.
	ok1, err := store.ClaimTrigger(uuid, past, "alice", "user:alice")
	if err != nil || !ok1 {
		t.Fatalf("first claim: ok=%v err=%v", ok1, err)
	}
	ok2, err := store.ClaimTrigger(uuid, past, "alice", "user:alice")
	if err != nil {
		t.Fatalf("second claim err: %v", err)
	}
	if ok2 {
		t.Errorf("second claim should have been rejected by UNIQUE constraint")
	}

	if err := store.MarkTriggerOutcome(uuid, past, "fired", "run-1"); err != nil {
		t.Fatalf("MarkTriggerOutcome: %v", err)
	}

	rows, err := store.RecentTriggerFired(10)
	if err != nil {
		t.Fatalf("RecentTriggerFired: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 fired row, got %d", len(rows))
	}
	if rows[0].Outcome != "fired" || rows[0].RunID != "run-1" {
		t.Errorf("fired row: got outcome=%q runID=%q", rows[0].Outcome, rows[0].RunID)
	}
}

func TestTriggersFiredForUserOnDate_LocalDayWindow(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	store := NewStore(db)

	uuidA := seedRoutine(t, store, "alice", "yoga", &RoutineMetadata{
		Days: []string{"mon"}, TimeStart: "07:00",
	})
	uuidB := seedRoutine(t, store, "alice", "lifting", &RoutineMetadata{
		Days: []string{"mon"}, TimeStart: "17:00",
	})
	uuidC := seedRoutine(t, store, "bob", "run", &RoutineMetadata{
		Days: []string{"mon"}, TimeStart: "08:00",
	})

	today := time.Date(2025, 10, 14, 12, 0, 0, 0, time.Local)
	morn := time.Date(2025, 10, 14, 7, 0, 0, 0, time.Local)
	noon := time.Date(2025, 10, 14, 12, 30, 0, 0, time.Local)
	eve := time.Date(2025, 10, 14, 17, 0, 0, 0, time.Local)
	yesterday := time.Date(2025, 10, 13, 17, 0, 0, 0, time.Local)

	// Morning + evening fires for alice on same routine's sibling.
	claimFire(t, store, uuidA, "alice", morn, morn.Add(time.Second), "fired", "r_morn")
	claimFire(t, store, uuidB, "alice", noon, noon.Add(time.Second), "silent", "r_noon")
	claimFire(t, store, uuidB, "alice", eve, eve.Add(time.Second), "fired", "r_eve")
	// Yesterday's fire should be excluded.
	claimFire(t, store, uuidA, "alice", yesterday, yesterday.Add(time.Second), "fired", "r_yesterday")
	// Other user's fire excluded.
	claimFire(t, store, uuidC, "bob", morn, morn.Add(time.Second), "fired", "r_bob")

	got, err := store.TriggersFiredForUserOnDate("alice", today)
	if err != nil {
		t.Fatalf("TriggersFiredForUserOnDate: %v", err)
	}

	if _, ok := got[uuidC]; ok {
		t.Errorf("bob's row leaked into alice result")
	}

	// uuidA: only morning entry (yesterday excluded).
	aliceA := got[uuidA]
	if len(aliceA) != 1 {
		t.Fatalf("expected 1 morning fire for uuidA, got %d", len(aliceA))
	}
	if !aliceA[0].ScheduledFor.Equal(morn) {
		t.Errorf("expected morning scheduled, got %v", aliceA[0].ScheduledFor)
	}

	// uuidB: two entries, sorted ascending by ScheduledFor.
	aliceB := got[uuidB]
	if len(aliceB) != 2 {
		t.Fatalf("expected 2 fires for uuidB, got %d", len(aliceB))
	}
	if !aliceB[0].ScheduledFor.Equal(noon) {
		t.Errorf("expected first=noon, got %v", aliceB[0].ScheduledFor)
	}
	if !aliceB[1].ScheduledFor.Equal(eve) {
		t.Errorf("expected second=eve, got %v", aliceB[1].ScheduledFor)
	}
}
