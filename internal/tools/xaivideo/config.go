package xaivideo

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	toolsconfig "github.com/roelfdiedericks/goclaw/internal/tools/config"
)

const configPath = "tools.xai_video"

// ConfigFormDef returns the form definition for this tool's config.
func ConfigFormDef() forms.FormDef {
	return forms.FormDef{
		Title: "xAI Video Generation",
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
					{Name: "Model", Title: "Model", Type: forms.Text, Default: "grok-imagine-video", Desc: "Default model for video generation"},
					{Name: "Resolution", Title: "Resolution", Type: forms.Select, Default: "480p", Options: []forms.Option{{Label: "480p", Value: "480p"}, {Label: "720p", Value: "720p"}}, Desc: "Default resolution"},
					{Name: "Duration", Title: "Duration (seconds)", Type: forms.Number, Default: 5, Desc: "Default video duration (1-15 seconds)"},
					{Name: "SaveToMedia", Title: "Save to Media", Type: forms.Toggle, Default: true, Desc: "Save generated videos to media store"},
				},
			},
			{
				Title: "Polling",
				Fields: []forms.Field{
					{Name: "PollInterval", Title: "Poll Interval (seconds)", Type: forms.Number, Default: 5, Desc: "Seconds between status checks"},
					{Name: "Timeout", Title: "Timeout (seconds)", Type: forms.Number, Default: 600, Desc: "Maximum wait time for generation (default: 10 minutes)"},
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
	cfg, ok := cmd.Payload.(*toolsconfig.XAIVideoConfig)
	if !ok {
		return bus.CommandResult{
			Success: false,
			Error:   fmt.Errorf("expected *XAIVideoConfig, got %T", cmd.Payload),
		}
	}

	L_info("xai_video: config applied", "enabled", cfg.Enabled, "model", cfg.Model, "resolution", cfg.Resolution)
	bus.PublishEvent(configPath+".config.applied", cfg)

	return bus.CommandResult{Success: true, Message: "Config applied"}
}
