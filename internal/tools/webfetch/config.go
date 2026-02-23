package webfetch

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// WebFetchConfig holds configuration for the web fetch tool.
type WebFetchConfig struct {
	Enabled    bool   `json:"enabled"`
	UseBrowser string `json:"useBrowser"` // "auto", "always", "never"
	Profile    string `json:"profile"`    // browser profile for rendering
	Headless   bool   `json:"headless"`   // run browser in headless mode
}

const configPath = "tools.webfetch"

// ConfigFormDef returns the form definition for this tool's config.
func ConfigFormDef() forms.FormDef {
	return forms.FormDef{
		Title: "Web Fetch Tool",
		Sections: []forms.Section{
			{
				Title: "General",
				Fields: []forms.Field{
					{Name: "Enabled", Title: "Enable Tool", Type: forms.Toggle},
					{
						Name:    "UseBrowser",
						Title:   "Use Browser",
						Type:    forms.Select,
						Default: "auto",
						Options: []forms.Option{
							{Value: "auto", Label: "Auto (try browser, fallback to HTTP)"},
							{Value: "always", Label: "Always (require browser)"},
							{Value: "never", Label: "Never (HTTP only)"},
						},
					},
					{Name: "Profile", Title: "Browser Profile", Type: forms.Text, Default: "default"},
					{Name: "Headless", Title: "Headless Mode", Type: forms.Toggle, Default: true},
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
	cfg, ok := cmd.Payload.(*WebFetchConfig)
	if !ok {
		return bus.CommandResult{
			Success: false,
			Error:   fmt.Errorf("expected *WebFetchConfig, got %T", cmd.Payload),
		}
	}

	L_info("webfetch: config applied", "enabled", cfg.Enabled, "useBrowser", cfg.UseBrowser)
	bus.PublishEvent(configPath+".config.applied", cfg)

	return bus.CommandResult{Success: true, Message: "Config applied"}
}
