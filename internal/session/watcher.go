package session

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// SessionWatcher monitors an OpenClaw session file for changes and reads new records
type SessionWatcher struct {
	filePath     string
	session      *Session
	watcher      *fsnotify.Watcher
	lastOffset   int64          // Track where we left off reading
	lastModTime  time.Time      // Track file modification time
	onNewRecords func([]Record) // Callback for new records
	mu           sync.Mutex
	stopCh       chan struct{}
	running      bool
}

// NewSessionWatcher creates a watcher for an OpenClaw session file
func NewSessionWatcher(filePath string, session *Session, onNewRecords func([]Record)) (*SessionWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	sw := &SessionWatcher{
		filePath:     filePath,
		session:      session,
		watcher:      watcher,
		onNewRecords: onNewRecords,
		stopCh:       make(chan struct{}),
	}

	// Get initial file size/offset
	if info, err := os.Stat(filePath); err == nil {
		sw.lastOffset = info.Size()
		sw.lastModTime = info.ModTime()
		L_debug("watcher: initialized",
			"file", filepath.Base(filePath),
			"size", sw.lastOffset,
			"modified", sw.lastModTime.Format(time.RFC3339))
	}

	return sw, nil
}

// Start begins watching the session file for changes
func (sw *SessionWatcher) Start(ctx context.Context) error {
	sw.mu.Lock()
	if sw.running {
		sw.mu.Unlock()
		return nil
	}
	sw.running = true
	sw.mu.Unlock()

	// Watch the directory (fsnotify can't always watch files directly on all platforms)
	dir := filepath.Dir(sw.filePath)
	if err := sw.watcher.Add(dir); err != nil {
		return err
	}

	L_info("watcher: started monitoring OpenClaw session",
		"file", filepath.Base(sw.filePath),
		"dir", dir)

	go sw.watchLoop(ctx)
	return nil
}

// Stop stops watching the session file
func (sw *SessionWatcher) Stop() {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	if !sw.running {
		return
	}

	close(sw.stopCh)
	sw.watcher.Close()
	sw.running = false
	L_debug("watcher: stopped")
}

// watchLoop is the main event loop for file watching
func (sw *SessionWatcher) watchLoop(ctx context.Context) {
	targetFile := filepath.Base(sw.filePath)

	for {
		select {
		case <-ctx.Done():
			L_debug("watcher: context cancelled")
			return
		case <-sw.stopCh:
			return
		case event, ok := <-sw.watcher.Events:
			if !ok {
				return
			}

			// Only care about writes to our target file
			if filepath.Base(event.Name) != targetFile {
				continue
			}

			if event.Op&fsnotify.Write == fsnotify.Write {
				L_trace("watcher: file modified", "file", targetFile)
				sw.readNewRecords()
			}

		case err, ok := <-sw.watcher.Errors:
			if !ok {
				return
			}
			L_warn("watcher: error", "error", err)
		}
	}
}

// readNewRecords reads any new records appended to the file since last read
func (sw *SessionWatcher) readNewRecords() {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	file, err := os.Open(sw.filePath)
	if err != nil {
		L_warn("watcher: failed to open file", "error", err)
		return
	}
	defer file.Close()

	// Get current file size
	info, err := file.Stat()
	if err != nil {
		L_warn("watcher: failed to stat file", "error", err)
		return
	}

	currentSize := info.Size()
	if currentSize <= sw.lastOffset {
		// File hasn't grown (or was truncated)
		if currentSize < sw.lastOffset {
			L_warn("watcher: file appears truncated, resetting offset",
				"lastOffset", sw.lastOffset,
				"currentSize", currentSize)
			sw.lastOffset = 0
		}
		return
	}

	// Seek to where we left off
	if _, err := file.Seek(sw.lastOffset, io.SeekStart); err != nil {
		L_warn("watcher: failed to seek", "error", err)
		return
	}

	// Read new lines
	var newRecords []Record
	scanner := bufio.NewScanner(file)
	const maxLineSize = 10 * 1024 * 1024 // 10MB
	buf := make([]byte, maxLineSize)
	scanner.Buffer(buf, maxLineSize)

	lineCount := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		record, err := ParseRecord(line)
		if err != nil {
			L_warn("watcher: failed to parse record", "error", err)
			continue
		}
		newRecords = append(newRecords, record)
		lineCount++
	}

	if err := scanner.Err(); err != nil {
		L_warn("watcher: scanner error", "error", err)
	}

	// Update offset
	sw.lastOffset = currentSize
	sw.lastModTime = info.ModTime()

	if len(newRecords) > 0 {
		L_info("watcher: read new records from OpenClaw session",
			"count", len(newRecords),
			"newSize", currentSize,
			"file", filepath.Base(sw.filePath))

		// Process new records
		sw.processNewRecords(newRecords)

		// Call callback if set
		if sw.onNewRecords != nil {
			sw.onNewRecords(newRecords)
		}
	}
}

// processNewRecords updates the session with new records
func (sw *SessionWatcher) processNewRecords(records []Record) {
	if sw.session == nil {
		return
	}

	sw.session.mu.Lock()
	defer sw.session.mu.Unlock()

	for _, r := range records {
		switch rec := r.(type) {
		case *MessageRecord:
			msg := convertMessageRecord(rec)
			if msg != nil {
				// Check if we already have this message (by ID)
				found := false
				for _, existing := range sw.session.Messages {
					if existing.ID == msg.ID {
						found = true
						break
					}
				}
				if !found {
					sw.session.Messages = append(sw.session.Messages, *msg)
					L_trace("watcher: added message",
						"id", msg.ID,
						"role", msg.Role)
				}
			}

		case *CompactionRecord:
			// OpenClaw did a compaction - we should respect it
			L_info("watcher: OpenClaw performed compaction",
				"summary", truncateString(rec.Summary, 100),
				"tokensBefore", rec.TokensBefore)
			sw.session.CompactionCount++
			// Could potentially update our context based on their compaction

		case *CheckpointRecord:
			// OpenClaw added a checkpoint - update our reference
			sw.session.LastCheckpoint = rec
			L_debug("watcher: OpenClaw added checkpoint",
				"tokens", rec.Checkpoint.TokensAtCheckpoint,
				"messages", rec.Checkpoint.MessageCountAtCheckpoint)
		}
	}

	sw.session.UpdatedAt = time.Now()
}

// truncateString truncates a string to maxLen with ellipsis
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// ForceSync forces an immediate read of any new records
func (sw *SessionWatcher) ForceSync() {
	sw.readNewRecords()
}

// LastModified returns the last modification time of the watched file
func (sw *SessionWatcher) LastModified() time.Time {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	return sw.lastModTime
}
