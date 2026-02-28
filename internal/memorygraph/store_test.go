package memorygraph

import (
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func setupTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	// Create temp file for test database
	f, err := os.CreateTemp("", "memorygraph_test_*.db")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	dbPath := f.Name()
	f.Close()

	// Open database
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=ON")
	if err != nil {
		os.Remove(dbPath)
		t.Fatalf("failed to open database: %v", err)
	}

	// Initialize schema
	if err := InitSchema(db); err != nil {
		db.Close()
		os.Remove(dbPath)
		t.Fatalf("failed to init schema: %v", err)
	}

	cleanup := func() {
		db.Close()
		os.Remove(dbPath)
	}

	return db, cleanup
}

func TestStoreCreateAndGetMemory(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(db)

	// Create a memory
	mem := &Memory{
		Content:    "User prefers dark mode",
		Type:       TypePreference,
		Importance: 0.8,
		Username:   "testuser",
	}

	err := store.CreateMemory(mem)
	if err != nil {
		t.Fatalf("CreateMemory failed: %v", err)
	}

	if mem.UUID == "" {
		t.Error("expected UUID to be set")
	}
	if mem.ID == 0 {
		t.Error("expected ID to be set")
	}

	// Retrieve the memory
	retrieved, err := store.GetMemory(mem.UUID)
	if err != nil {
		t.Fatalf("GetMemory failed: %v", err)
	}
	if retrieved == nil {
		t.Fatal("expected memory to be found")
	}

	if retrieved.Content != mem.Content {
		t.Errorf("content mismatch: got %q, want %q", retrieved.Content, mem.Content)
	}
	if retrieved.Type != mem.Type {
		t.Errorf("type mismatch: got %q, want %q", retrieved.Type, mem.Type)
	}
	if retrieved.Username != mem.Username {
		t.Errorf("username mismatch: got %q, want %q", retrieved.Username, mem.Username)
	}
}

func TestStoreUpdateMemory(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(db)

	// Create a memory
	mem := &Memory{
		Content:    "Initial content",
		Type:       TypeFact,
		Importance: 0.5,
	}

	if err := store.CreateMemory(mem); err != nil {
		t.Fatalf("CreateMemory failed: %v", err)
	}

	// Update the memory
	mem.Content = "Updated content"
	mem.Importance = 0.9

	if err := store.UpdateMemory(mem); err != nil {
		t.Fatalf("UpdateMemory failed: %v", err)
	}

	// Retrieve and verify
	retrieved, err := store.GetMemory(mem.UUID)
	if err != nil {
		t.Fatalf("GetMemory failed: %v", err)
	}

	if retrieved.Content != "Updated content" {
		t.Errorf("content not updated: got %q", retrieved.Content)
	}
	if retrieved.Importance != 0.9 {
		t.Errorf("importance not updated: got %f", retrieved.Importance)
	}
}

func TestStoreForgetMemory(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(db)

	mem := &Memory{
		Content: "To be forgotten",
		Type:    TypeObservation,
	}

	if err := store.CreateMemory(mem); err != nil {
		t.Fatalf("CreateMemory failed: %v", err)
	}

	if err := store.ForgetMemory(mem.UUID); err != nil {
		t.Fatalf("ForgetMemory failed: %v", err)
	}

	retrieved, err := store.GetMemory(mem.UUID)
	if err != nil {
		t.Fatalf("GetMemory failed: %v", err)
	}

	if !retrieved.Forgotten {
		t.Error("expected memory to be marked as forgotten")
	}
}

func TestStoreCreateAssociation(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(db)

	// Create two memories
	mem1 := &Memory{Content: "Memory 1", Type: TypeFact}
	mem2 := &Memory{Content: "Memory 2", Type: TypeFact}

	if err := store.CreateMemory(mem1); err != nil {
		t.Fatalf("CreateMemory 1 failed: %v", err)
	}
	if err := store.CreateMemory(mem2); err != nil {
		t.Fatalf("CreateMemory 2 failed: %v", err)
	}

	// Create association
	assoc := &Association{
		SourceID:     mem1.UUID,
		TargetID:     mem2.UUID,
		RelationType: RelationRelatedTo,
		Weight:       0.8,
	}

	if err := store.CreateAssociation(assoc); err != nil {
		t.Fatalf("CreateAssociation failed: %v", err)
	}

	if assoc.ID == "" {
		t.Error("expected association ID to be set")
	}

	// Retrieve associations
	assocs, err := store.GetAssociationsFrom(mem1.UUID)
	if err != nil {
		t.Fatalf("GetAssociationsFrom failed: %v", err)
	}

	if len(assocs) != 1 {
		t.Fatalf("expected 1 association, got %d", len(assocs))
	}

	if assocs[0].TargetID != mem2.UUID {
		t.Errorf("target mismatch: got %q, want %q", assocs[0].TargetID, mem2.UUID)
	}
}

func TestStoreTouchMemory(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(db)

	mem := &Memory{
		Content: "To be accessed",
		Type:    TypeFact,
	}

	if err := store.CreateMemory(mem); err != nil {
		t.Fatalf("CreateMemory failed: %v", err)
	}

	// Touch the memory multiple times
	for i := 0; i < 3; i++ {
		if err := store.TouchMemory(mem.UUID); err != nil {
			t.Fatalf("TouchMemory failed: %v", err)
		}
	}

	retrieved, err := store.GetMemory(mem.UUID)
	if err != nil {
		t.Fatalf("GetMemory failed: %v", err)
	}

	if retrieved.AccessCount != 3 {
		t.Errorf("access count mismatch: got %d, want 3", retrieved.AccessCount)
	}
}

func TestStoreRoutineMetadata(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(db)

	// Create a routine memory
	mem := &Memory{
		Content: "Morning coffee routine",
		Type:    TypeRoutine,
	}

	if err := store.CreateMemory(mem); err != nil {
		t.Fatalf("CreateMemory failed: %v", err)
	}

	// Set routine metadata
	now := time.Now()
	meta := &RoutineMetadata{
		MemoryUUID:      mem.UUID,
		TriggerType:     "time",
		TriggerCron:     "0 8 * * *",
		Action:          "suggest_coffee",
		Autonomy:        "suggest",
		Observations:    10,
		Acceptances:     8,
		LastTriggeredAt: &now,
	}

	if err := store.SetRoutineMetadata(meta); err != nil {
		t.Fatalf("SetRoutineMetadata failed: %v", err)
	}

	// Retrieve metadata
	retrieved, err := store.GetRoutineMetadata(mem.UUID)
	if err != nil {
		t.Fatalf("GetRoutineMetadata failed: %v", err)
	}
	if retrieved == nil {
		t.Fatal("expected metadata to be found")
	}

	if retrieved.TriggerCron != "0 8 * * *" {
		t.Errorf("trigger_cron mismatch: got %q", retrieved.TriggerCron)
	}
	if retrieved.Acceptances != 8 {
		t.Errorf("acceptances mismatch: got %d", retrieved.Acceptances)
	}
}
