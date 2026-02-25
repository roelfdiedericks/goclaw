// Package llm - LLM configuration types
//
// This file contains the canonical configuration types for LLM providers and purposes.
// These types are imported by config/config.go via type aliases.
package llm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// LLMConfig contains LLM provider settings.
// Providers are aliased instances; models reference them via "alias/model" format.
type LLMConfig struct {
	Providers     map[string]LLMProviderConfig `json:"providers"`
	Agent         LLMPurposeConfig             `json:"agent"`         // Main chat
	Summarization LLMPurposeConfig             `json:"summarization"` // Checkpoint/compaction
	Embeddings    LLMPurposeConfig             `json:"embeddings"`    // Memory/transcript
	Thinking      ThinkingConfig               `json:"thinking"`      // Extended thinking settings
	SystemPrompt  string                       `json:"systemPrompt"`  // System prompt for agent
}

// ThinkingConfig configures extended thinking for models that support it
type ThinkingConfig struct {
	BudgetTokens int    `json:"budgetTokens"` // Token budget for thinking (default: 10000) - legacy, kept for compatibility
	DefaultLevel string `json:"defaultLevel"` // Global default level: off/minimal/low/medium/high/xhigh (default: "medium")
}

// LLMProviderConfig is the configuration for a single provider instance.
// This is the canonical type used by both config loading and the LLM registry.
type LLMProviderConfig struct {
	Driver         string `json:"driver"`                    // "anthropic", "openai", "ollama", "xai"
	Subtype        string `json:"subtype,omitempty"`        // Hint for UI: "openrouter", "lmstudio", etc.
	APIKey         string `json:"apiKey,omitempty"`         // For cloud providers
	BaseURL        string `json:"baseURL,omitempty"`        // For OpenAI-compatible endpoints
	URL            string `json:"url,omitempty"`            // For Ollama
	MaxTokens      int    `json:"maxTokens,omitempty"`      // Default output limit
	ContextTokens  int    `json:"contextTokens,omitempty"`  // Context window override (0 = auto-detect)
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"` // Request timeout
	PromptCaching  bool   `json:"promptCaching,omitempty"`  // Anthropic-specific
	EmbeddingOnly  bool   `json:"embeddingOnly,omitempty"`  // For embedding-only models
	ThinkingLevel  string `json:"thinkingLevel,omitempty"`  // Default thinking level: off/minimal/low/medium/high/xhigh

	// Debug/Advanced
	Trace         *bool `json:"trace,omitempty"`         // Per-provider trace logging (nil = default enabled when -t flag used)
	DumpOnSuccess bool  `json:"dumpOnSuccess,omitempty"` // Keep request dumps even on success (for debugging)

	// xAI-specific fields
	ServerToolsAllowed []string `json:"serverToolsAllowed,omitempty"` // xAI server-side tools to enable (empty = all known tools)
	MaxTurns           int      `json:"maxTurns,omitempty"`           // xAI max agentic turns (0 = xai-go default)
	IncrementalContext *bool    `json:"incrementalContext,omitempty"` // xAI: chain context, send only new messages (nil = true)
	KeepaliveTime      int      `json:"keepaliveTime,omitempty"`      // xAI gRPC keepalive time in seconds (0 = xai-go default)
	KeepaliveTimeout   int      `json:"keepaliveTimeout,omitempty"`   // xAI gRPC keepalive timeout in seconds (0 = xai-go default)
}

// LLMPurposeConfig defines the model chain for a specific purpose (agent, summarization, embeddings).
type LLMPurposeConfig struct {
	Models         []string `json:"models"`                   // First = primary, rest = fallbacks
	MaxInputTokens int      `json:"maxInputTokens,omitempty"` // Input limit for summarization (0 = use model context - buffer)
	AutoRebuild    *bool    `json:"autoRebuild,omitempty"`    // Embeddings: auto-rebuild on model mismatch (default: true)
}

// GetAutoRebuild returns the AutoRebuild setting, defaulting to true if not set
func (c *LLMPurposeConfig) GetAutoRebuild() bool {
	if c.AutoRebuild == nil {
		return true // Default: auto-rebuild enabled
	}
	return *c.AutoRebuild
}

// --- Form Definition ---

// ProviderConfigFormDef returns the form definition for editing a single LLM provider.
// subtypeOptions should be built from models.json providers matching the driver type.
func ProviderConfigFormDef(subtypeOptions []forms.Option) forms.FormDef {
	return forms.FormDef{
		Title:       "LLM Provider",
		Description: "Configure an LLM provider connection",
		Sections: []forms.Section{
			{
				Title: "Connection",
				Fields: []forms.Field{
					{
						Name:     "driver",
						Title:    "Driver",
						Desc:     "The LLM driver (anthropic, openai, ollama, xai)",
						Type:     forms.Select,
						Required: true,
						Options: []forms.Option{
							{Label: "Anthropic", Value: "anthropic"},
							{Label: "OpenAI Compatible", Value: "openai"},
							{Label: "Ollama", Value: "ollama"},
							{Label: "xAI", Value: "xai"},
						},
					},
					{
						Name:    "subtype",
						Title:   "Subtype",
						Desc:    "The specific provider for this connection",
						Type:    forms.Select,
						Options: subtypeOptions,
					},
					{
						Name:  "apiKey",
						Title: "API Key",
						Desc:  "API key for authentication",
						Type:  forms.Secret,
					},
					{
						Name:  "baseURL",
						Title: "Base URL",
						Desc:  "API endpoint (for OpenAI-compatible)",
						Type:  forms.Text,
					},
					{
						Name:  "url",
						Title: "URL",
						Desc:  "Ollama server URL",
						Type:  forms.Text,
					},
				},
			},
			{
				Title: "Model Defaults",
				Fields: []forms.Field{
					{
						Name:  "maxTokens",
						Title: "Max Output Tokens",
						Desc:  "Default output token limit (0 = model default)",
						Type:  forms.Number,
						Min:   0,
						Max:   100000,
					},
					{
						Name:  "contextTokens",
						Title: "Context Window Override",
						Desc:  "Override context window size (0 = auto-detect)",
						Type:  forms.Number,
						Min:   0,
						Max:   2000000,
					},
					{
						Name:  "timeoutSeconds",
						Title: "Timeout (seconds)",
						Desc:  "Request timeout (0 = default)",
						Type:  forms.Number,
						Min:   0,
						Max:   3600,
					},
				},
			},
			{
				Title: "Features",
				Fields: []forms.Field{
					{
						Name:  "promptCaching",
						Title: "Prompt Caching",
						Desc:  "Enable Anthropic prompt caching",
						Type:  forms.Toggle,
					},
					{
						Name:  "embeddingOnly",
						Title: "Embedding Only",
						Desc:  "Provider only supports embeddings",
						Type:  forms.Toggle,
					},
					{
						Name:  "thinkingLevel",
						Title: "Thinking Level",
						Desc:  "Default extended thinking level",
						Type:  forms.Select,
						Options: []forms.Option{
							{Label: "Off", Value: "off"},
							{Label: "Minimal", Value: "minimal"},
							{Label: "Low", Value: "low"},
							{Label: "Medium", Value: "medium"},
							{Label: "High", Value: "high"},
							{Label: "Extra High", Value: "xhigh"},
						},
					},
				},
			},
			{
				Title:     "Advanced",
				Collapsed: true,
				Fields: []forms.Field{
					{
						Name:  "trace",
						Title: "Trace Logging",
						Desc:  "Enable detailed request/response logging",
						Type:  forms.Select,
						Options: []forms.Option{
							{Label: "Default (enabled with -t flag)", Value: "default"},
							{Label: "Always Enabled", Value: "true"},
							{Label: "Always Disabled", Value: "false"},
						},
					},
					{
						Name:  "dumpOnSuccess",
						Title: "Dump on Success",
						Desc:  "Keep request dumps even on success",
						Type:  forms.Toggle,
					},
				},
			},
			{
				Title:     "xAI Advanced",
				ShowWhen:  "driver=xai",
				Collapsed: true,
				Fields: []forms.Field{
					{
						Name:  "serverToolsAllowed",
						Title: "Server Tools",
						Desc:  "xAI server-side tools to enable (empty = all)",
						Type:  forms.StringList,
					},
					{
						Name:  "maxTurns",
						Title: "Max Turns",
						Desc:  "Max agentic turns (0 = default)",
						Type:  forms.Number,
						Min:   0,
						Max:   100,
					},
					{
						Name:  "incrementalContext",
						Title: "Incremental Context",
						Desc:  "Chain context, send only new messages",
						Type:  forms.Select,
						Options: []forms.Option{
							{Label: "Default (enabled)", Value: "default"},
							{Label: "Enabled", Value: "true"},
							{Label: "Disabled", Value: "false"},
						},
					},
					{
						Name:  "keepaliveTime",
						Title: "Keepalive Time",
						Desc:  "gRPC keepalive time in seconds (0 = default)",
						Type:  forms.Number,
						Min:   0,
						Max:   3600,
					},
					{
						Name:  "keepaliveTimeout",
						Title: "Keepalive Timeout",
						Desc:  "gRPC keepalive timeout in seconds (0 = default)",
						Type:  forms.Number,
						Min:   0,
						Max:   3600,
					},
				},
			},
		},
		Actions: []forms.ActionDef{
			{
				Name:  "test",
				Label: "Test Connection",
				Desc:  "Verify credentials and connectivity",
			},
			{
				Name:  "listModels",
				Label: "List Models",
				Desc:  "Fetch available models from provider",
			},
		},
	}
}

// --- Command Handlers ---

// RegisterCommands registers LLM config command handlers
func RegisterCommands() {
	bus.RegisterCommand("llm", "test", handleTestConnection)
	bus.RegisterCommand("llm", "listModels", handleListModels)
	bus.RegisterCommand("llm", "apply", handleApply)
}

// UnregisterCommands removes LLM config command handlers
func UnregisterCommands() {
	bus.UnregisterCommand("llm", "test")
	bus.UnregisterCommand("llm", "listModels")
	bus.UnregisterCommand("llm", "apply")
}

// handleTestConnection tests connectivity to the configured provider
func handleTestConnection(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(*LLMProviderConfig)
	if !ok {
		return bus.CommandResult{
			Error:   fmt.Errorf("invalid payload type"),
			Message: "Internal error: invalid config type",
		}
	}

	if cfg.Driver == "" {
		return bus.CommandResult{
			Error:   fmt.Errorf("provider driver is required"),
			Message: "Select a provider driver first",
		}
	}

	provider, err := NewProvider("test", *cfg)
	if err != nil {
		return bus.CommandResult{
			Error:   err,
			Message: fmt.Sprintf("Failed to create provider: %s", err),
		}
	}

	tester, ok := provider.(ConnectionTester)
	if !ok {
		return bus.CommandResult{
			Error:   fmt.Errorf("provider does not support connection testing"),
			Message: "This provider does not support connection testing",
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := tester.TestConnection(ctx); err != nil {
		L_warn("llm: test connection failed", "driver", cfg.Driver, "error", err)
		return bus.CommandResult{
			Error:   err,
			Message: fmt.Sprintf("Connection failed: %s", err),
		}
	}

	L_info("llm: test connection successful", "driver", cfg.Driver)
	return bus.CommandResult{
		Success: true,
		Message: "Connection successful",
	}
}

// handleListModels fetches available models from the provider
func handleListModels(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(*LLMProviderConfig)
	if !ok {
		return bus.CommandResult{
			Error:   fmt.Errorf("invalid payload type"),
			Message: "Internal error: invalid config type",
		}
	}

	if cfg.Driver == "" {
		return bus.CommandResult{
			Error:   fmt.Errorf("provider driver is required"),
			Message: "Select a provider driver first",
		}
	}

	provider, err := NewProvider("test", *cfg)
	if err != nil {
		return bus.CommandResult{
			Error:   err,
			Message: fmt.Sprintf("Failed to create provider: %s", err),
		}
	}

	lister, ok := provider.(ModelLister)
	if !ok {
		return bus.CommandResult{
			Error:   fmt.Errorf("provider does not support model listing"),
			Message: "This provider does not support model listing",
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	models, err := lister.ListModels(ctx)
	if err != nil {
		L_warn("llm: list models failed", "driver", cfg.Driver, "error", err)
		return bus.CommandResult{
			Error:   err,
			Message: fmt.Sprintf("Failed to list models: %s", err),
		}
	}

	// Format model list for display
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d models:\n", len(models)))
	for i, m := range models {
		if i >= 20 {
			sb.WriteString(fmt.Sprintf("... and %d more\n", len(models)-20))
			break
		}
		if m.ContextTokens > 0 {
			sb.WriteString(fmt.Sprintf("  %s (%dk context)\n", m.ID, m.ContextTokens/1000))
		} else {
			sb.WriteString(fmt.Sprintf("  %s\n", m.ID))
		}
	}

	L_info("llm: list models successful", "driver", cfg.Driver, "count", len(models))
	return bus.CommandResult{
		Success: true,
		Message: sb.String(),
		Data:    models,
	}
}

// handleApply rebuilds the LLM registry with new configuration and notifies subscribers
func handleApply(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(*LLMConfig)
	if !ok {
		return bus.CommandResult{
			Error:   fmt.Errorf("invalid payload type: expected *LLMConfig, got %T", cmd.Payload),
			Message: "Internal error: invalid config type",
		}
	}

	// Convert LLMConfig to RegistryConfig (subset of fields)
	regCfg := RegistryConfig{
		Providers:     cfg.Providers,
		Agent:         cfg.Agent,
		Summarization: cfg.Summarization,
		Embeddings:    cfg.Embeddings,
	}

	// Create new registry
	newRegistry, err := NewRegistry(regCfg)
	if err != nil {
		L_error("llm: apply failed to create registry", "error", err)
		return bus.CommandResult{
			Error:   err,
			Message: fmt.Sprintf("Failed to create registry: %s", err),
		}
	}

	// Replace global registry
	SetGlobalRegistry(newRegistry)

	// Publish event for subscribers (transcript, memory, etc.)
	bus.PublishEvent("llm.config.applied", cfg)

	L_info("llm: config applied", "providers", len(cfg.Providers))
	return bus.CommandResult{
		Success: true,
		Message: fmt.Sprintf("LLM configuration applied (%d providers)", len(cfg.Providers)),
	}
}

// --- Purpose Form Definitions ---

// AgentPurposeFormDef returns the form for agent purpose configuration
func AgentPurposeFormDef() forms.FormDef {
	return forms.FormDef{
		Title:       "Agent Configuration",
		Description: "Configure the main chat model chain",
		Sections: []forms.Section{
			{
				Title: "Model Chain",
				Desc:  "Primary model and fallbacks (format: provider/model)",
				Fields: []forms.Field{
					{
						Name:  "models",
						Title: "Models",
						Desc:  "Comma-separated list: primary, fallback1, fallback2...",
						Type:  forms.StringList,
					},
				},
			},
		},
	}
}

// SummarizationPurposeFormDef returns the form for summarization purpose configuration
func SummarizationPurposeFormDef() forms.FormDef {
	return forms.FormDef{
		Title:       "Summarization Configuration",
		Description: "Configure the checkpoint/compaction model chain",
		Sections: []forms.Section{
			{
				Title: "Model Chain",
				Desc:  "Primary model and fallbacks (format: provider/model)",
				Fields: []forms.Field{
					{
						Name:  "models",
						Title: "Models",
						Desc:  "Comma-separated list: primary, fallback1, fallback2...",
						Type:  forms.StringList,
					},
				},
			},
			{
				Title: "Limits",
				Fields: []forms.Field{
					{
						Name:  "maxInputTokens",
						Title: "Max Input Tokens",
						Desc:  "Input limit for summarization (0 = context - buffer)",
						Type:  forms.Number,
						Min:   0,
						Max:   2000000,
					},
				},
			},
		},
	}
}

// EmbeddingsPurposeFormDef returns the form for embeddings purpose configuration
func EmbeddingsPurposeFormDef() forms.FormDef {
	return forms.FormDef{
		Title:       "Embeddings Configuration",
		Description: "Configure the embedding model chain",
		Sections: []forms.Section{
			{
				Title: "Model Chain",
				Desc:  "Primary model and fallbacks (format: provider/model)",
				Fields: []forms.Field{
					{
						Name:  "models",
						Title: "Models",
						Desc:  "Comma-separated list: primary, fallback1, fallback2...",
						Type:  forms.StringList,
					},
				},
			},
			{
				Title: "Options",
				Fields: []forms.Field{
					{
						Name:  "autoRebuild",
						Title: "Auto Rebuild",
						Desc:  "Automatically rebuild embeddings on model mismatch",
						Type:  forms.Select,
						Options: []forms.Option{
							{Label: "Default (enabled)", Value: "default"},
							{Label: "Enabled", Value: "true"},
							{Label: "Disabled", Value: "false"},
						},
					},
				},
			},
		},
	}
}

// ThinkingFormDef returns the form for thinking configuration
func ThinkingFormDef() forms.FormDef {
	return forms.FormDef{
		Title:       "Extended Thinking",
		Description: "Configure extended thinking defaults",
		Sections: []forms.Section{
			{
				Title: "Settings",
				Fields: []forms.Field{
					{
						Name:  "defaultLevel",
						Title: "Default Thinking Level",
						Desc:  "Global default for models that support extended thinking",
						Type:  forms.Select,
						Options: []forms.Option{
							{Label: "Off", Value: "off"},
							{Label: "Minimal", Value: "minimal"},
							{Label: "Low", Value: "low"},
							{Label: "Medium", Value: "medium"},
							{Label: "High", Value: "high"},
							{Label: "Extra High", Value: "xhigh"},
						},
					},
					{
						Name:  "budgetTokens",
						Title: "Budget Tokens (Legacy)",
						Desc:  "Token budget for thinking (legacy, kept for compatibility)",
						Type:  forms.Number,
						Min:   0,
						Max:   100000,
					},
				},
			},
		},
	}
}

// SystemPromptFormDef returns the form for system prompt editing
func SystemPromptFormDef() forms.FormDef {
	return forms.FormDef{
		Title:       "System Prompt",
		Description: "Edit the agent's system prompt",
		Sections: []forms.Section{
			{
				Title: "Prompt",
				Fields: []forms.Field{
					{
						Name:  "systemPrompt",
						Title: "System Prompt",
						Desc:  "The system prompt sent with every conversation",
						Type:  forms.TextArea,
					},
				},
			},
		},
	}
}
