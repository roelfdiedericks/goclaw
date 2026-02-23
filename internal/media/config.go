package media

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

const configPath = "media"

// ConfigFormDef returns the form definition for this component's config.
func ConfigFormDef() forms.FormDef {
	return forms.FormDef{
		Title: "Media Storage",
		Sections: []forms.Section{
			{
				Title: "Settings",
				Fields: []forms.Field{
					{Name: "Dir", Title: "Storage Directory", Type: forms.Text, Desc: "Base directory for media files (empty = <workspace>/media/)"},
					{Name: "TTL", Title: "TTL (seconds)", Type: forms.Number, Default: 600, Desc: "Time-to-live for media files"},
					{Name: "MaxSize", Title: "Max File Size (bytes)", Type: forms.Number, Default: 5242880, Desc: "Maximum file size in bytes (default: 5MB)"},
				},
			},
		},
		Actions: []forms.ActionDef{
			{Name: "apply", Label: "Apply"},
		},
	}
}

// RegisterCommands registers bus commands for this component.
func RegisterCommands() {
	bus.RegisterCommand(configPath, "apply", handleApply)
}

// UnregisterCommands unregisters bus commands for this component.
func UnregisterCommands() {
	bus.UnregisterComponent(configPath)
}

func handleApply(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(*MediaConfig)
	if !ok {
		return bus.CommandResult{
			Success: false,
			Error:   fmt.Errorf("expected *media.MediaConfig, got %T", cmd.Payload),
		}
	}

	L_info("media: config applied", "dir", cfg.Dir, "ttl", cfg.TTL, "maxSize", cfg.MaxSize)
	bus.PublishEvent(configPath+".config.applied", cfg)

	return bus.CommandResult{Success: true, Message: "Config applied"}
}
