package memory

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/roelfdiedericks/goclaw/internal/llm"
)

func TestChunkMarkdown(t *testing.T) {
	content := `# Title

This is line one.
This is line two.
This is line three.

## Section

Some more content here.
And even more content.
`

	opts := ChunkOptions{
		TargetTokens:  20, // Very small for testing
		OverlapTokens: 5,
	}

	chunks := ChunkMarkdown(content, opts)

	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	// Verify chunks have required fields
	for i, chunk := range chunks {
		if chunk.Text == "" {
			t.Errorf("chunk %d has empty text", i)
		}
		if chunk.Hash == "" {
			t.Errorf("chunk %d has empty hash", i)
		}
		if chunk.StartLine < 1 {
			t.Errorf("chunk %d has invalid start line: %d", i, chunk.StartLine)
		}
		if chunk.EndLine < chunk.StartLine {
			t.Errorf("chunk %d has end line before start line: %d < %d", i, chunk.EndLine, chunk.StartLine)
		}
	}

	t.Logf("Created %d chunks", len(chunks))
	for i, c := range chunks {
		t.Logf("  Chunk %d: lines %d-%d, %d chars", i, c.StartLine, c.EndLine, len(c.Text))
	}
}

func TestSchema(t *testing.T) {
	// Create temp database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	// Initialize schema
	err = initSchema(db)
	if err != nil {
		t.Fatalf("failed to init schema: %v", err)
	}

	// Verify tables exist
	tables := []string{"memory_meta", "memory_files", "memory_chunks", "memory_fts", "embedding_cache"}
	for _, table := range tables {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Errorf("table %s not found: %v", table, err)
		}
	}

	// Verify FTS5 is working
	_, err = db.Exec("INSERT INTO memory_chunks (id, path, start_line, end_line, hash, text, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		"test1", "test.md", 1, 10, "abc123", "hello world test content", 1234567890)
	if err != nil {
		t.Fatalf("failed to insert test chunk: %v", err)
	}

	// Search via FTS5
	var id string
	err = db.QueryRow("SELECT id FROM memory_fts WHERE memory_fts MATCH ?", "hello*").Scan(&id)
	if err != nil {
		t.Fatalf("FTS5 search failed: %v", err)
	}
	if id != "test1" {
		t.Errorf("expected id 'test1', got '%s'", id)
	}

	t.Log("Schema and FTS5 working correctly")
}

func TestKeywordSearch(t *testing.T) {
	// Create temp database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	// Initialize schema
	err = initSchema(db)
	if err != nil {
		t.Fatalf("failed to init schema: %v", err)
	}

	// Insert test chunks
	testChunks := []struct {
		id   string
		path string
		text string
	}{
		{"chunk1", "memory/2026-01-01.md", "Today I worked on the authentication system using JWT tokens"},
		{"chunk2", "memory/2026-01-02.md", "Meeting with John about database design and PostgreSQL optimization"},
		{"chunk3", "MEMORY.md", "Important: Always use the read tool before editing files"},
		{"chunk4", "memory/2026-01-03.md", "Deployed the new authentication feature to production"},
	}

	for _, c := range testChunks {
		_, err = db.Exec(`
			INSERT INTO memory_chunks (id, path, start_line, end_line, hash, text, updated_at)
			VALUES (?, ?, 1, 10, ?, ?, ?)
		`, c.id, c.path, c.id, c.text, 1234567890)
		if err != nil {
			t.Fatalf("failed to insert chunk: %v", err)
		}
	}

	// Test search
	results, err := Search(context.Background(), db, &llm.NoopProvider{}, "authentication", DefaultSearchOptions())
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 results for 'authentication', got %d", len(results))
	}

	// Should find both chunks mentioning authentication
	foundChunk1 := false
	foundChunk4 := false
	for _, r := range results {
		t.Logf("Result: %s (score: %.3f)", r.Path, r.Score)
		if r.Path == "memory/2026-01-01.md" {
			foundChunk1 = true
		}
		if r.Path == "memory/2026-01-03.md" {
			foundChunk4 = true
		}
	}

	if !foundChunk1 || !foundChunk4 {
		t.Error("expected to find both authentication-related chunks")
	}

	// Test another search
	results, err = Search(context.Background(), db, &llm.NoopProvider{}, "database PostgreSQL", DefaultSearchOptions())
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	if len(results) == 0 {
		t.Error("expected at least 1 result for 'database PostgreSQL'")
	}

	t.Log("Keyword search working correctly")
}

func TestManagerIntegration(t *testing.T) {
	// Create temp workspace with memory files
	tmpDir := t.TempDir()
	memoryDir := filepath.Join(tmpDir, "memory")
	if err := os.MkdirAll(memoryDir, 0755); err != nil {
		t.Fatalf("failed to create memory dir: %v", err)
	}

	// Create test memory files
	memoryContent := `# 2026-01-15 Notes

## Project Work
- Fixed bug in the user registration flow
- Added validation for email addresses
- Refactored the authentication middleware

## Decisions
- Decided to use SQLite for the memory index
- Will implement vector search later
`
	if err := os.WriteFile(filepath.Join(memoryDir, "2026-01-15.md"), []byte(memoryContent), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	memoryMD := `# MEMORY.md

## Key Information
- Working on GoClaw project
- User prefers Go over TypeScript
- Ollama is at 192.168.56.1:11434
`
	if err := os.WriteFile(filepath.Join(tmpDir, "MEMORY.md"), []byte(memoryMD), 0644); err != nil {
		t.Fatalf("failed to write MEMORY.md: %v", err)
	}

	// Create manager (keyword-only mode until LLM registry is set up)
	cfg := MemorySearchConfig{
		Enabled: true,
		Query: MemorySearchQueryConfig{
			MaxResults:    6,
			MinScore:      0.1, // Lower threshold for testing
			VectorWeight:  0.7,
			KeywordWeight: 0.3,
		},
	}

	// Override default db path for testing
	home, _ := os.UserHomeDir()
	testDbDir := filepath.Join(home, ".goclaw", "test")
	os.MkdirAll(testDbDir, 0755)
	defer os.RemoveAll(testDbDir)

	mgr, err := NewManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	defer mgr.Close()

	// Start indexer
	if err := mgr.Start(); err != nil {
		t.Fatalf("failed to start manager: %v", err)
	}

	// Wait a bit for initial indexing
	// In a real test we'd use a sync mechanism
	ctx := context.Background()

	// Force sync
	mgr.TriggerSync()

	// Give it time to index
	// (In production we'd have better sync primitives)
	t.Log("Waiting for indexing...")

	// Test search
	results, err := mgr.Search(ctx, "authentication", 0, 0)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	t.Logf("Search 'authentication' returned %d results", len(results))
	for _, r := range results {
		t.Logf("  %s:%d-%d (%.3f): %s...", r.Path, r.StartLine, r.EndLine, r.Score, truncateForLog(r.Snippet, 50))
	}

	// Test ReadFile
	content, err := mgr.ReadFile("MEMORY.md", 0, 0)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if content == "" {
		t.Error("expected non-empty content from MEMORY.md")
	}
	t.Logf("ReadFile returned %d bytes", len(content))

	// Test ReadFile with line range
	content, err = mgr.ReadFile("MEMORY.md", 1, 3)
	if err != nil {
		t.Fatalf("ReadFile with range failed: %v", err)
	}
	t.Logf("ReadFile (lines 1-3) returned: %s", content)

	// Test path security
	_, err = mgr.ReadFile("/etc/passwd", 0, 0)
	if err == nil {
		t.Error("expected error when reading /etc/passwd")
	}

	files, chunks, provider, available := mgr.Stats()
	t.Logf("Stats: files=%d, chunks=%d, provider=%s, available=%v", files, chunks, provider, available)
}
