package jq

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// JQConfig holds configuration for the jq tool.
type JQConfig struct {
	Enabled bool `json:"enabled"`
}

const configPath = "tools.jq"

// ConfigFormDef returns the form definition for this tool's config.
func ConfigFormDef() forms.FormDef {
	return forms.FormDef{
		Title: "JQ Tool",
		Sections: []forms.Section{
			{
				Title: "General",
				Fields: []forms.Field{
					{Name: "Enabled", Title: "Enable Tool", Type: forms.Toggle},
				},
			},
		},
		Actions: []forms.ActionDef{
			{Name: "apply", Label: "Apply"},
		},
	}
}

// RegisterCommands registers bus commands for this tool.
func RegisterCommands() {
	bus.RegisterCommand(configPath, "apply", handleApply)
}

// UnregisterCommands unregisters bus commands for this tool.
func UnregisterCommands() {
	bus.UnregisterComponent(configPath)
}

func handleApply(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(*JQConfig)
	if !ok {
		return bus.CommandResult{
			Success: false,
			Error:   fmt.Errorf("expected *JQConfig, got %T", cmd.Payload),
		}
	}

	L_info("jq: config applied", "enabled", cfg.Enabled)
	bus.PublishEvent(configPath+".config.applied", cfg)

	return bus.CommandResult{Success: true, Message: "Config applied"}
}
