package exec

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// ExecConfig holds configuration for the exec tool.
type ExecConfig struct {
	Enabled        bool             `json:"enabled"`
	Timeout        int              `json:"timeout"`
	BubblewrapPath string           `json:"bubblewrapPath"`
	Bubblewrap     BubblewrapConfig `json:"bubblewrap"`
}

const configPath = "tools.exec"

// ConfigFormDef returns the form definition for this tool's config.
func ConfigFormDef() forms.FormDef {
	return forms.FormDef{
		Title: "Exec Tool",
		Sections: []forms.Section{
			{
				Title: "General",
				Fields: []forms.Field{
					{Name: "Enabled", Title: "Enable Tool", Type: forms.Toggle},
					{Name: "Timeout", Title: "Timeout (seconds)", Type: forms.Number, Default: 1800},
					{Name: "BubblewrapPath", Title: "Bubblewrap Path", Type: forms.Text, Desc: "Path to bwrap binary (optional)"},
				},
			},
			{
				Title: "Sandbox (Linux only)",
				Fields: []forms.Field{
					{Name: "Bubblewrap.Enabled", Title: "Enable Sandbox", Type: forms.Toggle},
					{Name: "Bubblewrap.AllowNetwork", Title: "Allow Network", Type: forms.Toggle},
					{Name: "Bubblewrap.ClearEnv", Title: "Clear Environment", Type: forms.Toggle},
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
	cfg, ok := cmd.Payload.(*ExecConfig)
	if !ok {
		return bus.CommandResult{
			Success: false,
			Error:   fmt.Errorf("expected *ExecConfig, got %T", cmd.Payload),
		}
	}

	L_info("exec: config applied", "enabled", cfg.Enabled, "timeout", cfg.Timeout)
	bus.PublishEvent(configPath+".config.applied", cfg)

	return bus.CommandResult{Success: true, Message: "Config applied"}
}
