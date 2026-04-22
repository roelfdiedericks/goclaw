package media

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	bytesPerMB = 1024 * 1024
	bytesPerGB = 1024 * 1024 * 1024

	DefaultMaxSizeBytes        = 100 * 1024 * 1024
	DefaultCleanupInterval     = 5 * time.Minute
	DefaultGlobalQuotaBytes    = 50 * bytesPerGB
	DefaultUploadsQuotaBytes   = bytesPerGB / 2
	DefaultKeeperQuotaBytes    = bytesPerGB / 2
	DefaultBrowserTTL          = 24 * time.Hour
	DefaultBrowserQuotaBytes   = bytesPerGB / 2
	DefaultCameraTTL           = 1 * time.Hour
	DefaultCameraQuotaBytes    = bytesPerGB / 2
	DefaultGeneratedTTL        = 7 * 24 * time.Hour
	DefaultGeneratedQuotaBytes = bytesPerGB
	DefaultDownloadsTTL        = 24 * time.Hour
	DefaultDownloadsQuotaBytes = bytesPerGB / 2
	DefaultVoiceTTL            = 1 * time.Hour
	DefaultVoiceQuotaBytes     = bytesPerGB / 2
	DefaultExtractedTTL        = 30 * 24 * time.Hour
	DefaultExtractedQuotaBytes = 2 * bytesPerGB
)

var (
	ephemeralCategories = []string{"browser", "camera", "generated", "downloads", "voice", "extracted"}
	allCategories       = []string{"uploads", "keeper", "browser", "camera", "generated", "downloads", "voice", "extracted"}
)

type MediaCleanupConfig struct {
	Enabled  bool `json:"enabled" default:"true"`
	Interval int  `json:"interval" default:"300"`
}

type MediaQuotasConfig struct {
	Global  int `json:"global" default:"53687091200"`
	Uploads int `json:"uploads" default:"536870912"`
	Keeper  int `json:"keeper" default:"536870912"`
}

type MediaCategoryConfig struct {
	TTL   int `json:"ttl"`
	Quota int `json:"quota"`
}

type MediaCategoriesConfig struct {
	Browser   MediaCategoryConfig `json:"browser"`
	Camera    MediaCategoryConfig `json:"camera"`
	Generated MediaCategoryConfig `json:"generated"`
	Downloads MediaCategoryConfig `json:"downloads"`
	Voice     MediaCategoryConfig `json:"voice"`
	Extracted MediaCategoryConfig `json:"extracted"`
}

// MediaConfig configures the MediaStore.
type MediaConfig struct {
	Dir        string                `json:"dir"`
	MaxSize    int                   `json:"maxSize" default:"104857600"`
	Cleanup    MediaCleanupConfig    `json:"cleanup"`
	Quotas     MediaQuotasConfig     `json:"quotas"`
	Categories MediaCategoriesConfig `json:"categories"`

	TTL int `json:"ttl,omitempty"`

	categoriesProvided bool `json:"-"`
}

func (c *MediaConfig) UnmarshalJSON(data []byte) error {
	type alias MediaConfig
	aux := alias(*c)
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	cfg := MediaConfig(aux)
	_, cfg.categoriesProvided = raw["categories"]
	cfg.Normalize()
	*c = cfg
	return nil
}

func (c *MediaConfig) Normalize() {
	if c.MaxSize <= 0 {
		c.MaxSize = DefaultMaxSizeBytes
	}
	if c.Cleanup.Interval <= 0 {
		c.Cleanup.Interval = int(DefaultCleanupInterval / time.Second)
	}
	if c.Quotas.Global <= 0 {
		c.Quotas.Global = DefaultGlobalQuotaBytes
	}
	if c.Quotas.Uploads <= 0 {
		c.Quotas.Uploads = DefaultUploadsQuotaBytes
	}
	if c.Quotas.Keeper <= 0 {
		c.Quotas.Keeper = DefaultKeeperQuotaBytes
	}

	legacyTTL := 0
	if !c.categoriesProvided && c.TTL > 0 {
		legacyTTL = c.TTL
	}
	normalizeCategoryConfig(&c.Categories.Browser, defaultCategoryConfig("browser"), legacyTTL)
	normalizeCategoryConfig(&c.Categories.Camera, defaultCategoryConfig("camera"), legacyTTL)
	normalizeCategoryConfig(&c.Categories.Generated, defaultCategoryConfig("generated"), legacyTTL)
	normalizeCategoryConfig(&c.Categories.Downloads, defaultCategoryConfig("downloads"), legacyTTL)
	normalizeCategoryConfig(&c.Categories.Voice, defaultCategoryConfig("voice"), legacyTTL)
	normalizeCategoryConfig(&c.Categories.Extracted, defaultCategoryConfig("extracted"), legacyTTL)
}

func normalizeCategoryConfig(cfg *MediaCategoryConfig, defaults MediaCategoryConfig, legacyTTL int) {
	if legacyTTL > 0 && cfg.TTL <= 0 {
		cfg.TTL = legacyTTL
	}
	if cfg.TTL <= 0 {
		cfg.TTL = defaults.TTL
	}
	if cfg.Quota <= 0 {
		cfg.Quota = defaults.Quota
	}
}

func defaultCategoryConfig(category string) MediaCategoryConfig {
	switch category {
	case "browser":
		return MediaCategoryConfig{TTL: int(DefaultBrowserTTL / time.Second), Quota: DefaultBrowserQuotaBytes}
	case "camera":
		return MediaCategoryConfig{TTL: int(DefaultCameraTTL / time.Second), Quota: DefaultCameraQuotaBytes}
	case "generated":
		return MediaCategoryConfig{TTL: int(DefaultGeneratedTTL / time.Second), Quota: DefaultGeneratedQuotaBytes}
	case "downloads":
		return MediaCategoryConfig{TTL: int(DefaultDownloadsTTL / time.Second), Quota: DefaultDownloadsQuotaBytes}
	case "voice":
		return MediaCategoryConfig{TTL: int(DefaultVoiceTTL / time.Second), Quota: DefaultVoiceQuotaBytes}
	case "extracted":
		return MediaCategoryConfig{TTL: int(DefaultExtractedTTL / time.Second), Quota: DefaultExtractedQuotaBytes}
	default:
		return MediaCategoryConfig{}
	}
}

func (c MediaConfig) CategoryPolicy(category string) MediaCategoryConfig {
	c.Normalize()
	switch category {
	case "browser":
		return c.Categories.Browser
	case "camera":
		return c.Categories.Camera
	case "generated":
		return c.Categories.Generated
	case "downloads":
		return c.Categories.Downloads
	case "voice":
		return c.Categories.Voice
	case "extracted":
		return c.Categories.Extracted
	default:
		return MediaCategoryConfig{}
	}
}

func (c MediaConfig) QuotaForCategory(category string) int64 {
	c.Normalize()
	switch category {
	case "uploads":
		return int64(c.Quotas.Uploads)
	case "keeper":
		return int64(c.Quotas.Keeper)
	case "browser", "camera", "generated", "downloads", "voice", "extracted":
		return int64(c.CategoryPolicy(category).Quota)
	default:
		return int64(c.CategoryPolicy("downloads").Quota)
	}
}

func topLevelCategory(path string) string {
	cleaned := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	cleaned = strings.TrimPrefix(cleaned, "./")
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "" || cleaned == "." {
		return "downloads"
	}
	root := cleaned
	if idx := strings.IndexByte(cleaned, '/'); idx >= 0 {
		root = cleaned[:idx]
	}
	switch root {
	case "uploads", "keeper", "browser", "camera", "generated", "downloads", "voice", "extracted":
		return root
	default:
		return "downloads"
	}
}

func isPermanentCategory(category string) bool {
	return category == "uploads" || category == "keeper"
}

type MediaCategoryUsage struct {
	Category       string `json:"category"`
	Permanent      bool   `json:"permanent"`
	FileCount      int    `json:"fileCount"`
	UsedBytes      int64  `json:"usedBytes"`
	QuotaBytes     int64  `json:"quotaBytes"`
	TTLSeconds     int    `json:"ttlSeconds,omitempty"`
	OverQuotaBytes int64  `json:"overQuotaBytes,omitempty"`
}

type MediaUsageSnapshot struct {
	BaseDir          string                        `json:"baseDir"`
	GeneratedAt      time.Time                     `json:"generatedAt"`
	TotalBytes       int64                         `json:"totalBytes"`
	GlobalQuotaBytes int64                         `json:"globalQuotaBytes"`
	OverGlobalBytes  int64                         `json:"overGlobalBytes,omitempty"`
	Categories       map[string]MediaCategoryUsage `json:"categories"`
	Warnings         []string                      `json:"warnings,omitempty"`
}

type MediaMaintenanceResult struct {
	RemovedFiles      int                `json:"removedFiles"`
	RemovedBytes      int64              `json:"removedBytes"`
	ExpiredRemoved    int                `json:"expiredRemoved"`
	QuotaRemoved      int                `json:"quotaRemoved"`
	Message           string             `json:"message"`
	Warnings          []string           `json:"warnings,omitempty"`
	Snapshot          MediaUsageSnapshot `json:"snapshot"`
	CategorySummaries map[string]string  `json:"categorySummaries,omitempty"`
}

type mediaFileInfo struct {
	Path     string
	Category string
	Size     int64
	ModTime  time.Time
}

func formatGB(n int64) string {
	value := float64(n) / float64(bytesPerGB)
	text := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", value), "0"), ".")
	return text + " GB"
}

func usageWarningSummary(snapshot MediaUsageSnapshot) []string {
	var warnings []string
	if snapshot.OverGlobalBytes > 0 {
		warnings = append(warnings, fmt.Sprintf("Global media storage is over quota by %s.", formatGB(snapshot.OverGlobalBytes)))
	}
	for _, category := range allCategories {
		usage, ok := snapshot.Categories[category]
		if !ok || usage.OverQuotaBytes <= 0 {
			continue
		}
		if usage.Permanent {
			warnings = append(warnings, fmt.Sprintf("%s is over quota by %s. Consider moving or deleting older files, or increase that quota.", categoryLabel(category), formatGB(usage.OverQuotaBytes)))
			continue
		}
		warnings = append(warnings, fmt.Sprintf("%s is over quota by %s. Run Clean Now or increase that quota.", categoryLabel(category), formatGB(usage.OverQuotaBytes)))
	}
	return warnings
}

func categoryLabel(category string) string {
	if category == "" {
		return "Unknown"
	}
	return strings.ToUpper(category[:1]) + category[1:]
}

var activeStoreState struct {
	mu    sync.RWMutex
	store *MediaStore
}

func setActiveStore(store *MediaStore) {
	activeStoreState.mu.Lock()
	defer activeStoreState.mu.Unlock()
	activeStoreState.store = store
}

// CurrentStore returns the process-wide media store singleton, if initialized.
func CurrentStore() *MediaStore {
	activeStoreState.mu.RLock()
	defer activeStoreState.mu.RUnlock()
	return activeStoreState.store
}
