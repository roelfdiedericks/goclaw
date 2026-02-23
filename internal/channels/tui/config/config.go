// Package config defines the TUI channel configuration.
// This is a separate package to avoid import cycles with gateway.
package config

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	"github.com/roelfdiedericks/goclaw/internal/logging"
)

// Config holds configuration for the terminal user interface.
type Config struct {
	Enabled  bool `json:"enabled"`  // Enable TUI channel (standard pattern)
	ShowLogs bool `json:"showLogs"` // Show logs panel by default (default: true)
}

const configPath = "channels.tui"

// ConfigFormDef returns the form definition for this component's config.
func ConfigFormDef() forms.FormDef {
	return forms.FormDef{
		Title: "Terminal UI",
		Sections: []forms.Section{
			{
				Title: "Settings",
				Fields: []forms.Field{
					{Name: "Enabled", Title: "Enabled", Type: forms.Toggle, Default: true, Desc: "Enable the TUI channel"},
					{Name: "ShowLogs", Title: "Show Logs Panel", Type: forms.Toggle, Default: true, Desc: "Show the logs panel by default when TUI starts"},
				},
			},
		},
		Actions: []forms.ActionDef{
			{Name: "apply", Label: "Apply"},
		},
	}
}

// RegisterCommands registers bus commands for this component.
func RegisterCommands() {
	bus.RegisterCommand(configPath, "apply", handleApply)
}

// UnregisterCommands unregisters bus commands for this component.
func UnregisterCommands() {
	bus.UnregisterComponent(configPath)
}

func handleApply(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(*Config)
	if !ok {
		return bus.CommandResult{
			Success: false,
			Error:   fmt.Errorf("expected *config.Config, got %T", cmd.Payload),
		}
	}

	logging.L_info("tui: config applied", "enabled", cfg.Enabled, "showLogs", cfg.ShowLogs)
	bus.PublishEvent(configPath+".config.applied", cfg)

	return bus.CommandResult{Success: true, Message: "Config applied"}
}
