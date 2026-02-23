package hass

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// HassConfig holds configuration for the Home Assistant tool.
type HassConfig struct {
	Enabled  bool   `json:"enabled"`
	URL      string `json:"url"`
	Token    string `json:"token"`
	Insecure bool   `json:"insecure"`
	Timeout  string `json:"timeout"`
}

const configPath = "tools.hass"

// ConfigFormDef returns the form definition for this tool's config.
func ConfigFormDef() forms.FormDef {
	return forms.FormDef{
		Title: "Home Assistant Tool",
		Sections: []forms.Section{
			{
				Title: "General",
				Fields: []forms.Field{
					{Name: "Enabled", Title: "Enable Tool", Type: forms.Toggle},
				},
			},
			{
				Title: "Connection",
				Fields: []forms.Field{
					{Name: "URL", Title: "Home Assistant URL", Type: forms.Text, Desc: "e.g., https://homeassistant.local:8123"},
					{Name: "Token", Title: "Long-Lived Access Token", Type: forms.Secret},
					{Name: "Insecure", Title: "Skip TLS Verification", Type: forms.Toggle, Desc: "For self-signed certificates"},
					{Name: "Timeout", Title: "Request Timeout", Type: forms.Text, Default: "10s", Desc: "e.g., 10s, 30s"},
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
	cfg, ok := cmd.Payload.(*HassConfig)
	if !ok {
		return bus.CommandResult{
			Success: false,
			Error:   fmt.Errorf("expected *HassConfig, got %T", cmd.Payload),
		}
	}

	L_info("hass: config applied", "enabled", cfg.Enabled, "url", cfg.URL)
	bus.PublishEvent(configPath+".config.applied", cfg)

	return bus.CommandResult{Success: true, Message: "Config applied"}
}
