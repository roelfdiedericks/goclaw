package tui

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// TUIConfig holds configuration for the terminal user interface.
type TUIConfig struct {
	ShowLogs bool `json:"showLogs"` // Show logs panel by default (default: true)
}

const configPath = "tui"

// ConfigFormDef returns the form definition for this component's config.
func ConfigFormDef() forms.FormDef {
	return forms.FormDef{
		Title: "Terminal UI",
		Sections: []forms.Section{
			{
				Title: "Settings",
				Fields: []forms.Field{
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
	cfg, ok := cmd.Payload.(*TUIConfig)
	if !ok {
		return bus.CommandResult{
			Success: false,
			Error:   fmt.Errorf("expected *tui.TUIConfig, got %T", cmd.Payload),
		}
	}

	L_info("tui: config applied", "showLogs", cfg.ShowLogs)
	bus.PublishEvent(configPath+".config.applied", cfg)

	return bus.CommandResult{Success: true, Message: "Config applied"}
}
