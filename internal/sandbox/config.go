package sandbox

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Sandbox modes
const (
	ModeEphemeral = "ephemeral" // No persistent home dirs - maximum security
	ModeVolumes   = "volumes"   // Specific directories persisted via isolated mounts
	ModeHome      = "home"      // Full isolated home directory - everything persists
)

// Config holds top-level sandbox configuration.
type Config struct {
	Bubblewrap BubblewrapConfig `json:"bubblewrap"`
}

// BubblewrapConfig holds global bubblewrap settings shared by all sandboxed tools.
type BubblewrapConfig struct {
	Path       string   `json:"path"`       // Custom bwrap binary path (empty = search PATH)
	Mode       string   `json:"mode"`       // "ephemeral", "volumes", "home" (default: "home")
	DataDir    string   `json:"dataDir"`    // Backing directory root (default: ~/.goclaw/sandbox)
	Volumes    []string `json:"volumes"`    // Isolated mount points (only used in "volumes" mode)
	ExtraPaths []string `json:"extraPaths"` // Additional PATH entries for sandbox (appended after defaults)
}

// GetMode returns the configured mode with default fallback.
func (c *BubblewrapConfig) GetMode() string {
	if c.Mode == "" {
		return ModeHome
	}
	return c.Mode
}

// DefaultVolumes returns the built-in sandbox volume mount points.
func DefaultVolumes() []string {
	return []string{"~/.local", "~/.config", "~/.cache"}
}

const configPath = "sandbox"

// ConfigFormDef returns the form definition for sandbox configuration.
func ConfigFormDef() forms.FormDef {
	return forms.FormDef{
		Title:       "Sandbox",
		Description: "Configure agent sandboxing and filesystem isolation",
		Sections: []forms.Section{
			{
				Title: "Bubblewrap",
				Fields: []forms.Field{
					{
						Name:  "Bubblewrap.Mode",
						Title: "Sandbox Mode",
						Type:  forms.Select,
						Desc:  "How home directories are handled inside the sandbox",
						Options: []forms.Option{
							{Label: "Home (full isolated home - recommended)", Value: ModeHome},
							{Label: "Volumes (specific dirs only)", Value: ModeVolumes},
							{Label: "Ephemeral (nothing persists)", Value: ModeEphemeral},
						},
					},
					{
						Name:  "Bubblewrap.Path",
						Title: "Bwrap Binary Path",
						Type:  forms.Text,
						Desc:  "Custom path to bwrap binary (empty = search PATH)",
					},
					{
						Name:  "Bubblewrap.DataDir",
						Title: "Data Directory",
						Type:  forms.Text,
						Desc:  "Backing storage root (default: ~/.goclaw/sandbox)",
					},
					{
						Name:  "Bubblewrap.ExtraPaths",
						Title: "Extra PATH Entries",
						Type:  forms.StringList,
						Desc:  "Additional directories to add to sandbox PATH",
					},
				},
			},
			{
				Title:    "Volume Mounts",
				ShowWhen: "Bubblewrap.Mode=volumes",
				Fields: []forms.Field{
					{
						Name:  "Bubblewrap.Volumes",
						Title: "Sandbox Volumes",
						Type:  forms.StringList,
						Desc:  "Home directory paths to persist (e.g., ~/.local, ~/.config)",
					},
				},
			},
		},
		Actions: []forms.ActionDef{
			{Name: "apply", Label: "Apply"},
		},
	}
}

// RegisterCommands registers bus commands for sandbox config.
func RegisterCommands() {
	bus.RegisterCommand(configPath, "apply", handleApply)
}

// UnregisterCommands unregisters bus commands for sandbox config.
func UnregisterCommands() {
	bus.UnregisterComponent(configPath)
}

func handleApply(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(*Config)
	if !ok {
		return bus.CommandResult{
			Success: false,
			Error:   fmt.Errorf("expected *Config, got %T", cmd.Payload),
		}
	}

	L_info("sandbox: config applied", "bwrapPath", cfg.Bubblewrap.Path, "volumes", len(cfg.Bubblewrap.Volumes))
	bus.PublishEvent(configPath+".config.applied", cfg)

	return bus.CommandResult{Success: true, Message: "Config applied"}
}
