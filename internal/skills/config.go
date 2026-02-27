package skills

import (
	"fmt"
	"os"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// SkillInstallConfig configures skill installation sources
type SkillInstallConfig struct {
	AllowEmbedded *bool `json:"allowEmbedded,omitempty"` // Allow installing from embedded catalog (default: true)
	AllowClawHub  bool  `json:"allowClawHub"`            // Allow installing from ClawHub repository (default: false)
	AllowLocal    bool  `json:"allowLocal"`              // Allow installing from local paths (default: false, security risk)
}

// IsEmbeddedAllowed returns true if embedded installation is allowed (defaults to true)
func (c SkillInstallConfig) IsEmbeddedAllowed() bool {
	if c.AllowEmbedded == nil {
		return true // Default to enabled
	}
	return *c.AllowEmbedded
}

// SkillsConfig configures the skills system
type SkillsConfig struct {
	Enabled       bool                        `json:"enabled"`
	BundledDir    string                      `json:"bundledDir"`      // Override bundled skills path (deprecated)
	ManagedDir    string                      `json:"managedDir"`      // Override managed skills path (deprecated)
	WorkspaceDir  string                      `json:"workspaceDir"`    // Override workspace skills path
	ExtraDirs     []string                    `json:"extraDirs"`       // Additional skill directories
	Install       SkillInstallConfig          `json:"install"`         // Installation source configuration
	Watch         bool                        `json:"watch"`           // Watch for file changes
	WatchDebounce int                         `json:"watchDebounceMs"` // Debounce interval in ms
	Entries       map[string]SkillEntryConfig `json:"entries"`         // Per-skill configuration
}

// Note: SkillEntryConfig is defined in types.go

// SkConfig is an alias for SkillsConfig for convenience
// (Named SkillsConfig rather than Config for clarity when embedded in main Config struct)
type SkConfig = SkillsConfig

const configPath = "skills"

// ConfigFormDef returns the form definition for SkillsConfig
func ConfigFormDef() forms.FormDef {
	return forms.FormDef{
		Title:       "Skills System",
		Description: "Configure skill loading and directories",
		Sections: []forms.Section{
			{
				Title: "General",
				Fields: []forms.Field{
					{Name: "Enabled", Title: "Enabled", Type: forms.Toggle, Default: true, Desc: "Enable the skills system"},
					{Name: "Watch", Title: "Watch for Changes", Type: forms.Toggle, Default: true, Desc: "Auto-reload when skill files change"},
					{Name: "WatchDebounce", Title: "Watch Debounce (ms)", Type: forms.Number, Default: 500, Desc: "Debounce interval for file changes"},
				},
			},
			{
				Title:     "Directories",
				Collapsed: true,
				Fields: []forms.Field{
					{Name: "BundledDir", Title: "Bundled Skills Dir", Type: forms.Text, Desc: "Override bundled skills path"},
					{Name: "ManagedDir", Title: "Managed Skills Dir", Type: forms.Text, Desc: "Override managed skills path"},
					{Name: "WorkspaceDir", Title: "Workspace Skills Dir", Type: forms.Text, Desc: "Override workspace skills path"},
				},
			},
		},
		Actions: []forms.ActionDef{
			{Name: "test", Label: "Test"},
			{Name: "apply", Label: "Apply"},
		},
	}
}

// RegisterCommands registers config commands for skills.
func RegisterCommands() {
	bus.RegisterCommand(configPath, "test", handleTest)
	bus.RegisterCommand(configPath, "apply", handleApply)
}

// UnregisterCommands unregisters config commands.
func UnregisterCommands() {
	bus.UnregisterCommand(configPath, "test")
	bus.UnregisterCommand(configPath, "apply")
}

// handleTest validates skills configuration (checks directories exist)
func handleTest(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(SkillsConfig)
	if !ok {
		cfgPtr, okPtr := cmd.Payload.(*SkillsConfig)
		if okPtr {
			cfg = *cfgPtr
			ok = true
		}
	}
	if !ok {
		return bus.CommandResult{
			Error:   fmt.Errorf("expected SkillsConfig, got %T", cmd.Payload),
			Message: "invalid payload type",
		}
	}

	// Check configured directories exist
	var issues []string

	if cfg.BundledDir != "" {
		if _, err := os.Stat(cfg.BundledDir); err != nil {
			issues = append(issues, fmt.Sprintf("bundledDir not found: %s", cfg.BundledDir))
		}
	}
	if cfg.ManagedDir != "" {
		if _, err := os.Stat(cfg.ManagedDir); err != nil {
			issues = append(issues, fmt.Sprintf("managedDir not found: %s", cfg.ManagedDir))
		}
	}
	if cfg.WorkspaceDir != "" {
		if _, err := os.Stat(cfg.WorkspaceDir); err != nil {
			issues = append(issues, fmt.Sprintf("workspaceDir not found: %s", cfg.WorkspaceDir))
		}
	}
	for _, dir := range cfg.ExtraDirs {
		if _, err := os.Stat(dir); err != nil {
			issues = append(issues, fmt.Sprintf("extraDir not found: %s", dir))
		}
	}

	if len(issues) > 0 {
		return bus.CommandResult{
			Success: false,
			Message: fmt.Sprintf("Directory issues: %v", issues),
		}
	}

	return bus.CommandResult{
		Success: true,
		Message: "Skills configuration valid",
	}
}

// handleApply publishes the config.applied event for listeners to react
func handleApply(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(SkillsConfig)
	if !ok {
		cfgPtr, okPtr := cmd.Payload.(*SkillsConfig)
		if okPtr {
			cfg = *cfgPtr
			ok = true
		}
	}
	if !ok {
		return bus.CommandResult{
			Error:   fmt.Errorf("expected SkillsConfig, got %T", cmd.Payload),
			Message: "invalid payload type",
		}
	}

	L_info("skills: config applied", "enabled", cfg.Enabled, "watch", cfg.Watch)
	bus.PublishEvent(configPath+".config.applied", cfg)

	return bus.CommandResult{
		Success: true,
		Message: "Config applied",
	}
}
