package media

import (
	"fmt"
	"strings"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

const configPath = "media"

// ConfigFormDef returns the form definition for this component's config.
func ConfigFormDef() forms.FormDef {
	return ConfigFormDefWithValues(MediaConfig{})
}

// ConfigFormDefWithValues returns the form definition with current config and usage context.
func ConfigFormDefWithValues(cfg MediaConfig) forms.FormDef {
	cfg.Normalize()
	snapshot, _ := currentUsageSnapshot()

	return forms.FormDef{
		Title:       "Media Storage",
		Description: mediaFormOverview(cfg, snapshot),
		Sections: []forms.Section{
			{
				Title: "Storage Limits And Location",
				Desc:  "Choose where media files live, the overall storage ceiling, and the largest file size GoClaw should accept.",
				Fields: []forms.Field{
					{Name: "dir", Title: "Storage Directory", Type: forms.Text, Desc: "Base directory for media files. Leave empty to use <workspace>/media/."},
					{Name: "quotas.global", Title: "Global Quota", Type: forms.Slider, Default: DefaultGlobalQuotaBytes, Desc: "Total media storage limit across all directories. Displayed in GB.", Scale: bytesPerGB, Unit: "GB", Min: 0.5, Max: 20, Step: 0.5},
					{Name: "maxSize", Title: "Max File Size", Type: forms.Number, Default: DefaultMaxSizeBytes, Desc: "Maximum size allowed for a single saved file. Displayed in MB. Default: 100MB.", Scale: bytesPerMB, Unit: "MB", Min: 25, Max: 1024, Step: 25},
				},
			},
			{
				Title:     "Cleanup Behavior",
				Desc:      "Background cleanup controls for temporary media directories.",
				FieldName: "cleanup",
				Nested: ptrFormDef(forms.FormDef{
					Sections: []forms.Section{
						{
							Fields: []forms.Field{
								{Name: "enabled", Title: "Enable Automatic Cleanup", Type: forms.Toggle, Desc: "Run background cleanup on a timer for temporary media."},
								{Name: "interval", Title: "Cleanup Interval (seconds)", Type: forms.Number, Default: int(DefaultCleanupInterval / time.Second), Desc: "How often to run background cleanup. Default: 300 seconds (5 minutes)."},
							},
						},
					},
				}),
			},
			directoryQuotaSection("Uploads Directory", "User-uploaded content from channels. Files here are never auto-deleted.", "uploads", cfg, snapshot),
			directoryQuotaSection("Keeper Directory", "Manually preserved files. Files here are never auto-deleted.", "keeper", cfg, snapshot),
			ephemeralCategorySection("Browser Directory", "Browser screenshots, PDFs, and traces.", "browser", cfg, snapshot),
			ephemeralCategorySection("Camera Directory", "Camera snapshots and security captures.", "camera", cfg, snapshot),
			ephemeralCategorySection("Generated Directory", "AI-generated images and videos.", "generated", cfg, snapshot),
			ephemeralCategorySection("Downloads Directory", "Downloaded files and general fallback storage.", "downloads", cfg, snapshot),
			ephemeralCategorySection("Voice Directory", "Text-to-speech audio output.", "voice", cfg, snapshot),
		},
		Actions: []forms.ActionDef{
			{Name: "stats", Label: "Refresh Usage", Desc: "Show current usage and warning information"},
			{Name: "clean", Label: "Clean Now", Desc: "Run a full media maintenance pass now"},
			{Name: "apply", Label: "Apply"},
		},
	}
}

func ptrFormDef(f forms.FormDef) *forms.FormDef {
	return &f
}

func currentUsageSnapshot() (*MediaUsageSnapshot, error) {
	store := CurrentStore()
	if store == nil {
		return nil, nil
	}
	snapshot, err := store.UsageSnapshot()
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func mediaFormOverview(cfg MediaConfig, snapshot *MediaUsageSnapshot) string {
	if snapshot == nil {
		return "Configure media retention, quotas, and cleanup policies for uploads, keeper, and temporary media directories."
	}
	parts := []string{
		fmt.Sprintf("Currently using %s of %s total media storage.", formatGB(snapshot.TotalBytes), formatGB(int64(cfg.Quotas.Global))),
	}
	if len(snapshot.Warnings) > 0 {
		parts = append(parts, strings.Join(snapshot.Warnings, " "))
	}
	return strings.Join(parts, " ")
}

func directoryQuotaSection(title, baseDesc, category string, cfg MediaConfig, snapshot *MediaUsageSnapshot) forms.Section {
	return forms.Section{
		Title:     title,
		Desc:      buildUsageDesc(baseDesc, category, cfg, snapshot),
		Collapsed: true,
		FieldName: "quotas",
		Nested: ptrFormDef(forms.FormDef{
			Sections: []forms.Section{
				{
					Fields: []forms.Field{
						{Name: category, Title: "Quota", Type: forms.Slider, Default: cfg.QuotaForCategory(category), Desc: "Soft limit for this permanent directory. Displayed in GB. Files are kept; GoClaw only warns when usage exceeds this quota.", Scale: bytesPerGB, Unit: "GB", Min: 0.5, Max: 10, Step: 0.5},
					},
				},
			},
		}),
	}
}

func ephemeralCategorySection(title, baseDesc, category string, cfg MediaConfig, snapshot *MediaUsageSnapshot) forms.Section {
	defaults := cfg.CategoryPolicy(category)
	return forms.Section{
		Title:     title,
		Desc:      buildUsageDesc(baseDesc, category, cfg, snapshot),
		Collapsed: true,
		FieldName: "categories",
		Nested: ptrFormDef(forms.FormDef{
			Sections: []forms.Section{
				{
					Fields: []forms.Field{
						{Name: category + ".ttl", Title: "Keep Files For (seconds)", Type: forms.Number, Default: defaults.TTL, Desc: "Files older than this are eligible for automatic cleanup."},
						{Name: category + ".quota", Title: "Quota", Type: forms.Slider, Default: defaults.Quota, Desc: "Hard limit for this directory. Displayed in GB. Cleanup removes oldest files when usage goes over this quota.", Scale: bytesPerGB, Unit: "GB", Min: 0.5, Max: 10, Step: 0.5},
					},
				},
			},
		}),
	}
}

func buildUsageDesc(baseDesc, category string, cfg MediaConfig, snapshot *MediaUsageSnapshot) string {
	if snapshot == nil {
		return baseDesc
	}
	usage, ok := snapshot.Categories[category]
	if !ok {
		return baseDesc
	}
	desc := fmt.Sprintf("%s Current usage: %s of %s across %d file(s).", baseDesc, formatGB(usage.UsedBytes), formatGB(usage.QuotaBytes), usage.FileCount)
	if usage.Permanent {
		if usage.OverQuotaBytes > 0 {
			return desc + " This directory is over quota and needs operator attention."
		}
		return desc + " Files here are preserved until you remove or move them manually."
	}
	if usage.OverQuotaBytes > 0 {
		return desc + " This directory is over quota; Clean Now will remove the oldest temporary files first."
	}
	return desc + fmt.Sprintf(" Files here expire after %d seconds.", cfg.CategoryPolicy(category).TTL)
}

// RegisterCommands registers bus commands for this component.
func RegisterCommands() {
	bus.RegisterCommand(configPath, "apply", handleApply)
	bus.RegisterCommand(configPath, "stats", handleStats)
	bus.RegisterCommand(configPath, "clean", handleClean)
}

// UnregisterCommands unregisters bus commands for this component.
func UnregisterCommands() {
	bus.UnregisterComponent(configPath)
}

func handleApply(cmd bus.Command) bus.CommandResult {
	var cfg MediaConfig
	switch v := cmd.Payload.(type) {
	case MediaConfig:
		cfg = v
	case *MediaConfig:
		if v != nil {
			cfg = *v
		}
	default:
		return bus.CommandResult{
			Success: false,
			Error:   fmt.Errorf("expected media.MediaConfig payload, got %T", cmd.Payload),
		}
	}
	cfg.Normalize()

	L_info("media: config applied",
		"dir", cfg.Dir,
		"maxSize", cfg.MaxSize,
		"cleanupEnabled", cfg.Cleanup.Enabled,
		"cleanupInterval", cfg.Cleanup.Interval,
	)
	bus.PublishEvent(configPath+".config.applied", &cfg)

	return bus.CommandResult{Success: true, Message: "Config applied"}
}

func handleStats(cmd bus.Command) bus.CommandResult {
	store := CurrentStore()
	if store == nil {
		return bus.CommandResult{
			Success: false,
			Message: "Media store is not initialized yet.",
			Error:   fmt.Errorf("media store not initialized"),
		}
	}
	snapshot, err := store.UsageSnapshot()
	if err != nil {
		return bus.CommandResult{
			Success: false,
			Message: "Failed to compute media usage.",
			Error:   err,
		}
	}
	message := fmt.Sprintf("Current media usage: %s of %s total.", formatGB(snapshot.TotalBytes), formatGB(snapshot.GlobalQuotaBytes))
	if len(snapshot.Warnings) > 0 {
		message += " " + strings.Join(snapshot.Warnings, " ")
	}
	return bus.CommandResult{
		Success: true,
		Message: message,
		Data:    snapshot,
	}
}

func handleClean(cmd bus.Command) bus.CommandResult {
	store := CurrentStore()
	if store == nil {
		return bus.CommandResult{
			Success: false,
			Message: "Media store is not initialized yet.",
			Error:   fmt.Errorf("media store not initialized"),
		}
	}
	result, err := store.CleanNow()
	if err != nil {
		return bus.CommandResult{
			Success: false,
			Message: "Media cleanup failed.",
			Error:   err,
		}
	}
	return bus.CommandResult{
		Success: true,
		Message: result.Message,
		Data:    result,
	}
}
