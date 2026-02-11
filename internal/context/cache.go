// Package context handles workspace context file loading and system prompt building.
package context

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// PromptCache caches workspace files and invalidates when they change.
// It uses fsnotify for immediate invalidation and optional hash polling as fallback.
// The cache stores workspace files (SOUL.md, IDENTITY.md, etc.) to avoid disk I/O on each request.
type PromptCache struct {
	mu            sync.RWMutex
	cachedFiles   []WorkspaceFile
	valid         bool
	contentHash   string
	fileHashes    map[string]string // Per-file hashes for change detection
	watcher       *fsnotify.Watcher
	workspaceDir  string
	pollInterval  time.Duration
	stopCh        chan struct{}
	watchedFiles  []string
}

// NewPromptCache creates a new prompt cache with file watching.
// pollInterval of 0 disables hash polling (fsnotify only).
func NewPromptCache(workspaceDir string, pollIntervalSec int) (*PromptCache, error) {
	pc := &PromptCache{
		workspaceDir: workspaceDir,
		pollInterval: time.Duration(pollIntervalSec) * time.Second,
		stopCh:       make(chan struct{}),
		valid:        false,
	}

	// Define files to watch (identity files that affect system prompt)
	pc.watchedFiles = []string{
		FileAgents,
		FileSoul,
		FileTools,
		FileIdentity,
		FileUser,
		FileHeartbeat,
		FileBootstrap,
		FileMemory,
	}

	// Set up fsnotify watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		L_warn("promptcache: failed to create watcher, caching disabled", "error", err)
		return pc, nil // Return cache without watcher - will rebuild every time
	}
	pc.watcher = watcher

	// Start watching files
	watchCount := 0
	for _, name := range pc.watchedFiles {
		path := filepath.Join(workspaceDir, name)
		if _, err := os.Stat(path); err == nil {
			if err := watcher.Add(path); err != nil {
				L_warn("promptcache: failed to watch file", "file", name, "error", err)
			} else {
				watchCount++
			}
		}
	}

	// Also watch the workspace directory for new file creation
	if err := watcher.Add(workspaceDir); err != nil {
		L_warn("promptcache: failed to watch workspace dir", "error", err)
	}

	// Watch memory/ directory if it exists
	memoryDir := filepath.Join(workspaceDir, "memory")
	if _, err := os.Stat(memoryDir); err == nil {
		if err := watcher.Add(memoryDir); err != nil {
			L_warn("promptcache: failed to watch memory dir", "error", err)
		}
	}

	L_info("promptcache: initialized", "workspaceDir", workspaceDir, "pollInterval", pollIntervalSec)
	L_debug("promptcache: watching files", "count", watchCount)

	// Start event handler goroutine
	go pc.watchLoop()

	// Start hash poller if interval > 0
	if pc.pollInterval > 0 {
		go pc.hashPoller()
	}

	return pc, nil
}

// watchLoop handles fsnotify events
func (pc *PromptCache) watchLoop() {
	if pc.watcher == nil {
		return
	}

	for {
		select {
		case event, ok := <-pc.watcher.Events:
			if !ok {
				return
			}
			// Only invalidate on write/create/remove operations
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
				// Check if it's one of our watched files
				fileName := filepath.Base(event.Name)
				for _, watched := range pc.watchedFiles {
					if fileName == watched || filepath.Dir(event.Name) == filepath.Join(pc.workspaceDir, "memory") {
						L_debug("promptcache: file changed, invalidating cache",
							"file", event.Name,
							"op", event.Op.String())
						pc.Invalidate()
						break
					}
				}
			}
		case err, ok := <-pc.watcher.Errors:
			if !ok {
				return
			}
			L_warn("promptcache: fsnotify error", "error", err)
		case <-pc.stopCh:
			return
		}
	}
}

// hashPoller periodically checks file hashes as a fallback
func (pc *PromptCache) hashPoller() {
	ticker := time.NewTicker(pc.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if changedFiles := pc.getChangedFiles(); len(changedFiles) > 0 {
				L_debug("promptcache: files changed, invalidating cache", "files", changedFiles)
				pc.Invalidate()
			} else {
				L_trace("promptcache: hash poll check", "changed", false)
			}
		case <-pc.stopCh:
			return
		}
	}
}

// getChangedFiles returns a list of files that have changed since last check
// Updates stored hashes so we don't detect the same changes again
func (pc *PromptCache) getChangedFiles() []string {
	newHashes := pc.computeFileHashes()
	
	pc.mu.Lock()
	defer pc.mu.Unlock()
	
	// First run - no previous hashes to compare
	if pc.fileHashes == nil {
		pc.fileHashes = newHashes
		return nil
	}
	
	var changed []string
	for name, newHash := range newHashes {
		if oldHash, exists := pc.fileHashes[name]; !exists || oldHash != newHash {
			changed = append(changed, name)
		}
	}
	
	// Check for deleted files
	for name := range pc.fileHashes {
		if _, exists := newHashes[name]; !exists {
			changed = append(changed, name+" (deleted)")
		}
	}
	
	// Update stored hashes
	if len(changed) > 0 {
		pc.fileHashes = newHashes
	}
	
	return changed
}

// computeFileHashes computes hash for each watched file
func (pc *PromptCache) computeFileHashes() map[string]string {
	hashes := make(map[string]string)
	
	for _, name := range pc.watchedFiles {
		path := filepath.Join(pc.workspaceDir, name)
		if content, err := os.ReadFile(path); err == nil {
			h := sha256.Sum256(content)
			hashes[name] = hex.EncodeToString(h[:8]) // Short hash for logging
		}
	}
	
	return hashes
}

// hashChanged checks if any watched file content has changed
// If changed, updates the stored hash so we don't keep detecting the same change
func (pc *PromptCache) hashChanged() bool {
	newHash := pc.computeHash()

	pc.mu.Lock()
	oldHash := pc.contentHash
	changed := oldHash != "" && newHash != oldHash
	if changed {
		// Update stored hash so we don't detect this change again
		pc.contentHash = newHash
	}
	pc.mu.Unlock()

	return changed
}

// computeHash computes a combined hash of all watched files
func (pc *PromptCache) computeHash() string {
	h := sha256.New()

	for _, name := range pc.watchedFiles {
		path := filepath.Join(pc.workspaceDir, name)
		if content, err := os.ReadFile(path); err == nil {
			h.Write([]byte(name))
			h.Write(content)
		}
	}

	// Also include memory/ directory files
	memoryDir := filepath.Join(pc.workspaceDir, "memory")
	if entries, err := os.ReadDir(memoryDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && filepath.Ext(entry.Name()) == ".md" {
				path := filepath.Join(memoryDir, entry.Name())
				if content, err := os.ReadFile(path); err == nil {
					h.Write([]byte(entry.Name()))
					h.Write(content)
				}
			}
		}
	}

	return hex.EncodeToString(h.Sum(nil))
}

// Invalidate marks the cache as invalid
func (pc *PromptCache) Invalidate() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.valid = false
}

// GetWorkspaceFiles returns cached workspace files or loads them fresh if cache is invalid.
func (pc *PromptCache) GetWorkspaceFiles() []WorkspaceFile {
	pc.mu.RLock()
	if pc.valid && pc.cachedFiles != nil {
		files := pc.cachedFiles
		pc.mu.RUnlock()
		L_trace("promptcache: cache hit")
		return files
	}
	pc.mu.RUnlock()

	// Need to reload
	pc.mu.Lock()
	defer pc.mu.Unlock()

	// Double-check after acquiring write lock
	if pc.valid && pc.cachedFiles != nil {
		L_trace("promptcache: cache hit (after lock)")
		return pc.cachedFiles
	}

	L_debug("promptcache: cache miss, loading workspace files")

	// Load workspace files from disk (always include memory - filtering happens at prompt build time)
	files := LoadWorkspaceFiles(pc.workspaceDir, true)

	// Update cache
	pc.cachedFiles = files
	pc.valid = true
	pc.contentHash = pc.computeHash()

	L_debug("promptcache: workspace files loaded", "count", len(files))

	return files
}

// GetOrBuild returns a cached value or builds it using the builder function.
// This is a generic method for caching any computed value that depends on workspace files.
func (pc *PromptCache) GetOrBuild(builder func() string) string {
	// For backward compatibility, this method can be used to cache computed strings
	// But the primary use case is GetWorkspaceFiles for caching file content
	pc.mu.RLock()
	valid := pc.valid
	pc.mu.RUnlock()

	if !valid {
		// Ensure files are loaded (which validates the cache)
		pc.GetWorkspaceFiles()
	}

	return builder()
}

// Close stops the watcher and hash poller
func (pc *PromptCache) Close() {
	close(pc.stopCh)
	if pc.watcher != nil {
		pc.watcher.Close()
	}
	L_debug("promptcache: closed")
}

// IsValid returns whether the cache currently holds a valid prompt
func (pc *PromptCache) IsValid() bool {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.valid
}
