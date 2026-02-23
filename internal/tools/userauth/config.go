package userauth

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// UserAuthConfig holds configuration for the user_auth tool.
type UserAuthConfig struct {
	Enabled   bool   `json:"enabled"`
	Script    string `json:"script"`
	Timeout   int    `json:"timeout"`
	RateLimit int    `json:"rateLimit"`
}

const configPath = "tools.user_auth"

// ConfigFormDef returns the form definition for this tool's config.
func ConfigFormDef() forms.FormDef {
	return forms.FormDef{
		Title: "User Authentication Tool",
		Sections: []forms.Section{
			{
				Title: "General",
				Fields: []forms.Field{
					{Name: "Enabled", Title: "Enable Tool", Type: forms.Toggle},
					{Name: "Script", Title: "Auth Script Path", Type: forms.Text, Desc: "Path to authentication script"},
					{Name: "Timeout", Title: "Script Timeout (seconds)", Type: forms.Number, Default: 10},
					{Name: "RateLimit", Title: "Rate Limit (per minute)", Type: forms.Number, Default: 3},
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
	cfg, ok := cmd.Payload.(*UserAuthConfig)
	if !ok {
		return bus.CommandResult{
			Success: false,
			Error:   fmt.Errorf("expected *UserAuthConfig, got %T", cmd.Payload),
		}
	}

	L_info("user_auth: config applied", "enabled", cfg.Enabled, "script", cfg.Script)
	bus.PublishEvent(configPath+".config.applied", cfg)

	return bus.CommandResult{Success: true, Message: "Config applied"}
}
