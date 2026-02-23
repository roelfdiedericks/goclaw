package xaiimagine

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// XAIImagineConfig holds configuration for the xAI imagine tool.
type XAIImagineConfig struct {
	Enabled     bool   `json:"enabled"`
	APIKey      string `json:"apiKey"`
	Model       string `json:"model"`
	Resolution  string `json:"resolution"`
	SaveToMedia bool   `json:"saveToMedia"`
}

const configPath = "tools.xai_imagine"

// ConfigFormDef returns the form definition for this tool's config.
func ConfigFormDef() forms.FormDef {
	return forms.FormDef{
		Title: "xAI Image Generation",
		Sections: []forms.Section{
			{
				Title: "General",
				Fields: []forms.Field{
					{Name: "Enabled", Title: "Enable Tool", Type: forms.Toggle},
					{Name: "APIKey", Title: "xAI API Key", Type: forms.Secret},
				},
			},
			{
				Title: "Defaults",
				Fields: []forms.Field{
					{Name: "Model", Title: "Model", Type: forms.Text, Default: "grok-2-image", Desc: "Default model for image generation"},
					{Name: "Resolution", Title: "Resolution", Type: forms.Select, Default: "1K", Options: []forms.Option{{Label: "1K", Value: "1K"}, {Label: "2K", Value: "2K"}}, Desc: "Default resolution"},
					{Name: "SaveToMedia", Title: "Save to Media", Type: forms.Toggle, Default: true, Desc: "Save generated images to media store"},
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
	cfg, ok := cmd.Payload.(*XAIImagineConfig)
	if !ok {
		return bus.CommandResult{
			Success: false,
			Error:   fmt.Errorf("expected *XAIImagineConfig, got %T", cmd.Payload),
		}
	}

	L_info("xai_imagine: config applied", "enabled", cfg.Enabled, "model", cfg.Model)
	bus.PublishEvent(configPath+".config.applied", cfg)

	return bus.CommandResult{Success: true, Message: "Config applied"}
}
