// Package types defines gateway-owned configuration types that are shared across packages.
// These types are defined here to avoid import cycles between config and gateway packages.
package types

// GatewayConfig contains gateway server settings
type GatewayConfig struct {
	LogFile    string `json:"logFile"`
	PIDFile    string `json:"pidFile"`
	WorkingDir string `json:"workingDir"`
}

// PromptCacheConfig configures system prompt caching
type PromptCacheConfig struct {
	PollInterval int `json:"pollInterval"` // Hash poll interval in seconds (default: 60, 0 = disabled)
}

// AgentIdentityConfig configures the agent's display identity
type AgentIdentityConfig struct {
	Name   string `json:"name"`   // Agent's display name (default: "GoClaw")
	Emoji  string `json:"emoji"`  // Optional emoji prefix (default: "")
	Typing string `json:"typing"` // Custom typing indicator text (default: derived from Name)
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
	// Prefix prepended to guidance messages (default: "[Supervisor]: ")
	Prefix string `json:"prefix"`
	// SystemNote is an optional system message injected with guidance
	SystemNote string `json:"systemNote,omitempty"`
}

// GhostwritingConfig configures supervisor ghostwriting
type GhostwritingConfig struct {
	// TypingDelayMs is the delay before delivering the message (default: 500)
	TypingDelayMs int `json:"typingDelayMs"`
}
