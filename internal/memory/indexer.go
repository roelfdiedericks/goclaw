package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

const (
	// debounceDelay is how long to wait after file changes before syncing
	debounceDelay = 1500 * time.Millisecond

	// snippetMaxChars is the maximum characters for search result snippets
	snippetMaxChars = 700
)

// Indexer manages the background indexing of memory files
type Indexer struct {
	db           *sql.DB
	provider     EmbeddingProvider
	workspaceDir string
	extraPaths   []string

	watcher      *fsnotify.Watcher
	dirty        atomic.Bool
	syncing      atomic.Bool
	forceReindex atomic.Bool // When true, re-index all files even if unchanged
	stopChan     chan struct{}
	syncChan     chan struct{}
	wg           sync.WaitGroup
	mu           sync.RWMutex

	// Stats
	lastSync     time.Time
	filesIndexed int
	chunksTotal  int
}

// NewIndexer creates a new memory indexer
func NewIndexer(db *sql.DB, provider EmbeddingProvider, workspaceDir string, extraPaths []string) *Indexer {
	return &Indexer{
		db:           db,
		provider:     provider,
		workspaceDir: workspaceDir,
		extraPaths:   extraPaths,
		stopChan:     make(chan struct{}),
		syncChan:     make(chan struct{}, 1),
	}
}

// SetProvider updates the embedding provider (e.g., when a better one becomes available)
func (idx *Indexer) SetProvider(provider EmbeddingProvider) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.provider = provider
}

// Start begins the background indexer goroutine
func (idx *Indexer) Start() error {
	L_info("memory: starting indexer", "workspace", idx.workspaceDir)

	// Create file watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	idx.watcher = watcher

	// Watch memory directories
	memoryDir := filepath.Join(idx.workspaceDir, "memory")
	if err := idx.watchDir(memoryDir); err != nil {
		L_debug("memory: memory dir not found, will create on first write", "path", memoryDir)
	}

	// Watch MEMORY.md
	memoryFile := filepath.Join(idx.workspaceDir, "MEMORY.md")
	if _, err := os.Stat(memoryFile); err == nil {
		if err := watcher.Add(memoryFile); err != nil {
			L_warn("memory: failed to watch MEMORY.md", "error", err)
		} else {
			L_debug("memory: watching MEMORY.md", "path", memoryFile)
		}
	}

	// Watch extra paths
	for _, path := range idx.extraPaths {
		absPath := path
		if !filepath.IsAbs(path) {
			absPath = filepath.Join(idx.workspaceDir, path)
		}
		if err := idx.watchDir(absPath); err != nil {
			L_warn("memory: failed to watch extra path", "path", absPath, "error", err)
		}
	}

	// Mark dirty for initial indexing
	idx.dirty.Store(true)

	// Start background goroutine
	idx.wg.Add(1)
	go idx.loop()

	return nil
}

// watchDir adds a directory to the watcher
func (idx *Indexer) watchDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", dir)
	}

	if err := idx.watcher.Add(dir); err != nil {
		return err
	}
	L_debug("memory: watching directory", "path", dir)
	return nil
}

// Stop stops the indexer gracefully
func (idx *Indexer) Stop() {
	L_info("memory: stopping indexer")
	close(idx.stopChan)
	if idx.watcher != nil {
		idx.watcher.Close()
	}
	idx.wg.Wait()
	L_debug("memory: indexer stopped")
}

// TriggerSync requests a sync (non-blocking)
func (idx *Indexer) TriggerSync() {
	select {
	case idx.syncChan <- struct{}{}:
		L_trace("memory: sync triggered")
	default:
		// Already a sync pending
	}
}

// MarkDirty marks all files as needing re-indexing (e.g., when embeddings become available)
func (idx *Indexer) MarkDirty() {
	idx.dirty.Store(true)
	idx.forceReindex.Store(true)
	L_debug("memory: marked all files for re-indexing")
}

// IsDirty returns true if there are pending changes
func (idx *Indexer) IsDirty() bool {
	return idx.dirty.Load()
}

// IsSyncing returns true if a sync is in progress
func (idx *Indexer) IsSyncing() bool {
	return idx.syncing.Load()
}

// loop is the main indexer goroutine
func (idx *Indexer) loop() {
	defer idx.wg.Done()

	debounceTimer := time.NewTimer(0)
	if !debounceTimer.Stop() {
		<-debounceTimer.C
	}

	// Initial sync after a short delay
	debounceTimer.Reset(500 * time.Millisecond)

	for {
		select {
		case <-idx.stopChan:
			L_debug("memory: indexer received stop signal")
			return

		case event, ok := <-idx.watcher.Events:
			if !ok {
				return
			}
			if idx.isMemoryFile(event.Name) {
				L_trace("memory: file changed", "path", event.Name, "op", event.Op.String())
				idx.dirty.Store(true)
				debounceTimer.Reset(debounceDelay)
			}

		case err, ok := <-idx.watcher.Errors:
			if !ok {
				return
			}
			L_warn("memory: watcher error", "error", err)

		case <-debounceTimer.C:
			if idx.dirty.Load() {
				idx.runSync()
			}

		case <-idx.syncChan:
			// Manual sync request
			if idx.dirty.Load() || idx.filesIndexed == 0 {
				idx.runSync()
			}
		}
	}
}

// isMemoryFile checks if a path is a memory file we care about
func (idx *Indexer) isMemoryFile(path string) bool {
	// Check if it's a markdown file
	if !strings.HasSuffix(strings.ToLower(path), ".md") {
		return false
	}

	// Check if it's in memory/ directory
	memoryDir := filepath.Join(idx.workspaceDir, "memory")
	if strings.HasPrefix(path, memoryDir) {
		return true
	}

	// Check if it's MEMORY.md
	memoryFile := filepath.Join(idx.workspaceDir, "MEMORY.md")
	if path == memoryFile {
		return true
	}

	// Check extra paths
	for _, extra := range idx.extraPaths {
		absExtra := extra
		if !filepath.IsAbs(extra) {
			absExtra = filepath.Join(idx.workspaceDir, extra)
		}
		if strings.HasPrefix(path, absExtra) {
			return true
		}
	}

	return false
}

// runSync performs the actual sync operation
func (idx *Indexer) runSync() {
	if idx.syncing.Load() {
		L_trace("memory: sync already in progress")
		return
	}
	idx.syncing.Store(true)
	defer idx.syncing.Store(false)

	startTime := time.Now()
	L_debug("memory: starting sync")

	ctx := context.Background()

	// List all memory files
	files, err := idx.listMemoryFiles()
	if err != nil {
		L_error("memory: failed to list memory files", "error", err)
		return
	}

	L_debug("memory: found memory files", "count", len(files))

	filesProcessed := 0
	chunksProcessed := 0

	for _, file := range files {
		changed, err := idx.indexFile(ctx, file)
		if err != nil {
			L_warn("memory: failed to index file", "path", file, "error", err)
			continue
		}
		if changed {
			filesProcessed++
			// Count chunks for this file
			var count int
			idx.db.QueryRow("SELECT COUNT(*) FROM memory_chunks WHERE path = ?", file).Scan(&count)
			chunksProcessed += count
		}
	}

	// Remove stale files
	idx.removeStaleFiles(files)

	idx.dirty.Store(false)
	idx.forceReindex.Store(false)
	idx.lastSync = time.Now()

	// Update stats including embedding counts
	var chunksWithEmbeddings int
	idx.mu.Lock()
	idx.filesIndexed = len(files)
	idx.db.QueryRow("SELECT COUNT(*) FROM memory_chunks").Scan(&idx.chunksTotal)
	idx.db.QueryRow("SELECT COUNT(*) FROM memory_chunks WHERE embedding IS NOT NULL AND embedding != ''").Scan(&chunksWithEmbeddings)
	idx.mu.Unlock()

	elapsed := time.Since(startTime)
	L_info("memory: sync completed",
		"filesProcessed", filesProcessed,
		"chunksProcessed", chunksProcessed,
		"totalFiles", len(files),
		"totalChunks", idx.chunksTotal,
		"withEmbeddings", chunksWithEmbeddings,
		"elapsed", elapsed.String(),
	)
}

// listMemoryFiles returns all markdown files to index
func (idx *Indexer) listMemoryFiles() ([]string, error) {
	var files []string

	// Check MEMORY.md
	memoryFile := filepath.Join(idx.workspaceDir, "MEMORY.md")
	if _, err := os.Stat(memoryFile); err == nil {
		files = append(files, memoryFile)
	}

	// List memory/ directory
	memoryDir := filepath.Join(idx.workspaceDir, "memory")
	if err := filepath.WalkDir(memoryDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(path), ".md") {
			files = append(files, path)
		}
		return nil
	}); err != nil && !os.IsNotExist(err) {
		L_trace("memory: error walking memory dir", "error", err)
	}

	// List extra paths
	for _, extra := range idx.extraPaths {
		absExtra := extra
		if !filepath.IsAbs(extra) {
			absExtra = filepath.Join(idx.workspaceDir, extra)
		}
		info, err := os.Stat(absExtra)
		if err != nil {
			continue
		}
		if info.IsDir() {
			filepath.WalkDir(absExtra, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if !d.IsDir() && strings.HasSuffix(strings.ToLower(path), ".md") {
					files = append(files, path)
				}
				return nil
			})
		} else if strings.HasSuffix(strings.ToLower(absExtra), ".md") {
			files = append(files, absExtra)
		}
	}

	return files, nil
}

// indexFile indexes a single file, returns true if file was updated
func (idx *Indexer) indexFile(ctx context.Context, path string) (bool, error) {
	// Read file
	content, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read file: %w", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return false, fmt.Errorf("stat file: %w", err)
	}

	// Compute hash
	hash := computeHash(content)

	// Make path relative to workspace for storage
	relPath := path
	if strings.HasPrefix(path, idx.workspaceDir) {
		relPath, _ = filepath.Rel(idx.workspaceDir, path)
	}

	// Check if file is unchanged
	var existingHash string
	err = idx.db.QueryRow("SELECT hash FROM memory_files WHERE path = ?", relPath).Scan(&existingHash)
	if err == nil && existingHash == hash {
		// File content unchanged - but check if we need to add embeddings
		if idx.forceReindex.Load() && idx.provider.Available() {
			// Check if chunks are missing embeddings
			var missingEmbeddings int
			idx.db.QueryRow(`
				SELECT COUNT(*) FROM memory_chunks 
				WHERE path = ? AND (embedding IS NULL OR embedding = '')
			`, relPath).Scan(&missingEmbeddings)
			if missingEmbeddings == 0 {
				L_trace("memory: file unchanged, embeddings present", "path", relPath)
				return false, nil
			}
			L_debug("memory: file unchanged but missing embeddings, re-indexing", "path", relPath, "missing", missingEmbeddings)
		} else {
			L_trace("memory: file unchanged", "path", relPath)
			return false, nil
		}
	}

	L_debug("memory: indexing file", "path", relPath, "size", len(content))

	// Chunk the content
	chunks := ChunkMarkdown(string(content), DefaultChunkOptions())

	// Generate embeddings if provider is available
	var embeddings [][]float32
	if idx.provider.Available() {
		texts := make([]string, len(chunks))
		for i, chunk := range chunks {
			texts[i] = chunk.Text
		}
		embeddings, err = idx.provider.EmbedBatch(ctx, texts)
		if err != nil {
			L_warn("memory: failed to generate embeddings", "path", path, "error", err)
			// Continue without embeddings
		}
	}

	// Begin transaction
	tx, err := idx.db.Begin()
	if err != nil {
		return false, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete existing chunks for this file
	if _, err := tx.Exec("DELETE FROM memory_chunks WHERE path = ?", relPath); err != nil {
		return false, fmt.Errorf("delete existing chunks: %w", err)
	}

	// Insert new chunks
	now := time.Now().UnixMilli()
	for i, chunk := range chunks {
		chunkID := fmt.Sprintf("%s:%d:%d", hash[:16], chunk.StartLine, chunk.EndLine)

		var embeddingBlob []byte
		var embeddingModel string
		if embeddings != nil && i < len(embeddings) && embeddings[i] != nil {
			embeddingBlob, _ = json.Marshal(embeddings[i])
			embeddingModel = idx.provider.Model()
		}

		_, err := tx.Exec(`
			INSERT INTO memory_chunks (id, path, start_line, end_line, hash, text, embedding, embedding_model, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, chunkID, relPath, chunk.StartLine, chunk.EndLine, chunk.Hash, chunk.Text, embeddingBlob, embeddingModel, now)
		if err != nil {
			return false, fmt.Errorf("insert chunk: %w", err)
		}
	}

	// Update file record
	_, err = tx.Exec(`
		INSERT INTO memory_files (path, hash, mtime_ms, size, indexed_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET hash=excluded.hash, mtime_ms=excluded.mtime_ms, size=excluded.size, indexed_at=excluded.indexed_at
	`, relPath, hash, info.ModTime().UnixMilli(), info.Size(), now)
	if err != nil {
		return false, fmt.Errorf("update file record: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}

	L_debug("memory: file indexed", "path", relPath, "chunks", len(chunks), "hasEmbeddings", embeddings != nil)
	return true, nil
}

// removeStaleFiles removes files from the index that no longer exist
func (idx *Indexer) removeStaleFiles(currentFiles []string) {
	// Build set of current relative paths
	currentSet := make(map[string]bool)
	for _, f := range currentFiles {
		relPath := f
		if strings.HasPrefix(f, idx.workspaceDir) {
			relPath, _ = filepath.Rel(idx.workspaceDir, f)
		}
		currentSet[relPath] = true
	}

	// Find stale files in database
	rows, err := idx.db.Query("SELECT path FROM memory_files")
	if err != nil {
		L_warn("memory: failed to query files for cleanup", "error", err)
		return
	}
	defer rows.Close()

	var stale []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			continue
		}
		if !currentSet[path] {
			stale = append(stale, path)
		}
	}
	if err := rows.Err(); err != nil {
		L_warn("memory: row iteration error during cleanup", "error", err)
		return
	}

	// Remove stale files
	for _, path := range stale {
		L_debug("memory: removing stale file from index", "path", path)
		idx.db.Exec("DELETE FROM memory_chunks WHERE path = ?", path)
		idx.db.Exec("DELETE FROM memory_files WHERE path = ?", path)
	}
}

// computeHash computes SHA256 hash of content
func computeHash(content []byte) string {
	h := sha256.Sum256(content)
	return hex.EncodeToString(h[:])
}

// Stats returns current indexer statistics
func (idx *Indexer) Stats() (files int, chunks int, lastSync time.Time) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.filesIndexed, idx.chunksTotal, idx.lastSync
}
