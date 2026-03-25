package media

import (
	"strings"
	"testing"
	"time"
)

func TestBuildStoreInfoIncludesUsageAndWarnings(t *testing.T) {
	baseDir := t.TempDir()
	cfg := MediaConfig{
		Dir: baseDir,
		Cleanup: MediaCleanupConfig{
			Enabled: true,
		},
		Quotas: MediaQuotasConfig{
			Global:  100,
			Uploads: 50,
			Keeper:  50,
		},
		Categories: MediaCategoriesConfig{
			Browser: MediaCategoryConfig{
				TTL:   3600,
				Quota: 10,
			},
		},
	}
	cfg.Normalize()
	store, err := NewMediaStore(cfg)
	if err != nil {
		t.Fatalf("NewMediaStore: %v", err)
	}
	t.Cleanup(store.Close)

	writeMediaFile(t, baseDir, "browser", "trace.json", strings.Repeat("b", 20), time.Now())

	info, err := BuildStoreInfo(store, "", true)
	if err != nil {
		t.Fatalf("BuildStoreInfo: %v", err)
	}
	if !info.Store.Initialized {
		t.Fatal("expected initialized store info")
	}
	if info.Store.BaseDir != baseDir {
		t.Fatalf("expected base dir %q, got %q", baseDir, info.Store.BaseDir)
	}
	if len(info.Categories) != len(allCategories) {
		t.Fatalf("expected %d categories, got %d", len(allCategories), len(info.Categories))
	}
	if info.Categories["browser"].Status != "over_quota" {
		t.Fatalf("expected browser over_quota, got %q", info.Categories["browser"].Status)
	}
	if len(info.Warnings) == 0 {
		t.Fatal("expected warnings when browser is over quota")
	}
	summary := FormatInfoSummary(info, "")
	if !strings.Contains(summary, "uploads and keeper are preserved") {
		t.Fatalf("expected preserve summary, got %q", summary)
	}
}

func TestBuildStoreInfoCategoryFocusReturnsSingleCategory(t *testing.T) {
	baseDir := t.TempDir()
	store, err := NewMediaStore(MediaConfig{Dir: baseDir})
	if err != nil {
		t.Fatalf("NewMediaStore: %v", err)
	}
	t.Cleanup(store.Close)

	writeMediaFile(t, baseDir, "keeper/docs", "keep.txt", "important", time.Now())

	info, err := BuildStoreInfo(store, "keeper", false)
	if err != nil {
		t.Fatalf("BuildStoreInfo: %v", err)
	}
	if len(info.Categories) != 1 {
		t.Fatalf("expected single focused category, got %d", len(info.Categories))
	}
	keeper, ok := info.Categories["keeper"]
	if !ok {
		t.Fatal("expected keeper category")
	}
	if !keeper.Permanent {
		t.Fatal("expected keeper to be permanent")
	}
	if keeper.TTLSeconds != 0 {
		t.Fatalf("expected keeper TTL 0, got %d", keeper.TTLSeconds)
	}
	if len(info.Warnings) != 0 {
		t.Fatalf("expected warnings omitted, got %#v", info.Warnings)
	}
	summary := FormatInfoSummary(info, "keeper")
	if !strings.Contains(summary, "never auto-deleted") {
		t.Fatalf("expected keeper summary to mention permanence, got %q", summary)
	}
}
