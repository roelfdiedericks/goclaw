// Package config defines the WhatsApp channel configuration.
// Separate package to avoid import cycles with gateway.
package config

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	"github.com/roelfdiedericks/goclaw/internal/logging"
)

// Config holds the WhatsApp channel configuration.
// Session state (keys, device identity) lives in the whatsmeow SQLite store,
// not in this config. No token or credentials needed — pairing is via QR code.
type Config struct {
	Enabled bool `json:"enabled"`
}

// ConfigFormDef returns the form definition for editing WhatsApp config
func ConfigFormDef() forms.FormDef {
	return forms.FormDef{
		Title:       "WhatsApp",
		Description: "Configure the WhatsApp channel",
		Sections: []forms.Section{
			{
				Title: "Connection",
				Fields: []forms.Field{
					{
						Name:  "enabled",
						Title: "Enabled",
						Desc:  "Enable the WhatsApp channel",
						Type:  forms.Toggle,
					},
				},
			},
		},
		Actions: []forms.ActionDef{
			{
				Name:  "apply",
				Label: "Apply Now",
				Desc:  "Apply changes to running WhatsApp channel (requires gateway)",
			},
		},
	}
}

const configPath = "channels.whatsapp"

// RegisterCommands registers whatsapp config command handlers
func RegisterCommands() {
	bus.RegisterCommand(configPath, "apply", handleApply)
}

// UnregisterCommands unregisters whatsapp config command handlers
func UnregisterCommands() {
	bus.UnregisterComponent(configPath)
}

func handleApply(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(*Config)
	if !ok {
		return bus.CommandResult{
			Error:   fmt.Errorf("invalid payload type: expected *Config, got %T", cmd.Payload),
			Message: "Internal error: invalid config type",
		}
	}

	logging.L_info("whatsapp: config applied", "enabled", cfg.Enabled)
	bus.PublishEvent(configPath+".config.applied", cfg)

	return bus.CommandResult{
		Success: true,
		Message: "Config applied - channel will restart if needed",
	}
}
