// Package types defines gateway-owned configuration types that are shared across packages.
// These types are defined here to avoid import cycles between config and gateway packages.
package types

// GatewayConfig contains gateway server settings
type GatewayConfig struct {
	LogFile        string              `json:"logFile"`
	PIDFile        string              `json:"pidFile"`
	WorkingDir     string              `json:"workingDir"`
	ACPCursorModel string              `json:"acpCursorModel" default:"claude-4.6-opus-high-thinking"`
	DelegatedRuns  DelegatedRunsConfig `json:"delegatedRuns"`
	ToolExecution  ToolExecutionConfig `json:"toolExecution"`
}

// DelegatedRunsConfig controls delegated runner behavior and rollout.
type DelegatedRunsConfig struct {
	Enabled                    bool `json:"enabled" default:"true"`
	MaxSpawnDepth              int  `json:"maxSpawnDepth" default:"4"`
	MaxActiveChildrenPerParent int  `json:"maxActiveChildrenPerParent" default:"4"`
	MaxConcurrentRuns          int  `json:"maxConcurrentRuns" default:"16"`
	DefaultTimeoutSeconds      int  `json:"defaultTimeoutSeconds" default:"300"`
	MaxTimeoutSeconds          int  `json:"maxTimeoutSeconds" default:"1800"`
}

// ToolExecutionConfig configures how the gateway executes model-requested tools.
type ToolExecutionConfig struct {
	ParallelEnabled   bool     `json:"parallelEnabled" default:"true"`
	MaxConcurrent     int      `json:"maxConcurrent" default:"3"`
	ParallelAllowlist []string `json:"parallelAllowlist"`
}

// PromptCacheConfig configures system prompt caching and time injection
type PromptCacheConfig struct {
	PollInterval       int  `json:"pollInterval" default:"60"`        // Hash poll interval in seconds (0 = disabled)
	TimeInSystemPrompt bool `json:"timeInSystemPrompt"`               // Include time in system prompt
	TimeInUserMessage  bool `json:"timeInUserMessage" default:"true"` // Prefix latest user message with timestamp
	ShowUptime         bool `json:"showUptime" default:"true"`        // Include gateway uptime with time
}

// AgentIdentityConfig configures the agent's display identity
type AgentIdentityConfig struct {
	Name   string `json:"name" default:"GoClaw"` // Agent's display name
	Emoji  string `json:"emoji" default:"🐾"`     // Optional emoji prefix
	Typing string `json:"typing"`                // Custom typing indicator text (derived from Name if empty)
}

// DisplayName returns the agent name with emoji prefix if configured
func (c *AgentIdentityConfig) DisplayName() string {
	if c.Emoji != "" {
		return c.Emoji + " " + c.Name
	}
	return c.Name
}

// TypingText returns the typing indicator text
func (c *AgentIdentityConfig) TypingText() string {
	if c.Typing != "" {
		return c.Typing
	}
	return c.Name + " is typing..."
}

// SupervisionConfig configures supervisor interactions with the agent
type SupervisionConfig struct {
	Guidance     GuidanceConfig     `json:"guidance"`
	Ghostwriting GhostwritingConfig `json:"ghostwriting"`
}

// GuidanceConfig configures supervisor guidance injection
type GuidanceConfig struct {
	Prefix     string `json:"prefix" default:"[Supervisor]: "` // Prefix prepended to guidance messages
	SystemNote string `json:"systemNote,omitempty"`            // Optional system message injected with guidance
}

// GhostwritingConfig configures supervisor ghostwriting
type GhostwritingConfig struct {
	TypingDelayMs int `json:"typingDelayMs" default:"500"` // Delay before delivering the message
}

// SafetyConfig configures emergency stop / panic phrase behavior
type SafetyConfig struct {
	PanicPhrases    []string `json:"panicPhrases"`                   // Words that trigger emergency stop (GetPanicPhrases has fallback)
	PanicEnabled    bool     `json:"panicEnabled" default:"true"`    // Whether panic phrase detection is active
	ShutdownPhrases []string `json:"shutdownPhrases"`                // Phrases that trigger graceful shutdown (owner-only)
	ShutdownEnabled bool     `json:"shutdownEnabled" default:"true"` // Whether shutdown phrase detection is active
}

// GetPanicPhrases returns configured panic phrases with fallback default
func (c *SafetyConfig) GetPanicPhrases() []string {
	if len(c.PanicPhrases) > 0 {
		return c.PanicPhrases
	}
	return []string{"STOP", "STOP NOW"}
}

// GetShutdownPhrases returns configured shutdown phrases with fallback default.
func (c *SafetyConfig) GetShutdownPhrases() []string {
	if len(c.ShutdownPhrases) > 0 {
		return c.ShutdownPhrases
	}
	return []string{"SHUTDOWN NOW"}
}

// SecurityConfig configures security policies for the gateway
type SecurityConfig struct {
	ToolRestrictions map[string]ToolRestriction `json:"toolRestrictions,omitempty"`
}

// ToolRestriction defines which tools are denied for a given purpose
type ToolRestriction struct {
	Deny []string `json:"deny"`
}
