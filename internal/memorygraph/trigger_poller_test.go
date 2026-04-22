package memorygraph

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeInvoker records memtrigger invocations and can return canned outcomes.
type fakeInvoker struct {
	mu    sync.Mutex
	calls []fakeCall

	retRunID string
	retErr   error
}

type fakeCall struct {
	user     string
	memUUID  string
	preamble string
}

func (f *fakeInvoker) RunAgentForMemoryTrigger(_ context.Context, user, memUUID, preamble string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCall{user, memUUID, preamble})
	return f.retRunID, f.retErr
}

func (f *fakeInvoker) Calls() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func newTestPoller(t *testing.T, store *Store, invoker TriggerInvoker, grace time.Duration) *TriggerPoller {
	t.Helper()
	cfg := TriggerConfig{Enabled: true, PollIntervalSeconds: 60, MissedGraceMinutes: int(grace.Minutes())}
	return NewTriggerPoller(store, invoker, cfg)
}

func TestTriggerPoller_FiresAndAdvances(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	store := NewStore(db)

	// Daily routine due 1 minute ago at any-daily @ a time we'll pin as NOW-ish.
	now := time.Now()
	past := now.Add(-1 * time.Minute)
	uuid := seedRoutine(t, store, "alice", "take meds", &RoutineMetadata{
		Days: []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"},
		TimeStart: past.Format("15:04"),
	})
	if err := store.SetNextTriggerAt(uuid, &past); err != nil {
		t.Fatalf("SetNextTriggerAt: %v", err)
	}

	inv := &fakeInvoker{retRunID: "run-xyz"}
	poller := newTestPoller(t, store, inv, 30*time.Minute)
	poller.tick(context.Background())

	calls := inv.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 invocation, got %d", len(calls))
	}
	if calls[0].user != "alice" || calls[0].memUUID != uuid {
		t.Errorf("invocation target: got %+v", calls[0])
	}

	// Outcome recorded.
	rows, err := store.RecentTriggerFired(10)
	if err != nil {
		t.Fatalf("RecentTriggerFired: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 fired row, got %d", len(rows))
	}
	if rows[0].Outcome != "fired" || rows[0].RunID != "run-xyz" {
		t.Errorf("outcome: got %+v", rows[0])
	}

	// next_trigger_at advanced to tomorrow-ish.
	mem, err := store.GetMemory(uuid)
	if err != nil {
		t.Fatal(err)
	}
	if mem.NextTriggerAt == nil {
		t.Fatalf("next_trigger_at not updated")
	}
	if !mem.NextTriggerAt.After(now) {
		t.Errorf("next_trigger_at should be in the future: got %v", mem.NextTriggerAt)
	}
}

func TestTriggerPoller_IdempotentClaim(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	store := NewStore(db)

	now := time.Now()
	past := now.Add(-1 * time.Minute)
	uuid := seedRoutine(t, store, "alice", "stretch", &RoutineMetadata{
		Days: []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"},
		TimeStart: past.Format("15:04"),
	})
	_ = store.SetNextTriggerAt(uuid, &past)

	inv := &fakeInvoker{retRunID: "run-1"}
	poller := newTestPoller(t, store, inv, 30*time.Minute)
	poller.tick(context.Background())

	// Now reset next_trigger_at back to past to simulate a crash mid-flight.
	_ = store.SetNextTriggerAt(uuid, &past)

	// Second tick should not re-invoke: claim rejected by UNIQUE constraint.
	poller.tick(context.Background())

	if len(inv.Calls()) != 1 {
		t.Errorf("expected no duplicate fire, got %d calls", len(inv.Calls()))
	}
}

func TestTriggerPoller_MissedGrace(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	store := NewStore(db)

	now := time.Now()
	// 2h in the past — beyond a 30m grace window.
	past := now.Add(-2 * time.Hour)
	uuid := seedRoutine(t, store, "alice", "stale", &RoutineMetadata{
		Days: []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"},
		TimeStart: past.Format("15:04"),
	})
	_ = store.SetNextTriggerAt(uuid, &past)

	inv := &fakeInvoker{retRunID: "run-should-not-fire"}
	poller := newTestPoller(t, store, inv, 30*time.Minute)
	poller.tick(context.Background())

	if len(inv.Calls()) != 0 {
		t.Errorf("expected no invocation past grace, got %d", len(inv.Calls()))
	}

	// Should still have claimed (audit) with missed_grace outcome.
	rows, err := store.RecentTriggerFired(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected audit row, got %d", len(rows))
	}
	if rows[0].Outcome != "missed_grace" {
		t.Errorf("outcome: got %q", rows[0].Outcome)
	}

	// next_trigger_at advanced.
	mem, _ := store.GetMemory(uuid)
	if mem.NextTriggerAt == nil || !mem.NextTriggerAt.After(now) {
		t.Errorf("next_trigger_at not advanced past missed grace: %v", mem.NextTriggerAt)
	}
}

func TestTriggerPoller_PastEndsOn_ClearsNext(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	store := NewStore(db)

	now := time.Now()
	past := now.Add(-1 * time.Minute)
	// EndsOn yesterday — today's firing should be defensive-skipped since
	// the scheduled_for falls outside bounds, and next_trigger_at should
	// be cleared.
	ends := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, time.Local)
	uuid := seedRoutine(t, store, "alice", "expired", &RoutineMetadata{
		Days:      []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"},
		TimeStart: past.Format("15:04"),
		EndsOn:    &ends,
	})
	_ = store.SetNextTriggerAt(uuid, &past)

	inv := &fakeInvoker{}
	poller := newTestPoller(t, store, inv, 30*time.Minute)
	poller.tick(context.Background())

	if len(inv.Calls()) != 0 {
		t.Errorf("expected no invocation past EndsOn, got %d", len(inv.Calls()))
	}
	mem, _ := store.GetMemory(uuid)
	if mem.NextTriggerAt != nil {
		t.Errorf("expected next_trigger_at cleared, got %v", mem.NextTriggerAt)
	}
}

func TestTriggerPoller_SkipDate(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	store := NewStore(db)

	now := time.Now()
	past := now.Add(-1 * time.Minute)
	today := now.Format("2006-01-02")
	uuid := seedRoutine(t, store, "alice", "skipping", &RoutineMetadata{
		Days:      []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"},
		TimeStart: past.Format("15:04"),
		SkipDates: []string{today},
	})
	_ = store.SetNextTriggerAt(uuid, &past)

	inv := &fakeInvoker{}
	poller := newTestPoller(t, store, inv, 30*time.Minute)
	poller.tick(context.Background())

	if len(inv.Calls()) != 0 {
		t.Errorf("expected no invocation on skip date, got %d", len(inv.Calls()))
	}
	// next_trigger_at should be advanced past today (either tomorrow or
	// further if today is skipped).
	mem, _ := store.GetMemory(uuid)
	if mem.NextTriggerAt == nil || !mem.NextTriggerAt.After(now) {
		t.Errorf("next_trigger_at not advanced past skip: %v", mem.NextTriggerAt)
	}
}

func TestTriggerPoller_SilentOutcome(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	store := NewStore(db)

	now := time.Now()
	past := now.Add(-1 * time.Minute)
	uuid := seedRoutine(t, store, "alice", "quiet", &RoutineMetadata{
		Days:      []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"},
		TimeStart: past.Format("15:04"),
	})
	_ = store.SetNextTriggerAt(uuid, &past)

	inv := &fakeInvoker{retRunID: ""} // silent outcome
	poller := newTestPoller(t, store, inv, 30*time.Minute)
	poller.tick(context.Background())

	rows, _ := store.RecentTriggerFired(10)
	if len(rows) != 1 || rows[0].Outcome != "silent" {
		t.Errorf("outcome: got %v", rows)
	}
}

func TestBuildMemTriggerPreamble_Content(t *testing.T) {
	meta := &RoutineMetadata{Location: "gym", Person: "Bob"}
	dr := &DueRoutine{
		Memory:  &Memory{UUID: "u1", Content: "evening lifting"},
		Meta:    meta,
		OwnerID: "alice",
	}
	now := time.Date(2025, 10, 14, 17, 50, 0, 0, time.Local)
	sched := time.Date(2025, 10, 14, 17, 45, 0, 0, time.Local)
	p := buildMemTriggerPreamble(dr, sched, now)
	if !(contains(p, "evening lifting") && contains(p, "gym") && contains(p, "Bob") &&
		contains(p, "SILENT_OK") && contains(p, "memtrigger")) {
		t.Errorf("preamble missing expected content:\n%s", p)
	}

	// Overdue lag > 2m should include "(overdue by ...)"
	late := sched.Add(30 * time.Minute)
	p2 := buildMemTriggerPreamble(dr, sched, late)
	if !contains(p2, "overdue by") {
		t.Errorf("expected overdue note:\n%s", p2)
	}
}

// contains is a tiny helper to avoid pulling strings in the test-only logic.
func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
