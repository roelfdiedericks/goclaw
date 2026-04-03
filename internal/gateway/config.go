package gateway

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	gwtypes "github.com/roelfdiedericks/goclaw/internal/gateway/types"
	"github.com/roelfdiedericks/goclaw/internal/logging"
)

// Re-export types from gateway/types for convenience
type (
	GatewayConfig       = gwtypes.GatewayConfig
	ToolExecutionConfig = gwtypes.ToolExecutionConfig
	PromptCacheConfig   = gwtypes.PromptCacheConfig
	AgentIdentityConfig = gwtypes.AgentIdentityConfig
	SupervisionConfig   = gwtypes.SupervisionConfig
	GuidanceConfig      = gwtypes.GuidanceConfig
	GhostwritingConfig  = gwtypes.GhostwritingConfig
	SafetyConfig        = gwtypes.SafetyConfig
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
					{Name: "gateway.logFile", Title: "Log File", Type: forms.Text, Desc: "Path to log file"},
					{Name: "gateway.pidFile", Title: "PID File", Type: forms.Text, Desc: "Path to PID file"},
					{Name: "gateway.workingDir", Title: "Working Directory", Type: forms.Text, Desc: "Working directory for sessions"},
					{Name: "gateway.acpCursorModel", Title: "ACP Cursor Model", Type: forms.Text, Default: "claude-4.6-opus-high-thinking", Desc: "Friendly ACP model alias to apply after attaching to a Cursor ACP session"},
				},
			},
			{
				Title: "Delegated Runs",
				Fields: []forms.Field{
					{Name: "gateway.delegatedRuns.enabled", Title: "Enable Delegated Runs", Type: forms.Toggle, Default: true, Desc: "Enable delegated runner path for cron/subagents"},
					{Name: "gateway.delegatedRuns.maxSpawnDepth", Title: "Delegated Max Spawn Depth", Type: forms.Number, Default: 4, Desc: "Maximum parent->child depth for spawned delegated runs (0 = unlimited)"},
					{Name: "gateway.delegatedRuns.maxActiveChildrenPerParent", Title: "Delegated Max Active Children Per Parent", Type: forms.Number, Default: 4, Desc: "Maximum active child runs per parent run (0 = unlimited)"},
					{Name: "gateway.delegatedRuns.maxConcurrentRuns", Title: "Delegated Max Concurrent Runs", Type: forms.Number, Default: 16, Desc: "Delegated runner lane capacity (0 = unlimited)"},
					{Name: "gateway.delegatedRuns.defaultTimeoutSeconds", Title: "Delegated Default Timeout (seconds)", Type: forms.Number, Default: 300, Desc: "Applied when delegated runs omit timeoutSeconds (0 = no default timeout)"},
					{Name: "gateway.delegatedRuns.maxTimeoutSeconds", Title: "Delegated Max Timeout (seconds)", Type: forms.Number, Default: 1800, Desc: "Safety cap for delegated run timeoutSeconds (0 = unlimited)"},
				},
			},
			{
				Title: "Agent Identity",
				Fields: []forms.Field{
					{Name: "agent.name", Title: "Agent Name", Type: forms.Text, Default: "GoClaw", Desc: "Display name for the agent"},
					{Name: "agent.emoji", Title: "Emoji Prefix", Type: forms.Text, Default: "🐾", Desc: "Optional emoji prefix for agent name"},
					{Name: "agent.typing", Title: "Typing Text", Type: forms.Text, Desc: "Custom typing indicator text"},
				},
			},
			{
				Title:     "Prompt Cache",
				Collapsed: true,
				Fields: []forms.Field{
					{Name: "promptCache.pollInterval", Title: "Poll Interval (seconds)", Type: forms.Number, Default: 60, Desc: "Hash poll interval for prompt cache (0 = disabled)"},
					{Name: "promptCache.timeInUserMessage", Title: "Show Time to Agent", Type: forms.Toggle, Default: true, Desc: "Inject current time before user messages"},
					{Name: "promptCache.showUptime", Title: "Show Uptime to Agent", Type: forms.Toggle, Default: true, Desc: "Include gateway uptime with time (privacy sensitive)"},
				},
			},
			{
				Title:     "Tool Execution",
				Collapsed: true,
				Fields: []forms.Field{
					{Name: "gateway.toolExecution.parallelEnabled", Title: "Enable Parallel Tool Execution", Type: forms.Toggle, Default: true, Desc: "Run allowlisted readonly tool batches concurrently"},
					{Name: "gateway.toolExecution.maxConcurrent", Title: "Max Concurrent Tools", Type: forms.Number, Default: 3, Desc: "Worker limit for parallel tool batches"},
					{Name: "gateway.toolExecution.parallelAllowlist", Title: "Parallel Allowlist", Type: forms.StringList, Placeholder: "read, web_search, web_fetch", Desc: "Tool names allowed to run in parallel. Empty = built-in readonly defaults."},
				},
			},
			{
				Title:     "Supervision - Guidance",
				Collapsed: true,
				Fields: []forms.Field{
					{Name: "supervision.guidance.prefix", Title: "Guidance Prefix", Type: forms.Text, Default: "[Supervisor]: ", Desc: "Prefix for supervisor guidance messages"},
					{Name: "supervision.guidance.systemNote", Title: "System Note", Type: forms.Text, Desc: "Note injected into system prompt about supervision"},
				},
			},
			{
				Title:     "Supervision - Ghostwriting",
				Collapsed: true,
				Fields: []forms.Field{
					{Name: "supervision.ghostwriting.typingDelayMs", Title: "Typing Delay (ms)", Type: forms.Number, Default: 500, Desc: "Delay before sending ghostwritten message"},
				},
			},
			{
				Title:     "Safety - Emergency Control",
				Collapsed: true,
				Fields: []forms.Field{
					{Name: "safety.panicEnabled", Title: "Enable STOP Phrases", Type: forms.Toggle, Default: true, Desc: "Allow panic phrases to trigger emergency stop"},
					{Name: "safety.panicPhrases", Title: "STOP Phrases", Type: forms.StringList, Default: []string{"STOP", "STOP NOW"}, Placeholder: "STOP, STOP NOW", Desc: "Exact phrases that trigger emergency stop. Leave empty to use built-in defaults."},
					{Name: "safety.shutdownEnabled", Title: "Enable SHUTDOWN Phrases", Type: forms.Toggle, Default: true, Desc: "Allow shutdown phrases to gracefully stop GoClaw (owner only)"},
					{Name: "safety.shutdownPhrases", Title: "SHUTDOWN Phrases", Type: forms.StringList, Default: []string{"SHUTDOWN NOW"}, Placeholder: "SHUTDOWN NOW", Desc: "Exact phrases that trigger graceful shutdown. Leave empty to use built-in defaults."},
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
	Gateway     GatewayConfig       `json:"gateway"`
	Agent       AgentIdentityConfig `json:"agent"`
	PromptCache PromptCacheConfig   `json:"promptCache"`
	Supervision SupervisionConfig   `json:"supervision"`
	Safety      SafetyConfig        `json:"safety"`
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
