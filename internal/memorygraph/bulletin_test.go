package memorygraph

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBuildMemoryBulletinIncludesUpcomingEvents(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(db)
	now := time.Now().UTC().Truncate(time.Second)
	upcoming := now.Add(48 * time.Hour)
	later := now.AddDate(0, 2, 0)

	memories := []*Memory{
		{Content: "Pay tax bill", Type: TypeTodo, Username: "alice", HappensAt: &upcoming},
		{Content: "Conference trip", Type: TypeEvent, Username: "alice", HappensAt: &later},
		{Content: "Undated idea", Type: TypeGoal, Username: "alice"},
	}
	for _, mem := range memories {
		if err := store.CreateMemory(mem); err != nil {
			t.Fatalf("CreateMemory failed: %v", err)
		}
	}

	cfg := BulletinConfig{
		Deduplicate:         true,
		UpcomingEventsLimit: 5,
		UpcomingEventsDays:  90,
	}
	NormalizeBulletinConfig(&cfg)

	mgr := &Manager{db: db, store: store}
	bulletin, err := BuildMemoryBulletinWithConfig(context.Background(), mgr, "alice", cfg)
	if err != nil {
		t.Fatalf("BuildMemoryBulletinWithConfig failed: %v", err)
	}

	if !strings.Contains(bulletin, "## Upcoming Events") {
		t.Fatalf("expected upcoming events section, got:\n%s", bulletin)
	}
	if !strings.Contains(bulletin, "Pay tax bill") {
		t.Fatalf("expected near-term scheduled todo in bulletin, got:\n%s", bulletin)
	}
	if !strings.Contains(bulletin, "Conference trip") {
		t.Fatalf("expected later scheduled event in bulletin, got:\n%s", bulletin)
	}
}
