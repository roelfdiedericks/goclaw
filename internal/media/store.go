// Package media provides image processing and storage utilities for GoClaw.
// store.go implements MediaStore for saving media files with TTL-based cleanup.
package media

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

const (
	// DefaultMediaDir is the fallback media storage directory.
	// Note: The gateway resolves media.dir to <workspace>/media/ when not explicitly set.
	// This constant is only used when MediaStore is created directly without gateway.
	DefaultMediaDir = "~/.goclaw/media"

	// DefaultTTL is the default time-to-live for media files (10 minutes)
	DefaultTTL = 10 * time.Minute

	// MaxMediaBytes is the maximum allowed file size (5MB)
	MaxMediaBytes = 5 * 1024 * 1024

	// CleanupInterval is how often to run cleanup (half of TTL)
	CleanupIntervalDivisor = 2
)

// MediaStore manages temporary media file storage with automatic TTL-based cleanup.
// It stores files in a configurable directory with subdirectories for different sources
// (browser screenshots, inbound media, etc.).
type MediaStore struct {
	baseDir string        // Resolved absolute path to media directory
	ttl     time.Duration // Time-to-live for files
	maxSize int64         // Maximum file size in bytes
	stopCh  chan struct{} // Channel to stop cleanup goroutine
	wg      sync.WaitGroup
	mu      sync.Mutex // Protects concurrent saves
}

// MediaConfig configures the MediaStore
type MediaConfig struct {
	Dir     string `json:"dir"`     // Base directory (gateway defaults to <workspace>/media/)
	TTL     int    `json:"ttl"`     // TTL in seconds (default: 600 = 10 min)
	MaxSize int    `json:"maxSize"` // Max file size in bytes (default: 5MB)
}

// NewMediaStore creates a new MediaStore with the given configuration.
// It expands ~ to the user's home directory and creates the base directory if needed.
func NewMediaStore(cfg MediaConfig) (*MediaStore, error) {
	// Apply defaults
	dir := cfg.Dir
	if dir == "" {
		dir = DefaultMediaDir
	}

	ttl := time.Duration(cfg.TTL) * time.Second
	if ttl == 0 {
		ttl = DefaultTTL
	}

	maxSize := int64(cfg.MaxSize)
	if maxSize == 0 {
		maxSize = MaxMediaBytes
	}

	// Expand ~ to home directory
	if strings.HasPrefix(dir, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		dir = filepath.Join(home, dir[1:])
	}

	// Clean the path
	dir = filepath.Clean(dir)

	// Create base directory with restricted permissions
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create media directory: %w", err)
	}

	store := &MediaStore{
		baseDir: dir,
		ttl:     ttl,
		maxSize: maxSize,
		stopCh:  make(chan struct{}),
	}

	logging.L_info("media: store initialized",
		"dir", dir,
		"ttl", ttl.String(),
		"maxSize", maxSize,
	)

	return store, nil
}

// Start begins the background cleanup goroutine.
// Call this after creating the MediaStore to enable automatic cleanup.
func (s *MediaStore) Start() {
	cleanupInterval := s.ttl / CleanupIntervalDivisor
	if cleanupInterval < time.Minute {
		cleanupInterval = time.Minute
	}

	logging.L_debug("media: starting cleanup goroutine", "interval", cleanupInterval.String())

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()

		// Run initial cleanup
		if err := s.cleanOld(); err != nil {
			logging.L_warn("media: initial cleanup error", "error", err)
		}

		for {
			select {
			case <-ticker.C:
				if err := s.cleanOld(); err != nil {
					logging.L_warn("media: cleanup error", "error", err)
				}
			case <-s.stopCh:
				logging.L_debug("media: cleanup goroutine stopped")
				return
			}
		}
	}()
}

// Close stops the cleanup goroutine and waits for it to finish.
func (s *MediaStore) Close() {
	close(s.stopCh)
	s.wg.Wait()
	logging.L_debug("media: store closed")
}

// Save stores data to a file in the given subdirectory with the given extension.
// Returns the absolute path and a relative path suitable for MEDIA: output.
// The relative path format ./media/{subdir}/{filename} matches OpenClaw's security requirements.
func (s *MediaStore) Save(data []byte, subdir, ext string) (absPath string, relPath string, err error) {
	// Check size limit
	if int64(len(data)) > s.maxSize {
		return "", "", fmt.Errorf("file size %d exceeds limit %d", len(data), s.maxSize)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Create subdirectory
	dir := filepath.Join(s.baseDir, subdir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", "", fmt.Errorf("failed to create subdirectory: %w", err)
	}

	// Generate unique filename
	id := uuid.New().String()[:8]
	filename := id + ext
	absPath = filepath.Join(dir, filename)

	// Write file with restricted permissions
	if err := os.WriteFile(absPath, data, 0600); err != nil {
		return "", "", fmt.Errorf("failed to write file: %w", err)
	}

	// Generate relative path for MEDIA: output
	// Format: ./media/{subdir}/{filename}
	relPath = fmt.Sprintf("./media/%s/%s", subdir, filename)

	logging.L_debug("media: saved file",
		"absPath", absPath,
		"relPath", relPath,
		"size", len(data),
		"subdir", subdir,
	)

	return absPath, relPath, nil
}

// UploadContext provides context for user-uploaded media.
// This allows the media store to organize uploads by channel/user/mediatype
// and enables future features like database tracking.
type UploadContext struct {
	Channel       string     // Source channel: "telegram", "discord", "http"
	User          *user.User // User from registry (nil for anonymous)
	ChannelUserID string     // Channel-specific user ID (e.g., Telegram numeric ID)
	ChatID        string     // Session/chat identifier
	MediaType     string     // Media type: "image", "voice", "document", etc.
	Caption       string     // Optional caption for metadata
}

// sanitizeFilename removes unsafe characters from a string for use in filenames
var unsafeFilenameChars = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func sanitizeFilename(s string) string {
	if s == "" {
		return "unknown"
	}
	safe := unsafeFilenameChars.ReplaceAllString(s, "_")
	if safe == "" || safe == "_" {
		return "unknown"
	}
	if len(safe) > 32 {
		safe = safe[:32]
	}
	return safe
}

// SaveUpload stores user-uploaded media with rich context.
// Directory structure: uploads/{channel}/{username}/{mediatype}/{filename}
// Files in uploads/ are excluded from TTL cleanup (permanent storage).
// Returns absPath, relPath (for MEDIA: references), and error.
func (s *MediaStore) SaveUpload(data []byte, ext string, ctx UploadContext) (absPath, relPath string, err error) {
	// Determine username for path
	username := "anonymous"
	if ctx.User != nil && ctx.User.Name != "" {
		username = sanitizeFilename(ctx.User.Name)
	} else if ctx.ChannelUserID != "" {
		username = sanitizeFilename(ctx.ChannelUserID)
	}

	// Determine channel and media type
	channel := ctx.Channel
	if channel == "" {
		channel = "unknown"
	}
	mediaType := ctx.MediaType
	if mediaType == "" {
		mediaType = "other"
	}

	// Build subdir: uploads/telegram/roelf/image
	subdir := filepath.Join("uploads", channel, username, mediaType)

	logging.L_debug("media: saving user upload",
		"channel", ctx.Channel,
		"user", username,
		"mediaType", mediaType,
		"chatID", ctx.ChatID,
	)

	return s.Save(data, subdir, ext)
}

// SaveFile copies a file from srcPath to the media store.
// Returns the absolute path and a relative path suitable for MEDIA: output.
func (s *MediaStore) SaveFile(srcPath, subdir string) (absPath string, relPath string, err error) {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to read source file: %w", err)
	}

	ext := filepath.Ext(srcPath)
	return s.Save(data, subdir, ext)
}

// RelativePath converts an absolute path to a relative path for MEDIA: output.
// Returns empty string if the path is not within the media store.
func (s *MediaStore) RelativePath(absolutePath string) string {
	rel, err := filepath.Rel(s.baseDir, absolutePath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return "./media/" + rel
}

// AbsolutePath converts a relative MEDIA: path to an absolute path.
// The input should be in format ./media/{subdir}/{filename}
func (s *MediaStore) AbsolutePath(relativePath string) string {
	// Strip ./media/ prefix
	if !strings.HasPrefix(relativePath, "./media/") {
		return ""
	}
	subpath := strings.TrimPrefix(relativePath, "./media/")
	return filepath.Join(s.baseDir, subpath)
}

// BaseDir returns the base directory of the media store.
func (s *MediaStore) BaseDir() string {
	return s.baseDir
}

// cleanOld removes files older than TTL from the media directory.
// It walks all subdirectories and removes expired files.
// The uploads/ directory is excluded from cleanup (permanent storage).
func (s *MediaStore) cleanOld() error {
	now := time.Now()
	cutoff := now.Add(-s.ttl)
	removedCount := 0
	uploadsDir := filepath.Join(s.baseDir, "uploads")

	err := filepath.Walk(s.baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip files with errors
		}

		// Skip the uploads directory entirely (permanent storage)
		if info.IsDir() && path == uploadsDir {
			return filepath.SkipDir
		}

		// Skip other directories
		if info.IsDir() {
			return nil
		}

		// Check if file is older than TTL
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(path); err != nil {
				logging.L_trace("media: failed to remove expired file", "path", path, "error", err)
			} else {
				removedCount++
				logging.L_trace("media: removed expired file", "path", path, "age", now.Sub(info.ModTime()).String())
			}
		}

		return nil
	})

	if removedCount > 0 {
		logging.L_debug("media: cleanup completed", "removed", removedCount)
	}

	return err
}
