package media

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrStoreNotInitialized = errors.New("media store not initialized")
	ErrUnknownCategory     = errors.New("unknown media category")
)

// MediaStoreInfo exposes normalized store-level metadata for agents and UIs.
type MediaStoreInfo struct {
	Initialized            bool   `json:"initialized"`
	BaseDir                string `json:"baseDir"`
	MaxSizeBytes           int    `json:"maxSizeBytes"`
	MaxSizeDisplay         string `json:"maxSizeDisplay"`
	CleanupEnabled         bool   `json:"cleanupEnabled"`
	CleanupIntervalSeconds int    `json:"cleanupIntervalSeconds"`
	GlobalQuotaBytes       int64  `json:"globalQuotaBytes"`
	GlobalQuotaDisplay     string `json:"globalQuotaDisplay"`
	TotalUsedBytes         int64  `json:"totalUsedBytes"`
	TotalUsedDisplay       string `json:"totalUsedDisplay"`
	TotalFileCount         int    `json:"totalFileCount"`
}

// MediaCategoryInfo exposes per-category usage and policy details.
type MediaCategoryInfo struct {
	Name             string `json:"name"`
	Label            string `json:"label"`
	UsedBytes        int64  `json:"usedBytes"`
	UsedDisplay      string `json:"usedDisplay"`
	QuotaBytes       int64  `json:"quotaBytes"`
	QuotaDisplay     string `json:"quotaDisplay"`
	FileCount        int    `json:"fileCount"`
	Permanent        bool   `json:"permanent"`
	TTLSeconds       int    `json:"ttlSeconds"`
	OverQuotaBytes   int64  `json:"overQuotaBytes,omitempty"`
	OverQuotaDisplay string `json:"overQuotaDisplay"`
	Status           string `json:"status"`
	PolicyNote       string `json:"policyNote"`
}

// MediaInfoSnapshot combines live store metadata, usage, and warnings.
type MediaInfoSnapshot struct {
	Store      MediaStoreInfo               `json:"store"`
	Categories map[string]MediaCategoryInfo `json:"categories"`
	Warnings   []string                     `json:"warnings,omitempty"`
}

// KnownCategories returns the top-level media categories recognized by the store.
func KnownCategories() []string {
	out := make([]string, len(allCategories))
	copy(out, allCategories)
	return out
}

// CurrentStoreInfo returns a normalized live media info snapshot for the active store.
func CurrentStoreInfo(category string, includeWarnings bool) (MediaInfoSnapshot, error) {
	store := CurrentStore()
	if store == nil {
		return MediaInfoSnapshot{}, ErrStoreNotInitialized
	}
	return BuildStoreInfo(store, category, includeWarnings)
}

// BuildStoreInfo returns a normalized live media info snapshot for a specific store.
func BuildStoreInfo(store *MediaStore, category string, includeWarnings bool) (MediaInfoSnapshot, error) {
	if store == nil {
		return MediaInfoSnapshot{}, ErrStoreNotInitialized
	}
	focusCategory, err := normalizeInfoCategory(category)
	if err != nil {
		return MediaInfoSnapshot{}, err
	}

	cfg := store.Config()
	snapshot, err := store.UsageSnapshot()
	if err != nil {
		return MediaInfoSnapshot{}, err
	}

	info := MediaInfoSnapshot{
		Store: MediaStoreInfo{
			Initialized:            true,
			BaseDir:                store.BaseDir(),
			MaxSizeBytes:           cfg.MaxSize,
			MaxSizeDisplay:         formatMBBytes(int64(cfg.MaxSize)),
			CleanupEnabled:         cfg.Cleanup.Enabled,
			CleanupIntervalSeconds: cfg.Cleanup.Interval,
			GlobalQuotaBytes:       int64(cfg.Quotas.Global),
			GlobalQuotaDisplay:     formatGB(int64(cfg.Quotas.Global)),
			TotalUsedBytes:         snapshot.TotalBytes,
			TotalUsedDisplay:       formatGB(snapshot.TotalBytes),
			TotalFileCount:         totalFileCount(snapshot),
		},
		Categories: make(map[string]MediaCategoryInfo),
	}
	if includeWarnings {
		info.Warnings = append(info.Warnings, snapshot.Warnings...)
	}

	for _, name := range allCategories {
		if focusCategory != "" && name != focusCategory {
			continue
		}
		usage := snapshot.Categories[name]
		ttl := usage.TTLSeconds
		if usage.Permanent {
			ttl = 0
		}
		status := "ok"
		if usage.OverQuotaBytes > 0 {
			status = "over_quota"
		}
		info.Categories[name] = MediaCategoryInfo{
			Name:             name,
			Label:            categoryLabel(name),
			UsedBytes:        usage.UsedBytes,
			UsedDisplay:      formatGB(usage.UsedBytes),
			QuotaBytes:       usage.QuotaBytes,
			QuotaDisplay:     formatGB(usage.QuotaBytes),
			FileCount:        usage.FileCount,
			Permanent:        usage.Permanent,
			TTLSeconds:       ttl,
			OverQuotaBytes:   usage.OverQuotaBytes,
			OverQuotaDisplay: formatGB(usage.OverQuotaBytes),
			Status:           status,
			PolicyNote:       categoryPolicyNote(usage),
		}
	}
	return info, nil
}

// FormatInfoSummary returns a concise natural-language summary for the snapshot.
func FormatInfoSummary(info MediaInfoSnapshot, focusCategory string) string {
	if focusCategory != "" {
		category, ok := info.Categories[focusCategory]
		if !ok {
			return "Requested media category was not found."
		}
		summary := fmt.Sprintf("%s uses %s of %s across %d file(s).", category.Label, category.UsedDisplay, category.QuotaDisplay, category.FileCount)
		if category.Permanent {
			if category.OverQuotaBytes > 0 {
				return summary + " Files here are preserved and never auto-deleted, but the directory is over quota."
			}
			return summary + " Files here are preserved and never auto-deleted."
		}
		if category.OverQuotaBytes > 0 {
			return summary + fmt.Sprintf(" It keeps files for %d seconds and is over quota by %s.", category.TTLSeconds, category.OverQuotaDisplay)
		}
		return summary + fmt.Sprintf(" Files older than %d seconds or beyond quota may be cleaned.", category.TTLSeconds)
	}

	summary := fmt.Sprintf("Using %s of %s total media storage.", info.Store.TotalUsedDisplay, info.Store.GlobalQuotaDisplay)
	if _, ok := info.Categories["uploads"]; ok {
		if _, ok := info.Categories["keeper"]; ok {
			summary += " uploads and keeper are preserved."
		}
	}
	if len(info.Warnings) > 0 {
		summary += " " + info.Warnings[0]
	}
	return summary
}

func normalizeInfoCategory(category string) (string, error) {
	category = strings.TrimSpace(category)
	if category == "" {
		return "", nil
	}
	for _, candidate := range allCategories {
		if category == candidate {
			return category, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrUnknownCategory, category)
}

func totalFileCount(snapshot MediaUsageSnapshot) int {
	total := 0
	for _, usage := range snapshot.Categories {
		total += usage.FileCount
	}
	return total
}

func categoryPolicyNote(usage MediaCategoryUsage) string {
	if usage.Permanent {
		return "Files here are never auto-deleted."
	}
	return "Files older than TTL or beyond quota may be cleaned."
}

func formatMBBytes(n int64) string {
	if n <= 0 {
		return "0 MB"
	}
	if n%bytesPerMB == 0 {
		return fmt.Sprintf("%d MB", n/bytesPerMB)
	}
	value := float64(n) / float64(bytesPerMB)
	text := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", value), "0"), ".")
	return text + " MB"
}
