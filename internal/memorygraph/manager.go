package memorygraph

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/paths"
)

// Manager coordinates memory graph operations including storage, search, and maintenance
type Manager struct {
	db       *sql.DB
	store    *Store
	provider llm.EmbeddingProvider
	config   Config

	mu          sync.RWMutex
	closed      bool
	llmEventSub bus.SubscriptionID

	// Background maintenance
	maintenanceTicker *time.Ticker
	maintenanceDone   chan struct{}
}

var (
	globalManager *Manager
	managerMu     sync.RWMutex
)

// GetManager returns the global memory graph manager instance
func GetManager() *Manager {
	managerMu.RLock()
	defer managerMu.RUnlock()
	return globalManager
}

// NewManager creates a new memory graph manager
func NewManager(cfg Config) (*Manager, error) {
	if !cfg.Enabled {
		L_info("memorygraph: disabled by configuration")
		return nil, nil
	}

	L_info("memorygraph: initializing manager")

	// Determine database path
	dbPath := cfg.DBPath
	if dbPath == "" {
		var err error
		dbPath, err = paths.DataPath("memory_graph.db")
		if err != nil {
			return nil, fmt.Errorf("get memory_graph db path: %w", err)
		}
	} else if strings.HasPrefix(dbPath, "~") {
		expanded, err := paths.ExpandTilde(dbPath)
		if err != nil {
			return nil, fmt.Errorf("expand db path: %w", err)
		}
		dbPath = expanded
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(dbPath), 0750); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	L_debug("memorygraph: using database", "path", dbPath)

	// Open database with WAL mode for better concurrency
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Initialize schema
	if err := InitSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	// Create store
	store := NewStore(db)

	// Start with NoopProvider - real provider will be resolved lazily
	provider := &llm.NoopProvider{}

	m := &Manager{
		db:       db,
		store:    store,
		provider: provider,
		config:   cfg,
	}

	// Subscribe to LLM config changes to refresh embedding provider
	m.llmEventSub = bus.SubscribeEvent("llm.config.applied", m.onLLMConfigApplied)

	// Set global instance
	managerMu.Lock()
	globalManager = m
	managerMu.Unlock()

	L_info("memorygraph: manager created", "dbPath", dbPath)

	return m, nil
}

// onLLMConfigApplied handles the llm.config.applied event by refreshing the embedding provider
func (m *Manager) onLLMConfigApplied(e bus.Event) {
	L_debug("memorygraph: received llm.config.applied event")
	m.refreshProvider()
}

// Provider returns the embedding provider
func (m *Manager) Provider() llm.EmbeddingProvider {
	m.refreshProvider()
	return m.provider
}

// refreshProvider checks if a better provider is available and updates if so
func (m *Manager) refreshProvider() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return
	}

	registry := llm.GetRegistry()
	if registry == nil {
		return
	}

	provider, err := registry.GetProvider("embeddings")
	if err != nil {
		L_debug("memorygraph: refreshProvider GetProvider failed", "error", err)
		return
	}

	// Adapt llm.Provider to llm.EmbeddingProvider interface
	embedder, ok := provider.(llm.LLMEmbedder)
	if !ok {
		L_debug("memorygraph: provider does not support embeddings")
		return
	}
	newProvider := llm.NewLLMProviderAdapter(embedder)

	// Check if provider changed
	currentID := m.provider.ID()
	newID := newProvider.ID()

	if currentID != newID || (currentID == "none" && newID != "none") {
		L_info("memorygraph: switching embedding provider", "from", currentID, "to", newID, "model", newProvider.Model())
		m.provider = newProvider
	}
}

// DB returns the underlying database for direct queries
func (m *Manager) DB() *sql.DB {
	return m.db
}

// Store returns the memory store
func (m *Manager) Store() *Store {
	return m.store
}

// Config returns the current configuration
func (m *Manager) Config() Config {
	return m.config
}

// CreateMemory creates a new memory with automatic embedding generation
func (m *Manager) CreateMemory(ctx context.Context, mem *Memory) error {
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return fmt.Errorf("manager is closed")
	}
	provider := m.provider
	m.mu.RUnlock()

	// Create the memory first
	if err := m.store.CreateMemory(mem); err != nil {
		return err
	}

	// Generate embedding if provider is available
	if provider != nil && provider.Available() {
		embedding, err := provider.EmbedQuery(ctx, mem.Content)
		if err != nil {
			L_warn("memorygraph: failed to generate embedding", "uuid", mem.UUID, "error", err)
		} else if embedding != nil {
			mem.Embedding = embedding
			mem.EmbeddingModel = provider.Model()
			if err := m.store.UpdateEmbedding(mem.UUID, embedding, provider.Model()); err != nil {
				L_warn("memorygraph: failed to store embedding", "uuid", mem.UUID, "error", err)
			}
		}
	}

	return nil
}

// GetMemory retrieves a memory by UUID
func (m *Manager) GetMemory(uuid string) (*Memory, error) {
	return m.store.GetMemory(uuid)
}

// UpdateMemory updates an existing memory and regenerates embedding if content changed
func (m *Manager) UpdateMemory(ctx context.Context, mem *Memory, regenerateEmbedding bool) error {
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return fmt.Errorf("manager is closed")
	}
	provider := m.provider
	m.mu.RUnlock()

	if err := m.store.UpdateMemory(mem); err != nil {
		return err
	}

	// Regenerate embedding if requested and provider available
	if regenerateEmbedding && provider != nil && provider.Available() {
		embedding, err := provider.EmbedQuery(ctx, mem.Content)
		if err != nil {
			L_warn("memorygraph: failed to regenerate embedding", "uuid", mem.UUID, "error", err)
		} else if embedding != nil {
			mem.Embedding = embedding
			mem.EmbeddingModel = provider.Model()
			if err := m.store.UpdateEmbedding(mem.UUID, embedding, provider.Model()); err != nil {
				L_warn("memorygraph: failed to store embedding", "uuid", mem.UUID, "error", err)
			}
		}
	}

	return nil
}

// ForgetMemory soft-deletes a memory
func (m *Manager) ForgetMemory(uuid string) error {
	return m.store.ForgetMemory(uuid)
}

// DeleteMemory permanently removes a memory
func (m *Manager) DeleteMemory(uuid string) error {
	return m.store.DeleteMemory(uuid)
}

// TouchMemory marks a memory as accessed
func (m *Manager) TouchMemory(uuid string) error {
	return m.store.TouchMemory(uuid)
}

// CreateAssociation creates an association between two memories
func (m *Manager) CreateAssociation(assoc *Association) error {
	return m.store.CreateAssociation(assoc)
}

// Query returns a new query builder
func (m *Manager) Query() *QueryOptions {
	return Query()
}

// Search performs hybrid search across the memory graph
func (m *Manager) Search(ctx context.Context, opts SearchOptions) ([]SearchResult, error) {
	m.mu.RLock()
	provider := m.provider
	m.mu.RUnlock()

	searcher := NewSearcher(m.db, provider, m.config.Search)
	return searcher.Search(ctx, opts)
}

// ExecuteQuery runs a query against the database
func (m *Manager) ExecuteQuery(q *QueryOptions) ([]*Memory, error) {
	return q.Execute(m.db)
}

// GetPendingTriggers returns memories with triggers due before the given time
func (m *Manager) GetPendingTriggers(before time.Time) ([]*Memory, error) {
	return Query().
		HasTriggerBefore(before).
		Types(TypeRoutine, TypePrediction).
		Limit(100).
		Execute(m.db)
}

// StartMaintenance starts the background maintenance routine
func (m *Manager) StartMaintenance(ctx context.Context) {
	if !m.config.Maintenance.Enabled {
		L_info("memorygraph: maintenance disabled")
		return
	}

	interval := m.config.Maintenance.IntervalHours
	if interval <= 0 {
		interval = 24 // Default to daily
	}

	m.maintenanceTicker = time.NewTicker(time.Duration(interval) * time.Hour)
	m.maintenanceDone = make(chan struct{})

	go func() {
		L_info("memorygraph: maintenance routine started", "intervalHours", interval)

		for {
			select {
			case <-m.maintenanceTicker.C:
				report, err := m.RunMaintenance(ctx)
				if err != nil {
					L_error("memorygraph: maintenance failed", "error", err)
				} else {
					L_info("memorygraph: maintenance completed",
						"decayed", report.ImportanceDecayed+report.ConfidenceDecayed,
						"boosted", report.Boosted,
						"pruned", report.Pruned,
						"merged", report.Merged,
					)
				}

			case <-m.maintenanceDone:
				return

			case <-ctx.Done():
				return
			}
		}
	}()
}

// RunMaintenance performs decay, pruning, and other maintenance tasks
func (m *Manager) RunMaintenance(ctx context.Context) (*MaintenanceReport, error) {
	maintainer := NewMaintainer(m.db, m.config.Maintenance)
	return maintainer.Run(ctx)
}

// Close shuts down the manager and releases resources
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil
	}
	m.closed = true

	// Stop maintenance
	if m.maintenanceTicker != nil {
		m.maintenanceTicker.Stop()
		close(m.maintenanceDone)
	}

	// Unsubscribe from bus events
	if m.llmEventSub != 0 {
		bus.UnsubscribeEvent(m.llmEventSub)
	}

	// Clear global instance
	managerMu.Lock()
	if globalManager == m {
		globalManager = nil
	}
	managerMu.Unlock()

	L_info("memorygraph: closing database")
	return m.db.Close()
}

// BackfillEmbeddings generates embeddings for memories that don't have them
func (m *Manager) BackfillEmbeddings(ctx context.Context, batchSize int) (int, error) {
	m.mu.RLock()
	provider := m.provider
	m.mu.RUnlock()

	if provider == nil || !provider.Available() {
		return 0, fmt.Errorf("no embedding provider available")
	}

	if batchSize <= 0 {
		batchSize = 100
	}

	rows, err := m.db.QueryContext(ctx, `
		SELECT uuid, content FROM memories 
		WHERE (embedding IS NULL OR embedding_model != ?)
		AND forgotten = 0
		LIMIT ?
	`, provider.Model(), batchSize)
	if err != nil {
		return 0, fmt.Errorf("query memories for backfill: %w", err)
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		var uuid, content string
		if err := rows.Scan(&uuid, &content); err != nil {
			continue
		}

		embedding, err := provider.EmbedQuery(ctx, content)
		if err != nil {
			L_warn("memorygraph: backfill embedding failed", "uuid", uuid, "error", err)
			continue
		}

		if embedding != nil {
			if err := m.store.UpdateEmbedding(uuid, embedding, provider.Model()); err != nil {
				L_warn("memorygraph: backfill store failed", "uuid", uuid, "error", err)
				continue
			}
			count++
		}

		select {
		case <-ctx.Done():
			return count, ctx.Err()
		default:
		}
	}

	L_info("memorygraph: backfill completed", "updated", count)
	return count, rows.Err()
}

// Stats returns statistics about the memory graph
func (m *Manager) Stats() (Stats, error) {
	var stats Stats

	err := m.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE forgotten = 0`).Scan(&stats.TotalMemories)
	if err != nil {
		return stats, err
	}

	err = m.db.QueryRow(`SELECT COUNT(*) FROM associations`).Scan(&stats.TotalAssociations)
	if err != nil {
		return stats, err
	}

	err = m.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE embedding IS NOT NULL AND forgotten = 0`).Scan(&stats.WithEmbeddings)
	if err != nil {
		return stats, err
	}

	// Count by type
	rows, err := m.db.Query(`SELECT memory_type, COUNT(*) FROM memories WHERE forgotten = 0 GROUP BY memory_type`)
	if err != nil {
		return stats, err
	}
	defer rows.Close()

	stats.ByType = make(map[Type]int)
	for rows.Next() {
		var memType Type
		var count int
		if err := rows.Scan(&memType, &count); err == nil {
			stats.ByType[memType] = count
		}
	}

	return stats, rows.Err()
}

// Stats contains statistics about the memory graph
type Stats struct {
	TotalMemories     int          `json:"total_memories"`
	TotalAssociations int          `json:"total_associations"`
	WithEmbeddings    int          `json:"with_embeddings"`
	ByType            map[Type]int `json:"by_type"`
}
