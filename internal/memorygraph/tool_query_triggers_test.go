package memorygraph

import (
	"strings"
	"testing"
	"time"
)

// claimFire writes a memory_triggers_fired row plus an outcome. Uses the store
// helpers directly so the tests don't depend on the trigger poller.
func claimFire(t *testing.T, store *Store, memoryUUID, username string, scheduled, fired time.Time, outcome, runID string) {
	t.Helper()
	ok, err := store.ClaimTrigger(memoryUUID, scheduled, username, "primary")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !ok {
		t.Fatalf("claim returned false")
	}
	// Overwrite fired_at to the requested instant so lag math is predictable.
	if _, err := store.db.Exec(
		`UPDATE memory_triggers_fired SET fired_at = ? WHERE memory_uuid = ? AND scheduled_for = ?`,
		fired.UTC().Format(time.RFC3339), memoryUUID, scheduled.UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("override fired_at: %v", err)
	}
	if err := store.MarkTriggerOutcome(memoryUUID, scheduled, outcome, runID); err != nil {
		t.Fatalf("mark outcome: %v", err)
	}
}

func TestFormatTriggersFired_Empty(t *testing.T) {
	out := formatTriggersFired(nil)
	if !strings.Contains(out, "No trigger fires found") {
		t.Errorf("empty state: %q", out)
	}
}

func TestFormatTriggersFired_RendersRow(t *testing.T) {
	scheduled := time.Date(2026, 4, 21, 17, 45, 0, 0, time.Local)
	fired := scheduled.Add(2 * time.Second)
	rows := []*TriggerFired{
		{
			ID:           1,
			MemoryUUID:   "u-lift",
			ScheduledFor: scheduled,
			FiredAt:      fired,
			Username:     "u",
			SessionKey:   "primary",
			Outcome:      "fired",
			RunID:        "r_abc",
			Content:      "lifting at gym",
		},
	}
	out := formatTriggersFired(rows)
	if !strings.Contains(out, "## Trigger Fires (1)") {
		t.Errorf("header missing: %q", out)
	}
	if !strings.Contains(out, "lifting at gym (id: u-lift)") {
		t.Errorf("content+id missing: %q", out)
	}
	if !strings.Contains(out, "scheduled: 2026-04-21 17:45") {
		t.Errorf("scheduled missing: %q", out)
	}
	if !strings.Contains(out, "(lag 2s)") {
		t.Errorf("lag missing: %q", out)
	}
	if !strings.Contains(out, "outcome: fired") {
		t.Errorf("outcome missing: %q", out)
	}
	if !strings.Contains(out, "run: r_abc") {
		t.Errorf("run id missing: %q", out)
	}
}

func TestFormatTriggersFired_MissingContent(t *testing.T) {
	scheduled := time.Date(2026, 4, 21, 17, 45, 0, 0, time.Local)
	fired := scheduled.Add(1 * time.Second)
	rows := []*TriggerFired{{
		MemoryUUID: "u-gone", ScheduledFor: scheduled, FiredAt: fired, Outcome: "fired",
	}}
	out := formatTriggersFired(rows)
	if !strings.Contains(out, "(memory not found)") {
		t.Errorf("expected memory-not-found placeholder: %q", out)
	}
}

func TestQueryTriggersFired_Filters(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	store := NewStore(db)

	uuidA := seedRoutine(t, store, "alice", "lifting", &RoutineMetadata{
		Days: []string{"tue"}, TimeStart: "17:45",
	})
	uuidB := seedRoutine(t, store, "alice", "yoga", &RoutineMetadata{
		Days: []string{"mon"}, TimeStart: "07:00",
	})
	uuidC := seedRoutine(t, store, "bob", "run", &RoutineMetadata{
		Days: []string{"sat"}, TimeStart: "09:00",
	})

	now := time.Now().Truncate(time.Second)
	ago2h := now.Add(-2 * time.Hour)
	ago30m := now.Add(-30 * time.Minute)
	ago5m := now.Add(-5 * time.Minute)

	claimFire(t, store, uuidA, "alice", ago2h, ago2h.Add(time.Second), "fired", "r_a")
	claimFire(t, store, uuidB, "alice", ago30m, ago30m.Add(time.Second), "silent", "r_b")
	claimFire(t, store, uuidC, "bob", ago5m, ago5m.Add(time.Second), "fired", "r_c")

	// Username scoping.
	got, err := store.QueryTriggersFired(TriggerQueryParams{Username: "alice", Limit: 10})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 alice rows, got %d", len(got))
	}

	// Outcome filter.
	silent, err := store.QueryTriggersFired(TriggerQueryParams{Username: "alice", Outcome: "silent", Limit: 10})
	if err != nil {
		t.Fatalf("outcome query: %v", err)
	}
	if len(silent) != 1 || silent[0].MemoryUUID != uuidB {
		t.Errorf("expected only uuidB, got %+v", silent)
	}
	if silent[0].Content != "yoga" {
		t.Errorf("expected content join, got %q", silent[0].Content)
	}

	// memory_uuid scoping.
	bOnly, err := store.QueryTriggersFired(TriggerQueryParams{MemoryUUID: uuidB, Limit: 10})
	if err != nil {
		t.Fatalf("uuid query: %v", err)
	}
	if len(bOnly) != 1 || bOnly[0].MemoryUUID != uuidB {
		t.Errorf("expected one row for uuidB, got %+v", bOnly)
	}

	// since window excludes old fires.
	since := now.Add(-10 * time.Minute)
	recent, err := store.QueryTriggersFired(TriggerQueryParams{Since: &since, Limit: 10})
	if err != nil {
		t.Fatalf("since query: %v", err)
	}
	if len(recent) != 1 || recent[0].MemoryUUID != uuidC {
		t.Errorf("expected bob's recent row, got %+v", recent)
	}

	// before window excludes newer fires.
	before := now.Add(-1 * time.Hour)
	old, err := store.QueryTriggersFired(TriggerQueryParams{Before: &before, Limit: 10})
	if err != nil {
		t.Fatalf("before query: %v", err)
	}
	if len(old) != 1 || old[0].MemoryUUID != uuidA {
		t.Errorf("expected only alice's old row, got %+v", old)
	}
}

func TestRecentTriggerFired_DelegatesToQuery(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	store := NewStore(db)

	uuid := seedRoutine(t, store, "u", "x", &RoutineMetadata{Days: []string{"mon"}, TimeStart: "08:00"})
	now := time.Now().Truncate(time.Second)
	claimFire(t, store, uuid, "u", now.Add(-time.Minute), now, "fired", "r1")

	rows, err := store.RecentTriggerFired(5)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Content != "x" {
		t.Errorf("expected content join via delegate, got %q", rows[0].Content)
	}
}
