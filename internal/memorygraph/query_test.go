package memorygraph

import (
	"testing"
	"time"
)

func TestQueryBuilder(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(db)

	// Create test memories
	memories := []*Memory{
		{Content: "User is a software developer", Type: TypeIdentity, Importance: 1.0, Username: "alice"},
		{Content: "User prefers dark mode", Type: TypePreference, Importance: 0.7, Username: "alice"},
		{Content: "User prefers morning meetings", Type: TypePreference, Importance: 0.6, Username: "alice"},
		{Content: "Completed project X", Type: TypeEvent, Importance: 0.5, Username: "alice"},
		{Content: "Bob likes coffee", Type: TypePreference, Importance: 0.7, Username: "bob"},
	}

	for _, mem := range memories {
		if err := store.CreateMemory(mem); err != nil {
			t.Fatalf("CreateMemory failed: %v", err)
		}
	}

	// Test type filter
	t.Run("FilterByType", func(t *testing.T) {
		results, err := Query().
			Types(TypePreference).
			Execute(db)

		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		if len(results) != 3 {
			t.Errorf("expected 3 preferences, got %d", len(results))
		}

		for _, r := range results {
			if r.Type != TypePreference {
				t.Errorf("expected type preference, got %s", r.Type)
			}
		}
	})

	// Test username filter
	t.Run("FilterByUsername", func(t *testing.T) {
		results, err := Query().
			Username("alice").
			Execute(db)

		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		if len(results) != 4 {
			t.Errorf("expected 4 results for alice, got %d", len(results))
		}

		for _, r := range results {
			if r.Username != "alice" {
				t.Errorf("expected username alice, got %s", r.Username)
			}
		}
	})

	// Test importance filter
	t.Run("FilterByMinImportance", func(t *testing.T) {
		results, err := Query().
			MinImportance(0.7).
			Execute(db)

		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		for _, r := range results {
			if r.Importance < 0.7 {
				t.Errorf("expected importance >= 0.7, got %f", r.Importance)
			}
		}
	})

	// Test ordering
	t.Run("OrderByImportance", func(t *testing.T) {
		results, err := Query().
			Username("alice").
			OrderBy("importance").
			Descending().
			Execute(db)

		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		// Should be sorted by importance descending
		for i := 1; i < len(results); i++ {
			if results[i].Importance > results[i-1].Importance {
				t.Errorf("not sorted by importance descending")
				break
			}
		}
	})

	// Test limit
	t.Run("Limit", func(t *testing.T) {
		results, err := Query().
			Limit(2).
			Execute(db)

		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		if len(results) != 2 {
			t.Errorf("expected 2 results, got %d", len(results))
		}
	})

	// Test combined filters
	t.Run("CombinedFilters", func(t *testing.T) {
		results, err := Query().
			Username("alice").
			Types(TypePreference).
			MinImportance(0.6).
			OrderBy("importance").
			Descending().
			Limit(10).
			Execute(db)

		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		if len(results) != 2 {
			t.Errorf("expected 2 results, got %d", len(results))
		}
	})
}

func TestQueryBuilderTriggers(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(db)

	now := time.Now()
	future := now.Add(24 * time.Hour)
	past := now.Add(-24 * time.Hour)

	// Create memories with triggers
	mem1 := &Memory{Content: "Past trigger", Type: TypeRoutine, NextTriggerAt: &past}
	mem2 := &Memory{Content: "Future trigger", Type: TypeRoutine, NextTriggerAt: &future}
	mem3 := &Memory{Content: "No trigger", Type: TypeRoutine}

	for _, mem := range []*Memory{mem1, mem2, mem3} {
		if err := store.CreateMemory(mem); err != nil {
			t.Fatalf("CreateMemory failed: %v", err)
		}
	}

	// Query for pending triggers
	results, err := Query().
		HasTriggerBefore(now).
		Execute(db)

	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("expected 1 pending trigger, got %d", len(results))
	}

	if len(results) > 0 && results[0].Content != "Past trigger" {
		t.Errorf("expected 'Past trigger', got %q", results[0].Content)
	}
}

func TestAssociationQuery(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(db)

	// Create memories
	mem1 := &Memory{Content: "Central memory", Type: TypeFact}
	mem2 := &Memory{Content: "Related memory 1", Type: TypeFact}
	mem3 := &Memory{Content: "Related memory 2", Type: TypeFact}

	for _, mem := range []*Memory{mem1, mem2, mem3} {
		if err := store.CreateMemory(mem); err != nil {
			t.Fatalf("CreateMemory failed: %v", err)
		}
	}

	// Create associations
	assoc1 := &Association{
		SourceID:     mem1.UUID,
		TargetID:     mem2.UUID,
		RelationType: RelationRelatedTo,
		Directed:     false,
	}
	assoc2 := &Association{
		SourceID:     mem1.UUID,
		TargetID:     mem3.UUID,
		RelationType: RelationUpdates,
		Directed:     true,
	}

	for _, a := range []*Association{assoc1, assoc2} {
		if err := store.CreateAssociation(a); err != nil {
			t.Fatalf("CreateAssociation failed: %v", err)
		}
	}

	// Query from mem1
	t.Run("FromQuery", func(t *testing.T) {
		results, err := QueryAssociations(mem1.UUID).
			From().
			Execute(db)

		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		if len(results) != 2 {
			t.Errorf("expected 2 associations from mem1, got %d", len(results))
		}
	})

	// Query by type
	t.Run("FilterByType", func(t *testing.T) {
		results, err := QueryAssociations(mem1.UUID).
			From().
			Types(RelationUpdates).
			Execute(db)

		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		if len(results) != 1 {
			t.Errorf("expected 1 association of type updates, got %d", len(results))
		}
	})
}

func TestGetRelatedMemories(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(db)

	// Create a chain of memories
	mem1 := &Memory{Content: "Root", Type: TypeFact}
	mem2 := &Memory{Content: "Level 1", Type: TypeFact}
	mem3 := &Memory{Content: "Level 2", Type: TypeFact}

	for _, mem := range []*Memory{mem1, mem2, mem3} {
		if err := store.CreateMemory(mem); err != nil {
			t.Fatalf("CreateMemory failed: %v", err)
		}
	}

	// Create chain: mem1 -> mem2 -> mem3
	store.CreateAssociation(&Association{
		SourceID:     mem1.UUID,
		TargetID:     mem2.UUID,
		RelationType: RelationRelatedTo,
		Directed:     false,
	})
	store.CreateAssociation(&Association{
		SourceID:     mem2.UUID,
		TargetID:     mem3.UUID,
		RelationType: RelationRelatedTo,
		Directed:     false,
	})

	// Get related with depth 1
	related, err := GetRelatedMemories(db, mem1.UUID, 1, nil)
	if err != nil {
		t.Fatalf("GetRelatedMemories failed: %v", err)
	}

	if len(related) != 1 {
		t.Errorf("expected 1 related memory at depth 1, got %d", len(related))
	}

	// Get related with depth 2
	related, err = GetRelatedMemories(db, mem1.UUID, 2, nil)
	if err != nil {
		t.Fatalf("GetRelatedMemories failed: %v", err)
	}

	if len(related) != 2 {
		t.Errorf("expected 2 related memories at depth 2, got %d", len(related))
	}
}
