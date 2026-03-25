package media

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creasty/defaults"
	"github.com/roelfdiedericks/goclaw/internal/bus"
)

func TestMediaConfigMigratesLegacyFields(t *testing.T) {
	var cfg MediaConfig
	if err := defaults.Set(&cfg); err != nil {
		t.Fatalf("defaults.Set: %v", err)
	}
	if err := json.Unmarshal([]byte(`{"ttl":1234,"maxSize":987654}`), &cfg); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if cfg.MaxSize != 987654 {
		t.Fatalf("expected maxSize from json maxSize, got %d", cfg.MaxSize)
	}
	for _, category := range ephemeralCategories {
		if got := cfg.CategoryPolicy(category).TTL; got != 1234 {
			t.Fatalf("expected legacy ttl for %s, got %d", category, got)
		}
	}
	if cfg.Cleanup.Interval != int(DefaultCleanupInterval/time.Second) {
		t.Fatalf("expected default cleanup interval, got %d", cfg.Cleanup.Interval)
	}
}

func TestNewMediaStoreCreatesKeeperDir(t *testing.T) {
	baseDir := t.TempDir()
	store, err := NewMediaStore(MediaConfig{Dir: baseDir})
	if err != nil {
		t.Fatalf("NewMediaStore: %v", err)
	}
	t.Cleanup(store.Close)

	if _, err := os.Stat(filepath.Join(baseDir, "keeper")); err != nil {
		t.Fatalf("keeper dir missing: %v", err)
	}
}

func TestUsageSnapshotClassifiesNestedPaths(t *testing.T) {
	baseDir := t.TempDir()
	store, err := NewMediaStore(MediaConfig{Dir: baseDir})
	if err != nil {
		t.Fatalf("NewMediaStore: %v", err)
	}
	t.Cleanup(store.Close)

	if _, _, err := store.Save([]byte("browser"), "browser/traces", ".json"); err != nil {
		t.Fatalf("save browser trace: %v", err)
	}
	if _, _, err := store.Save([]byte("video"), "generated/video", ".mp4"); err != nil {
		t.Fatalf("save generated video: %v", err)
	}

	snapshot, err := store.UsageSnapshot()
	if err != nil {
		t.Fatalf("UsageSnapshot: %v", err)
	}

	if snapshot.Categories["browser"].FileCount != 1 {
		t.Fatalf("expected browser file count 1, got %d", snapshot.Categories["browser"].FileCount)
	}
	if snapshot.Categories["generated"].FileCount != 1 {
		t.Fatalf("expected generated file count 1, got %d", snapshot.Categories["generated"].FileCount)
	}
}

func TestCleanNowRemovesExpiredAndOldestQuotaFiles(t *testing.T) {
	baseDir := t.TempDir()
	cfg := MediaConfig{
		Dir: baseDir,
		Cleanup: MediaCleanupConfig{
			Enabled: false,
		},
		Categories: MediaCategoriesConfig{
			Browser: MediaCategoryConfig{
				TTL:   3600,
				Quota: 20,
			},
		},
	}
	cfg.Normalize()
	store, err := NewMediaStore(cfg)
	if err != nil {
		t.Fatalf("NewMediaStore: %v", err)
	}
	t.Cleanup(store.Close)

	expiredPath := writeMediaFile(t, baseDir, "browser", "expired.txt", strings.Repeat("a", 10), time.Now().Add(-2*time.Hour))
	oldPath := writeMediaFile(t, baseDir, "browser", "old.txt", strings.Repeat("b", 12), time.Now().Add(-30*time.Minute))
	keepPath := writeMediaFile(t, baseDir, "browser", "keep.txt", strings.Repeat("c", 12), time.Now().Add(-10*time.Minute))

	result, err := store.CleanNow()
	if err != nil {
		t.Fatalf("CleanNow: %v", err)
	}

	if result.ExpiredRemoved != 1 {
		t.Fatalf("expected 1 expired removal, got %d", result.ExpiredRemoved)
	}
	if result.QuotaRemoved != 1 {
		t.Fatalf("expected 1 quota removal, got %d", result.QuotaRemoved)
	}
	if fileExists(expiredPath) {
		t.Fatalf("expired file should be removed")
	}
	if fileExists(oldPath) {
		t.Fatalf("oldest quota file should be removed")
	}
	if !fileExists(keepPath) {
		t.Fatalf("newest file should remain")
	}
}

func TestCleanNowPreservesPermanentDirectories(t *testing.T) {
	baseDir := t.TempDir()
	cfg := MediaConfig{
		Dir: baseDir,
		Cleanup: MediaCleanupConfig{
			Enabled: false,
		},
		Quotas: MediaQuotasConfig{
			Global:  100,
			Uploads: 10,
			Keeper:  10,
		},
	}
	cfg.Normalize()
	store, err := NewMediaStore(cfg)
	if err != nil {
		t.Fatalf("NewMediaStore: %v", err)
	}
	t.Cleanup(store.Close)

	uploadPath := writeMediaFile(t, baseDir, "uploads/telegram/test/image", "upload.txt", strings.Repeat("u", 20), time.Now().Add(-2*time.Hour))
	keeperPath := writeMediaFile(t, baseDir, "keeper/generated", "keep.txt", strings.Repeat("k", 20), time.Now().Add(-2*time.Hour))

	result, err := store.CleanNow()
	if err != nil {
		t.Fatalf("CleanNow: %v", err)
	}

	if !fileExists(uploadPath) {
		t.Fatalf("uploads file should remain")
	}
	if !fileExists(keeperPath) {
		t.Fatalf("keeper file should remain")
	}
	if len(result.Warnings) == 0 {
		t.Fatalf("expected warnings for permanent directories over quota")
	}
}

func TestMediaBusActionsReturnUsageAndCleanupResults(t *testing.T) {
	baseDir := t.TempDir()
	cfg := MediaConfig{
		Dir: baseDir,
		Cleanup: MediaCleanupConfig{
			Enabled: false,
		},
		Categories: MediaCategoriesConfig{
			Browser: MediaCategoryConfig{
				TTL:   1,
				Quota: 100,
			},
		},
	}
	cfg.Normalize()
	store, err := NewMediaStore(cfg)
	if err != nil {
		t.Fatalf("NewMediaStore: %v", err)
	}
	t.Cleanup(store.Close)

	writeMediaFile(t, baseDir, "browser", "expired.txt", "abc", time.Now().Add(-10*time.Minute))

	stats := handleStats(bus.Command{})
	if !stats.Success {
		t.Fatalf("expected stats success, got error %v", stats.Error)
	}
	if _, ok := stats.Data.(MediaUsageSnapshot); !ok {
		t.Fatalf("expected stats data to be MediaUsageSnapshot, got %T", stats.Data)
	}

	clean := handleClean(bus.Command{})
	if !clean.Success {
		t.Fatalf("expected clean success, got error %v", clean.Error)
	}
	if _, ok := clean.Data.(MediaMaintenanceResult); !ok {
		t.Fatalf("expected clean data to be MediaMaintenanceResult, got %T", clean.Data)
	}
}

func writeMediaFile(t *testing.T, baseDir, subdir, name, content string, modTime time.Time) string {
	t.Helper()
	dir := filepath.Join(baseDir, filepath.FromSlash(subdir))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
	return path
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
