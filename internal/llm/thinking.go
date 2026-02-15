// Package llm provides LLM client implementations.
package llm

import "github.com/roelfdiedericks/xai-go"

// ThinkingLevel represents the effort level for extended thinking/reasoning.
// This is a universal abstraction that gets mapped to provider-specific parameters.
type ThinkingLevel string

const (
	// ThinkingOff disables extended thinking completely
	ThinkingOff ThinkingLevel = "off"

	// ThinkingMinimal uses minimal reasoning effort (quick responses)
	ThinkingMinimal ThinkingLevel = "minimal"

	// ThinkingLow uses low reasoning effort
	ThinkingLow ThinkingLevel = "low"

	// ThinkingMedium uses medium reasoning effort (default)
	ThinkingMedium ThinkingLevel = "medium"

	// ThinkingHigh uses high reasoning effort
	ThinkingHigh ThinkingLevel = "high"

	// ThinkingXHigh uses maximum reasoning effort
	// Note: May not be supported by all providers/models
	ThinkingXHigh ThinkingLevel = "xhigh"
)

// DefaultThinkingLevel is the default level when not specified
const DefaultThinkingLevel = ThinkingMedium

// ValidThinkingLevels contains all valid thinking level values
var ValidThinkingLevels = []ThinkingLevel{
	ThinkingOff,
	ThinkingMinimal,
	ThinkingLow,
	ThinkingMedium,
	ThinkingHigh,
	ThinkingXHigh,
}

// IsValidThinkingLevel checks if a string is a valid thinking level
func IsValidThinkingLevel(level string) bool {
	for _, valid := range ValidThinkingLevels {
		if ThinkingLevel(level) == valid {
			return true
		}
	}
	return false
}

// ParseThinkingLevel converts a string to ThinkingLevel, returning the default if invalid.
func ParseThinkingLevel(level string) ThinkingLevel {
	if level == "" {
		return DefaultThinkingLevel
	}
	if IsValidThinkingLevel(level) {
		return ThinkingLevel(level)
	}
	return DefaultThinkingLevel
}

// IsEnabled returns true if thinking is enabled (level is not "off")
func (l ThinkingLevel) IsEnabled() bool {
	return l != ThinkingOff && l != ""
}

// String returns the string representation
func (l ThinkingLevel) String() string {
	return string(l)
}

// OpenRouterEffort maps ThinkingLevel to OpenRouter's reasoning.effort parameter.
// OpenRouter supports: "low", "medium", "high"
func (l ThinkingLevel) OpenRouterEffort() string {
	switch l {
	case ThinkingOff:
		return "" // No reasoning
	case ThinkingMinimal, ThinkingLow:
		return "low"
	case ThinkingMedium:
		return "medium"
	case ThinkingHigh, ThinkingXHigh:
		return "high"
	default:
		return "medium"
	}
}

// AnthropicBudgetTokens maps ThinkingLevel to Anthropic's thinking.budget_tokens.
// Anthropic uses token budgets: min 1024, recommended based on complexity.
// Returns 0 for "off" (no thinking), otherwise returns suggested budget.
func (l ThinkingLevel) AnthropicBudgetTokens() int {
	switch l {
	case ThinkingOff:
		return 0
	case ThinkingMinimal:
		return 1024 // Minimum allowed
	case ThinkingLow:
		return 4096
	case ThinkingMedium:
		return 10000
	case ThinkingHigh:
		return 25000
	case ThinkingXHigh:
		return 50000 // Large budget for complex tasks
	default:
		return 10000 // Default to medium
	}
}

// DeepSeekEffort maps ThinkingLevel to DeepSeek's reasoning effort.
// DeepSeek R1 uses similar parameters to OpenRouter.
func (l ThinkingLevel) DeepSeekEffort() string {
	// DeepSeek uses same values as OpenRouter
	return l.OpenRouterEffort()
}

// KimiEffort maps ThinkingLevel to Kimi's reasoning effort.
// Kimi models (k1.5, etc.) support reasoning with similar effort levels.
func (l ThinkingLevel) KimiEffort() string {
	// Kimi uses same values as OpenRouter
	return l.OpenRouterEffort()
}

// XAIEffort maps ThinkingLevel to xAI's ReasoningEffort.
// Returns nil for "off" (no reasoning), otherwise returns Low/Medium/High.
func (l ThinkingLevel) XAIEffort() *xai.ReasoningEffort {
	switch l {
	case ThinkingOff:
		return nil // No reasoning
	case ThinkingMinimal, ThinkingLow:
		effort := xai.ReasoningEffortLow
		return &effort
	case ThinkingMedium:
		effort := xai.ReasoningEffortMedium
		return &effort
	case ThinkingHigh, ThinkingXHigh:
		effort := xai.ReasoningEffortHigh
		return &effort
	default:
		effort := xai.ReasoningEffortMedium
		return &effort
	}
}

// ThinkingLevelFromBool converts legacy boolean EnableThinking to ThinkingLevel.
// Used for backward compatibility with existing code.
func ThinkingLevelFromBool(enable bool) ThinkingLevel {
	if enable {
		return DefaultThinkingLevel
	}
	return ThinkingOff
}
