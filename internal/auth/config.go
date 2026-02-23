package auth

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// AConfig is an alias for config.AuthConfig to avoid name collisions
type AConfig = config.AuthConfig

const configPath = "auth"

// ConfigFormDef returns the form definition for AuthConfig
func ConfigFormDef() forms.FormDef {
	return forms.FormDef{
		Title:       "Role Elevation",
		Description: "Configure role elevation via external authentication script",
		Sections: []forms.Section{
			{
				Title: "General",
				Fields: []forms.Field{
					{Name: "Enabled", Title: "Enabled", Type: forms.Toggle, Default: false, Desc: "Enable user_auth tool for role elevation"},
					{Name: "Script", Title: "Auth Script", Type: forms.Text, Desc: "Path to authentication script"},
				},
			},
			{
				Title:     "Security",
				Collapsed: true,
				Fields: []forms.Field{
					{Name: "RateLimit", Title: "Rate Limit", Type: forms.Number, Default: 3, Desc: "Max attempts per minute"},
					{Name: "Timeout", Title: "Timeout (seconds)", Type: forms.Number, Default: 10, Desc: "Script execution timeout"},
				},
			},
		},
		Actions: []forms.ActionDef{
			{Name: "apply", Label: "Apply"},
		},
	}
}

// RegisterCommands registers config commands for auth.
func RegisterCommands() {
	bus.RegisterCommand(configPath, "apply", handleApply)
}

// UnregisterCommands unregisters config commands.
func UnregisterCommands() {
	bus.UnregisterCommand(configPath, "apply")
}

// handleApply publishes the config.applied event for listeners to react
func handleApply(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(*config.AuthConfig)
	if !ok {
		return bus.CommandResult{
			Error:   fmt.Errorf("expected *AuthConfig, got %T", cmd.Payload),
			Message: "invalid payload type",
		}
	}

	L_info("auth: config applied", "enabled", cfg.Enabled, "script", cfg.Script)
	bus.PublishEvent(configPath+".config.applied", cfg)

	return bus.CommandResult{
		Success: true,
		Message: "Config applied",
	}
}
