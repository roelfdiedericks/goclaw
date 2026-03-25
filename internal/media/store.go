package media

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
)

// MediaStore manages temporary media file storage with automatic TTL-based cleanup.
// It stores files in a configurable directory with subdirectories for different sources
// (browser screenshots, inbound media, etc.).
type MediaStore struct {
	baseDir string
	cfg     MediaConfig
	stopCh  chan struct{}
	wg      sync.WaitGroup
	mu      sync.Mutex
}

// NewMediaStore creates a new MediaStore with the given configuration.
// It expands ~ to the user's home directory and creates the base directory if needed.
func NewMediaStore(cfg MediaConfig) (*MediaStore, error) {
	cfg.Normalize()

	dir := cfg.Dir
	if dir == "" {
		dir = DefaultMediaDir
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
	if err := os.MkdirAll(filepath.Join(dir, "keeper"), 0700); err != nil {
		return nil, fmt.Errorf("failed to create keeper directory: %w", err)
	}

	store := &MediaStore{
		baseDir: dir,
		cfg:     cfg,
		stopCh:  make(chan struct{}),
	}
	setActiveStore(store)

	logging.L_info("media: store initialized",
		"dir", dir,
		"maxSize", cfg.MaxSize,
		"cleanupEnabled", cfg.Cleanup.Enabled,
		"cleanupIntervalSeconds", cfg.Cleanup.Interval,
		"globalQuota", cfg.Quotas.Global,
		"uploadsQuota", cfg.Quotas.Uploads,
		"keeperQuota", cfg.Quotas.Keeper,
	)

	return store, nil
}

// Start begins the background cleanup goroutine.
// Call this after creating the MediaStore to enable automatic cleanup.
func (s *MediaStore) Start() {
	if !s.cfg.Cleanup.Enabled {
		logging.L_info("media: cleanup disabled", "dir", s.baseDir)
		return
	}
	cleanupInterval := time.Duration(s.cfg.Cleanup.Interval) * time.Second

	logging.L_debug("media: starting cleanup goroutine", "interval", cleanupInterval.String())

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()

		if _, err := s.CleanNow(); err != nil {
			logging.L_warn("media: initial cleanup error", "error", err)
		}

		for {
			select {
			case <-ticker.C:
				if _, err := s.CleanNow(); err != nil {
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
	if CurrentStore() == s {
		setActiveStore(nil)
	}
	logging.L_debug("media: store closed")
}

// Save stores data to a file in the given subdirectory with the given extension.
// Returns the absolute path and a relative path suitable for MEDIA: output.
// The relative path format ./media/{subdir}/{filename} matches OpenClaw's security requirements.
func (s *MediaStore) Save(data []byte, subdir, ext string) (absPath string, relPath string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := uuid.New().String()[:8]
	filename := id + ext
	return s.saveWithFilenameLocked(data, subdir, filename)
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
	OriginalName  string     // Original uploaded filename (if available)
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
		"originalName", ctx.OriginalName,
	)

	// Preserve original upload filename stem for traceability while still avoiding collisions.
	base := strings.TrimSpace(filepath.Base(ctx.OriginalName))
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	safeStem := sanitizeFilename(stem)
	if safeStem == "unknown" {
		safeStem = "upload"
	}
	filename := fmt.Sprintf("%s_%s%s", safeStem, uuid.New().String()[:8], ext)
	return s.saveWithFilename(data, subdir, filename)
}

func (s *MediaStore) saveWithFilename(data []byte, subdir, filename string) (absPath, relPath string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.saveWithFilenameLocked(data, subdir, filename)
}

func (s *MediaStore) saveWithFilenameLocked(data []byte, subdir, filename string) (absPath, relPath string, err error) {
	if int64(len(data)) > int64(s.cfg.MaxSize) {
		return "", "", fmt.Errorf("file size %d exceeds limit %d", len(data), s.cfg.MaxSize)
	}

	// Create subdirectory
	dir := filepath.Join(s.baseDir, subdir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", "", fmt.Errorf("failed to create subdirectory: %w", err)
	}

	absPath = filepath.Join(dir, filename)
	if err := os.WriteFile(absPath, data, 0600); err != nil {
		return "", "", fmt.Errorf("failed to write file: %w", err)
	}

	relPath = fmt.Sprintf("./media/%s/%s", subdir, filename)
	logging.L_debug("media: saved file",
		"absPath", absPath,
		"relPath", relPath,
		"size", len(data),
		"category", topLevelCategory(subdir),
		"filename", filename,
	)

	if warning, warnErr := s.buildPostSaveWarningLocked(topLevelCategory(subdir)); warnErr != nil {
		logging.L_warn("media: post-save usage check failed", "error", warnErr)
	} else if warning != "" {
		logging.L_warn("media: storage pressure after save", "warning", warning, "category", topLevelCategory(subdir))
	}
	return absPath, relPath, nil
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

// Config returns the normalized store configuration in use by the current store.
func (s *MediaStore) Config() MediaConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.cfg
	cfg.Normalize()
	return cfg
}

// UsageSnapshot returns current usage across the top-level media categories.
func (s *MediaStore) UsageSnapshot() (MediaUsageSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usageSnapshotLocked()
}

// CleanNow runs a full media maintenance pass immediately.
func (s *MediaStore) CleanNow() (MediaMaintenanceResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cleanNowLocked()
}

func (s *MediaStore) cleanNowLocked() (MediaMaintenanceResult, error) {
	files, _, err := s.scanMediaLocked()
	if err != nil {
		return MediaMaintenanceResult{}, err
	}
	removed := map[string]bool{}
	result := MediaMaintenanceResult{
		CategorySummaries: make(map[string]string),
	}

	for _, category := range ephemeralCategories {
		policy := s.cfg.CategoryPolicy(category)
		if policy.TTL <= 0 && policy.Quota <= 0 {
			continue
		}
		categoryFiles := sortedRemainingFiles(files, removed, category)
		cutoff := time.Now().Add(-time.Duration(policy.TTL) * time.Second)
		var remainingBytes int64
		for _, file := range categoryFiles {
			remainingBytes += file.Size
			if policy.TTL > 0 && file.ModTime.Before(cutoff) {
				if s.removeFile(file.Path) {
					removed[file.Path] = true
					result.RemovedFiles++
					result.ExpiredRemoved++
					result.RemovedBytes += file.Size
					remainingBytes -= file.Size
				}
			}
		}
		if policy.Quota > 0 {
			for _, file := range sortedRemainingFiles(files, removed, category) {
				if remainingBytes <= int64(policy.Quota) {
					break
				}
				if s.removeFile(file.Path) {
					removed[file.Path] = true
					result.RemovedFiles++
					result.QuotaRemoved++
					result.RemovedBytes += file.Size
					remainingBytes -= file.Size
				}
			}
		}
	}

	snapshot, err := s.usageSnapshotLocked()
	if err != nil {
		return MediaMaintenanceResult{}, err
	}
	if snapshot.OverGlobalBytes > 0 {
		ephemeralFiles := make([]mediaFileInfo, 0)
		for _, file := range files {
			if removed[file.Path] || isPermanentCategory(file.Category) {
				continue
			}
			ephemeralFiles = append(ephemeralFiles, file)
		}
		sort.Slice(ephemeralFiles, func(i, j int) bool {
			return ephemeralFiles[i].ModTime.Before(ephemeralFiles[j].ModTime)
		})
		for _, file := range ephemeralFiles {
			if snapshot.OverGlobalBytes <= 0 {
				break
			}
			if s.removeFile(file.Path) {
				removed[file.Path] = true
				result.RemovedFiles++
				result.QuotaRemoved++
				result.RemovedBytes += file.Size
				snapshot.OverGlobalBytes -= file.Size
			}
		}
	}

	finalSnapshot, err := s.usageSnapshotLocked()
	if err != nil {
		return MediaMaintenanceResult{}, err
	}
	result.Snapshot = finalSnapshot
	result.Warnings = usageWarningSummary(finalSnapshot)
	if result.RemovedFiles == 0 {
		result.Message = "Media maintenance finished. No files were removed."
	} else {
		result.Message = fmt.Sprintf("Media maintenance finished. Removed %d files and freed %s.", result.RemovedFiles, formatGB(result.RemovedBytes))
	}
	logging.L_info("media: maintenance completed",
		"removedFiles", result.RemovedFiles,
		"removedBytes", result.RemovedBytes,
		"expiredRemoved", result.ExpiredRemoved,
		"quotaRemoved", result.QuotaRemoved,
		"warnings", len(result.Warnings),
	)
	return result, nil
}

func (s *MediaStore) usageSnapshotLocked() (MediaUsageSnapshot, error) {
	_, snapshot, err := s.scanMediaLocked()
	if err != nil {
		return MediaUsageSnapshot{}, err
	}
	return snapshot, nil
}

func (s *MediaStore) scanMediaLocked() ([]mediaFileInfo, MediaUsageSnapshot, error) {
	s.cfg.Normalize()
	files := make([]mediaFileInfo, 0)
	snapshot := MediaUsageSnapshot{
		BaseDir:          s.baseDir,
		GeneratedAt:      time.Now(),
		GlobalQuotaBytes: int64(s.cfg.Quotas.Global),
		Categories:       make(map[string]MediaCategoryUsage, len(allCategories)),
	}
	for _, category := range allCategories {
		usage := MediaCategoryUsage{
			Category:   category,
			Permanent:  isPermanentCategory(category),
			QuotaBytes: s.cfg.QuotaForCategory(category),
		}
		if !usage.Permanent {
			usage.TTLSeconds = s.cfg.CategoryPolicy(category).TTL
		}
		snapshot.Categories[category] = usage
	}

	err := filepath.Walk(s.baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(s.baseDir, path)
		if err != nil {
			return nil
		}
		category := topLevelCategory(rel)
		files = append(files, mediaFileInfo{
			Path:     path,
			Category: category,
			Size:     info.Size(),
			ModTime:  info.ModTime(),
		})
		usage := snapshot.Categories[category]
		usage.FileCount++
		usage.UsedBytes += info.Size()
		if usage.QuotaBytes > 0 && usage.UsedBytes > usage.QuotaBytes {
			usage.OverQuotaBytes = usage.UsedBytes - usage.QuotaBytes
		}
		snapshot.Categories[category] = usage
		snapshot.TotalBytes += info.Size()
		return nil
	})
	if err != nil {
		return nil, MediaUsageSnapshot{}, err
	}
	if snapshot.GlobalQuotaBytes > 0 && snapshot.TotalBytes > snapshot.GlobalQuotaBytes {
		snapshot.OverGlobalBytes = snapshot.TotalBytes - snapshot.GlobalQuotaBytes
	}
	snapshot.Warnings = usageWarningSummary(snapshot)
	return files, snapshot, nil
}

func sortedRemainingFiles(files []mediaFileInfo, removed map[string]bool, category string) []mediaFileInfo {
	filtered := make([]mediaFileInfo, 0)
	for _, file := range files {
		if removed[file.Path] || file.Category != category {
			continue
		}
		filtered = append(filtered, file)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].ModTime.Before(filtered[j].ModTime)
	})
	return filtered
}

func (s *MediaStore) removeFile(path string) bool {
	if err := os.Remove(path); err != nil {
		logging.L_warn("media: failed to remove file", "path", path, "error", err)
		return false
	}
	logging.L_trace("media: removed file", "path", path)
	return true
}

func (s *MediaStore) buildPostSaveWarningLocked(category string) (string, error) {
	snapshot, err := s.usageSnapshotLocked()
	if err != nil {
		return "", err
	}
	warnings := make([]string, 0, 2)
	if usage, ok := snapshot.Categories[category]; ok && usage.OverQuotaBytes > 0 {
		warnings = append(warnings, fmt.Sprintf("%s is over quota by %s", categoryLabel(category), formatGB(usage.OverQuotaBytes)))
	}
	if snapshot.OverGlobalBytes > 0 {
		warnings = append(warnings, fmt.Sprintf("global media storage is over quota by %s", formatGB(snapshot.OverGlobalBytes)))
	}
	if len(warnings) == 0 {
		return "", nil
	}
	return fmt.Sprintf("Saved successfully, but %s. Run Clean Now or increase the relevant quota.", strings.Join(warnings, " and ")), nil
}
