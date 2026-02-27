package sandbox

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Config holds top-level sandbox configuration.
type Config struct {
	Bubblewrap BubblewrapConfig `json:"bubblewrap"`
}

// BubblewrapConfig holds global bubblewrap settings shared by all sandboxed tools.
type BubblewrapConfig struct {
	Path    string   `json:"path"`    // Custom bwrap binary path (empty = search PATH)
	Volumes []string `json:"volumes"` // Isolated mount points backed by ~/.goclaw/sandbox/
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
						Name:  "Bubblewrap.Path",
						Title: "Bwrap Binary Path",
						Type:  forms.Text,
						Desc:  "Custom path to bwrap binary (empty = search PATH)",
					},
					{
						Name:  "Bubblewrap.Volumes",
						Title: "Sandbox Volumes",
						Type:  forms.StringList,
						Desc:  "Paths isolated from host filesystem, backed by ~/.goclaw/sandbox/",
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
