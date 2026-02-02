package skills

import (
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/fsnotify/fsnotify"
)

// Watcher monitors skill directories for changes.
type Watcher struct {
	watcher      *fsnotify.Watcher
	dirs         []string
	debounceMs   int
	onChange     func()
	stopCh       chan struct{}
	mu           sync.Mutex
	lastTrigger  time.Time
	pendingTimer *time.Timer
}

// NewWatcher creates a new skill directory watcher.
func NewWatcher(debounceMs int, onChange func()) (*Watcher, error) {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	if debounceMs <= 0 {
		debounceMs = 500 // Default 500ms debounce
	}

	return &Watcher{
		watcher:    fsWatcher,
		debounceMs: debounceMs,
		onChange:   onChange,
		stopCh:     make(chan struct{}),
	}, nil
}

// WatchDirs adds directories to watch.
func (w *Watcher) WatchDirs(dirs []string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	for _, dir := range dirs {
		if dir == "" {
			continue
		}

		// Watch the directory itself
		if err := w.watcher.Add(dir); err != nil {
			log.Warn("failed to watch skill directory", "path", dir, "error", err)
			continue
		}

		// Also watch subdirectories (skill folders)
		entries, err := filepath.Glob(filepath.Join(dir, "*/"))
		if err == nil {
			for _, entry := range entries {
				if err := w.watcher.Add(entry); err != nil {
					log.Debug("failed to watch skill subdirectory", "path", entry, "error", err)
				}
			}
		}

		w.dirs = append(w.dirs, dir)
		log.Debug("watching skill directory", "path", dir)
	}

	return nil
}

// Start begins watching for file changes.
// This spawns a goroutine internally.
func (w *Watcher) Start() {
	go w.run()
}

// run is the main event loop.
func (w *Watcher) run() {
	for {
		select {
		case <-w.stopCh:
			return

		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			w.handleEvent(event)

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			log.Warn("skill watcher error", "error", err)
		}
	}
}

// handleEvent processes a file system event.
func (w *Watcher) handleEvent(event fsnotify.Event) {
	// Only care about SKILL.md files
	if !strings.HasSuffix(event.Name, "SKILL.md") {
		// If a new directory was created, start watching it
		if event.Op&fsnotify.Create != 0 {
			w.maybeWatchNewDir(event.Name)
		}
		return
	}

	// Check if it's a relevant operation
	isRelevant := event.Op&fsnotify.Write != 0 ||
		event.Op&fsnotify.Create != 0 ||
		event.Op&fsnotify.Remove != 0

	if !isRelevant {
		return
	}

	log.Debug("skill file changed",
		"path", event.Name,
		"op", event.Op.String())

	w.triggerReload()
}

// maybeWatchNewDir watches a new skill directory if it's a directory.
func (w *Watcher) maybeWatchNewDir(path string) {
	// Check if this is in one of our watched directories
	for _, dir := range w.dirs {
		if strings.HasPrefix(path, dir) {
			// It's a potential new skill directory
			if err := w.watcher.Add(path); err == nil {
				log.Debug("watching new skill directory", "path", path)
			}
			return
		}
	}
}

// triggerReload schedules a reload with debouncing.
func (w *Watcher) triggerReload() {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Cancel any pending timer
	if w.pendingTimer != nil {
		w.pendingTimer.Stop()
	}

	// Schedule new reload
	w.pendingTimer = time.AfterFunc(time.Duration(w.debounceMs)*time.Millisecond, func() {
		w.mu.Lock()
		w.lastTrigger = time.Now()
		w.pendingTimer = nil
		w.mu.Unlock()

		log.Info("skills changed, reloading")
		if w.onChange != nil {
			w.onChange()
		}
	})
}

// Stop stops watching for changes.
func (w *Watcher) Stop() error {
	close(w.stopCh)

	w.mu.Lock()
	if w.pendingTimer != nil {
		w.pendingTimer.Stop()
	}
	w.mu.Unlock()

	return w.watcher.Close()
}

// LastTrigger returns the time of the last reload trigger.
func (w *Watcher) LastTrigger() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastTrigger
}
