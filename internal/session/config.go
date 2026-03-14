package session

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

const configPath = "session"

// ConfigFormDef returns the form definition for SessionConfig
func ConfigFormDef() forms.FormDef {
	return forms.FormDef{
		Title:       "Session Management",
		Description: "Configure session persistence and context management",
		Sections: []forms.Section{
			{
				Title: "Storage",
				Fields: []forms.Field{
					{Name: "store", Title: "Storage Backend", Type: forms.Select, Default: "sqlite",
						Options: []forms.Option{{Label: "SQLite", Value: "sqlite"}},
						Desc:    "Storage backend for sessions"},
					{Name: "storePath", Title: "Store Path", Type: forms.Text, Desc: "Path to storage (DB file or sessions directory)"},
				},
			},
			{
				Title:     "OpenClaw Inheritance",
				Collapsed: true,
				Fields: []forms.Field{
					{Name: "inherit", Title: "Enable Inheritance", Type: forms.Toggle, Desc: "Inherit context from OpenClaw session"},
					{Name: "inheritPath", Title: "Sessions Directory", Type: forms.Text, Desc: "Path to OpenClaw sessions directory"},
					{Name: "inheritFrom", Title: "Inherit From", Type: forms.Text, Desc: "Session key to inherit from (e.g., agent:main:main)"},
				},
			},
			{
				Title:     "Memory Flush",
				Collapsed: true,
				Fields: []forms.Field{
					{Name: "memoryFlush.enabled", Title: "Enable Memory Flush", Type: forms.Toggle, Desc: "Prompt for memory writes at context thresholds"},
				},
			},
			{
				Title:     "Summarization",
				Collapsed: true,
				Fields: []forms.Field{
					{Name: "summarization.checkpoint.enabled", Title: "Enable Checkpoints", Type: forms.Toggle, Desc: "Generate rolling checkpoints"},
				},
			},
		},
		Actions: []forms.ActionDef{
			{Name: "apply", Label: "Apply"},
		},
	}
}

// RegisterCommands registers config commands for session.
func RegisterCommands() {
	bus.RegisterCommand(configPath, "apply", handleApply)
}

// UnregisterCommands unregisters config commands.
func UnregisterCommands() {
	bus.UnregisterCommand(configPath, "apply")
}

// handleApply publishes the config.applied event for listeners to react
func handleApply(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(*SessionConfig)
	if !ok {
		return bus.CommandResult{
			Error:   fmt.Errorf("expected *SessionConfig, got %T", cmd.Payload),
			Message: "invalid payload type",
		}
	}

	L_info("session: config applied", "store", cfg.Store, "storePath", cfg.StorePath)
	bus.PublishEvent(configPath+".config.applied", cfg)

	return bus.CommandResult{
		Success: true,
		Message: "Config applied",
	}
}
