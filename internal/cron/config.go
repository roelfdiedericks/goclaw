package cron

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// CronConfig configures the cron scheduler
type CronConfig struct {
	Enabled           bool            `json:"enabled"`           // Enable cron scheduler (default: true)
	JobTimeoutMinutes int             `json:"jobTimeoutMinutes"` // Timeout for job execution in minutes (default: 30, 0 = no timeout)
	Heartbeat         HeartbeatConfig `json:"heartbeat"`         // Heartbeat configuration
}

// HeartbeatConfig configures the periodic heartbeat system
type HeartbeatConfig struct {
	Enabled         bool   `json:"enabled"`         // Enable heartbeat (default: true)
	IntervalMinutes int    `json:"intervalMinutes"` // Interval in minutes (default: 30)
	Prompt          string `json:"prompt"`          // Custom heartbeat prompt (optional)
}

// CConfig is an alias for CronConfig for convenience
// (Cannot use "Config" due to dot-import conflict with logging.Config)
type CConfig = CronConfig

const configPath = "cron"

// ConfigFormDef returns the form definition for CronConfig
func ConfigFormDef() forms.FormDef {
	return forms.FormDef{
		Title:       "Cron Scheduler",
		Description: "Configure scheduled jobs and heartbeat",
		Sections: []forms.Section{
			{
				Title: "General",
				Fields: []forms.Field{
					{Name: "Enabled", Title: "Enabled", Type: forms.Toggle, Default: true, Desc: "Enable the cron scheduler"},
					{Name: "JobTimeoutMinutes", Title: "Job Timeout (minutes)", Type: forms.Number, Default: 30, Desc: "Timeout for job execution (0 = no timeout)"},
				},
			},
			{
				Title:     "Heartbeat",
				Collapsed: true,
				Fields: []forms.Field{
					{Name: "Heartbeat.Enabled", Title: "Enable Heartbeat", Type: forms.Toggle, Default: true, Desc: "Enable periodic heartbeat"},
					{Name: "Heartbeat.IntervalMinutes", Title: "Interval (minutes)", Type: forms.Number, Default: 30, Desc: "Heartbeat interval"},
					{Name: "Heartbeat.Prompt", Title: "Custom Prompt", Type: forms.Text, Desc: "Custom heartbeat prompt (optional)"},
				},
			},
		},
		Actions: []forms.ActionDef{
			{Name: "apply", Label: "Apply"},
		},
	}
}

// RegisterCommands registers config commands for cron.
func RegisterCommands() {
	bus.RegisterCommand(configPath, "apply", handleApply)
}

// UnregisterCommands unregisters config commands.
func UnregisterCommands() {
	bus.UnregisterCommand(configPath, "apply")
}

// handleApply publishes the config.applied event for listeners to react
func handleApply(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(CronConfig)
	if !ok {
		cfgPtr, okPtr := cmd.Payload.(*CronConfig)
		if okPtr {
			cfg = *cfgPtr
			ok = true
		}
	}
	if !ok {
		return bus.CommandResult{
			Error:   fmt.Errorf("expected CronConfig, got %T", cmd.Payload),
			Message: "invalid payload type",
		}
	}

	L_info("cron: config applied", "enabled", cfg.Enabled, "heartbeatEnabled", cfg.Heartbeat.Enabled)
	bus.PublishEvent(configPath+".config.applied", cfg)

	return bus.CommandResult{
		Success: true,
		Message: "Config applied",
	}
}
