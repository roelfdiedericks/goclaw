package gateway

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	gwtypes "github.com/roelfdiedericks/goclaw/internal/gateway/types"
	"github.com/roelfdiedericks/goclaw/internal/logging"
)

// Re-export types from gateway/types for convenience
type (
	AgentIdentityConfig = gwtypes.AgentIdentityConfig
	SupervisionConfig   = gwtypes.SupervisionConfig
	GuidanceConfig      = gwtypes.GuidanceConfig
	GhostwritingConfig  = gwtypes.GhostwritingConfig
)

const configPath = "gateway"

// ConfigFormDef returns the form definition for Gateway-owned configs
// (GatewayConfig, AgentIdentityConfig, PromptCacheConfig, SupervisionConfig)
func ConfigFormDef() forms.FormDef {
	return forms.FormDef{
		Title:       "Gateway Settings",
		Description: "Configure gateway, agent identity, prompt cache, and supervision",
		Sections: []forms.Section{
			{
				Title: "Gateway",
				Fields: []forms.Field{
					{Name: "Gateway.LogFile", Title: "Log File", Type: forms.Text, Desc: "Path to log file"},
					{Name: "Gateway.PIDFile", Title: "PID File", Type: forms.Text, Desc: "Path to PID file"},
					{Name: "Gateway.WorkingDir", Title: "Working Directory", Type: forms.Text, Desc: "Working directory for sessions"},
				},
			},
			{
				Title: "Agent Identity",
				Fields: []forms.Field{
					{Name: "Agent.Name", Title: "Agent Name", Type: forms.Text, Default: "GoClaw", Desc: "Display name for the agent"},
					{Name: "Agent.Emoji", Title: "Emoji Prefix", Type: forms.Text, Desc: "Optional emoji prefix for agent name"},
					{Name: "Agent.Typing", Title: "Typing Text", Type: forms.Text, Desc: "Custom typing indicator text"},
				},
			},
			{
				Title:     "Prompt Cache",
				Collapsed: true,
				Fields: []forms.Field{
					{Name: "PromptCache.PollInterval", Title: "Poll Interval (seconds)", Type: forms.Number, Default: 60, Desc: "Hash poll interval for prompt cache (0 = disabled)"},
				},
			},
			{
				Title:     "Supervision - Guidance",
				Collapsed: true,
				Fields: []forms.Field{
					{Name: "Supervision.Guidance.Prefix", Title: "Guidance Prefix", Type: forms.Text, Default: "[Supervisor]: ", Desc: "Prefix for supervisor guidance messages"},
					{Name: "Supervision.Guidance.SystemNote", Title: "System Note", Type: forms.Text, Desc: "Note injected into system prompt about supervision"},
				},
			},
			{
				Title:     "Supervision - Ghostwriting",
				Collapsed: true,
				Fields: []forms.Field{
					{Name: "Supervision.Ghostwriting.Enabled", Title: "Enable Ghostwriting", Type: forms.Toggle, Desc: "Allow supervisor to send messages as the agent"},
					{Name: "Supervision.Ghostwriting.TypingDelayMs", Title: "Typing Delay (ms)", Type: forms.Number, Default: 500, Desc: "Delay before sending ghostwritten message"},
				},
			},
		},
		Actions: []forms.ActionDef{
			{Name: "apply", Label: "Apply"},
		},
	}
}

// GatewayConfigBundle holds all gateway-owned config sections
type GatewayConfigBundle struct {
	Gateway     config.GatewayConfig     `json:"gateway"`
	Agent       AgentIdentityConfig      `json:"agent"`
	PromptCache config.PromptCacheConfig `json:"promptCache"`
	Supervision SupervisionConfig        `json:"supervision"`
}

// RegisterCommands registers config commands for gateway.
func RegisterCommands() {
	bus.RegisterCommand(configPath, "apply", handleApply)
}

// UnregisterCommands unregisters config commands.
func UnregisterCommands() {
	bus.UnregisterCommand(configPath, "apply")
}

// handleApply publishes config.applied events for listeners to react
// Publishes:
//   - gateway.config.applied (full bundle)
//   - gateway.agent.config.applied (agent identity only)
//   - gateway.supervision.config.applied (supervision only)
func handleApply(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(*GatewayConfigBundle)
	if !ok {
		return bus.CommandResult{
			Error:   fmt.Errorf("expected *GatewayConfigBundle, got %T", cmd.Payload),
			Message: "invalid payload type",
		}
	}

	logging.L_info("gateway: config applied", "agentName", cfg.Agent.Name, "promptCachePoll", cfg.PromptCache.PollInterval)

	// Publish full bundle for gateway itself
	bus.PublishEvent(configPath+".config.applied", cfg)

	// Publish specific events for channels to react to identity/supervision changes
	bus.PublishEvent(configPath+".agent.config.applied", &cfg.Agent)
	bus.PublishEvent(configPath+".supervision.config.applied", &cfg.Supervision)

	return bus.CommandResult{
		Success: true,
		Message: "Config applied",
	}
}
