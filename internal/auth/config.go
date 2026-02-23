package auth

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// CredentialHint describes a credential the auth script accepts
type CredentialHint struct {
	Key      string `json:"key"`                // JSON field name to pass to script
	Label    string `json:"label,omitempty"`    // Friendly name to ask user for (defaults to Key)
	Required bool   `json:"required,omitempty"` // Whether this credential is required
}

// AuthConfig configures role elevation via external authentication
type AuthConfig struct {
	Enabled         bool             `json:"enabled"`         // Enable user_auth tool
	Script          string           `json:"script"`          // Path to auth script
	AllowedRoles    []string         `json:"allowedRoles"`    // Roles script can return (empty = disabled)
	CredentialHints []CredentialHint `json:"credentialHints"` // Credentials the script accepts (shown to agent)
	RateLimit       int              `json:"rateLimit"`       // Max attempts per minute (default: 3)
	Timeout         int              `json:"timeout"`         // Script timeout in seconds (default: 10)
}

// AConfig is an alias for AuthConfig for convenience
// (Named AuthConfig rather than Config for clarity when embedded in main Config struct)
type AConfig = AuthConfig

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
	cfg, ok := cmd.Payload.(AuthConfig)
	if !ok {
		cfgPtr, okPtr := cmd.Payload.(*AuthConfig)
		if okPtr {
			cfg = *cfgPtr
			ok = true
		}
	}
	if !ok {
		return bus.CommandResult{
			Error:   fmt.Errorf("expected AuthConfig, got %T", cmd.Payload),
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
