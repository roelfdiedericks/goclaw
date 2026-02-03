package memory

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "github.com/mattn/go-sqlite3"
	"github.com/roelfdiedericks/goclaw/internal/config"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Manager coordinates memory indexing and search
type Manager struct {
	db           *sql.DB
	indexer      *Indexer
	provider     EmbeddingProvider
	workspaceDir string
	config       config.MemorySearchConfig

	mu     sync.RWMutex
	closed bool
}

// NewManager creates a new memory manager
func NewManager(cfg config.MemorySearchConfig, workspaceDir string) (*Manager, error) {
	if !cfg.Enabled {
		L_info("memory: disabled by configuration")
		return nil, nil
	}

	L_info("memory: initializing manager", "workspace", workspaceDir)

	// Determine database path
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".openclaw", "goclaw", "memory.db")

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	// Open database
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Initialize schema
	if err := initSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	// Create embedding provider
	var provider EmbeddingProvider
	var ollamaProvider *OllamaProvider
	if cfg.Ollama.URL != "" {
		ollamaProvider = NewOllamaProvider(cfg.Ollama.URL, cfg.Ollama.Model)
		provider = ollamaProvider
		L_info("memory: using ollama provider", "url", cfg.Ollama.URL, "model", cfg.Ollama.Model)
	} else {
		provider = &NoopProvider{}
		L_info("memory: using keyword-only search (no embedding provider configured)")
	}

	// Create indexer
	indexer := NewIndexer(db, provider, workspaceDir, cfg.Paths)

	// Wire up Ollama ready callback to trigger re-index with embeddings
	if ollamaProvider != nil {
		ollamaProvider.OnReady(func() {
			L_info("memory: ollama ready, triggering re-index for embeddings")
			indexer.MarkDirty()
			indexer.TriggerSync()
		})
	}

	m := &Manager{
		db:           db,
		indexer:      indexer,
		provider:     provider,
		workspaceDir: workspaceDir,
		config:       cfg,
	}

	L_info("memory: manager created", "dbPath", dbPath, "provider", provider.ID())

	return m, nil
}

// Provider returns the embedding provider (for sharing with transcript indexer)
func (m *Manager) Provider() EmbeddingProvider {
	return m.provider
}

// Start begins background indexing
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return fmt.Errorf("manager is closed")
	}

	return m.indexer.Start()
}

// Close stops the manager and releases resources
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil
	}
	m.closed = true

	L_info("memory: closing manager")

	// Stop indexer
	m.indexer.Stop()

	// Close database
	if err := m.db.Close(); err != nil {
		L_warn("memory: error closing database", "error", err)
		return err
	}

	L_debug("memory: manager closed")
	return nil
}

// Search performs a memory search
func (m *Manager) Search(ctx context.Context, query string, maxResults int, minScore float64) ([]SearchResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil, fmt.Errorf("manager is closed")
	}

	// Use config defaults if not specified
	if maxResults <= 0 {
		maxResults = m.config.Query.MaxResults
		if maxResults <= 0 {
			maxResults = 6
		}
	}
	if minScore <= 0 {
		minScore = m.config.Query.MinScore
		if minScore <= 0 {
			minScore = 0.35
		}
	}

	vectorWeight := m.config.Query.VectorWeight
	keywordWeight := m.config.Query.KeywordWeight
	if vectorWeight <= 0 {
		vectorWeight = 0.7
	}
	if keywordWeight <= 0 {
		keywordWeight = 0.3
	}

	opts := SearchOptions{
		MaxResults:    maxResults,
		MinScore:      minScore,
		VectorWeight:  vectorWeight,
		KeywordWeight: keywordWeight,
	}

	// Trigger sync if dirty (non-blocking, search uses current index)
	if m.indexer.IsDirty() {
		m.indexer.TriggerSync()
	}

	return Search(ctx, m.db, m.provider, query, opts)
}

// ReadFile reads a memory file with optional line range
func (m *Manager) ReadFile(path string, fromLine, numLines int) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return "", fmt.Errorf("manager is closed")
	}

	// Resolve path
	absPath := path
	if !filepath.IsAbs(path) {
		absPath = filepath.Join(m.workspaceDir, path)
	}

	// Security check: ensure path is within workspace or allowed paths
	if !m.isAllowedPath(absPath) {
		L_warn("memory: path not allowed", "path", path)
		return "", fmt.Errorf("path not allowed: %s", path)
	}

	// Check it's a markdown file
	if !strings.HasSuffix(strings.ToLower(absPath), ".md") {
		return "", fmt.Errorf("only markdown files allowed")
	}

	// Read file
	content, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	L_debug("memory: reading file", "path", path, "size", len(content), "fromLine", fromLine, "numLines", numLines)

	// If no line range specified, return full content
	if fromLine <= 0 && numLines <= 0 {
		return string(content), nil
	}

	// Extract line range
	lines := strings.Split(string(content), "\n")

	// Convert to 0-indexed
	start := fromLine - 1
	if start < 0 {
		start = 0
	}
	if start >= len(lines) {
		return "", nil
	}

	end := len(lines)
	if numLines > 0 {
		end = start + numLines
		if end > len(lines) {
			end = len(lines)
		}
	}

	return strings.Join(lines[start:end], "\n"), nil
}

// isAllowedPath checks if a path is allowed to be read
func (m *Manager) isAllowedPath(absPath string) bool {
	// Check if in workspace
	if strings.HasPrefix(absPath, m.workspaceDir) {
		relPath, _ := filepath.Rel(m.workspaceDir, absPath)
		// Allow memory files: MEMORY.md, HEARTBEAT.md, memory/*
		if relPath == "MEMORY.md" || relPath == "HEARTBEAT.md" || strings.HasPrefix(relPath, "memory"+string(filepath.Separator)) || relPath == "memory" {
			return true
		}
	}

	// Check extra paths
	for _, extra := range m.config.Paths {
		extraAbs := extra
		if !filepath.IsAbs(extra) {
			extraAbs = filepath.Join(m.workspaceDir, extra)
		}
		if strings.HasPrefix(absPath, extraAbs) {
			return true
		}
	}

	return false
}

// Stats returns current statistics
func (m *Manager) Stats() (files int, chunks int, provider string, available bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return 0, 0, "", false
	}

	files, chunks, _ = m.indexer.Stats()
	return files, chunks, m.provider.ID(), m.provider.Available()
}

// TriggerSync triggers a background sync
func (m *Manager) TriggerSync() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.closed {
		m.indexer.TriggerSync()
	}
}
