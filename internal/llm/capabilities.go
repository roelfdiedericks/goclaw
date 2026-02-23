// Package llm - Provider capability detection
package llm

import "strings"

// SupportsVision returns true if the provider/model supports image input in user messages.
// This is a conservative check - models not explicitly known are assumed to NOT support vision.
func SupportsVision(p Provider) bool {
	providerType := p.Type()
	model := strings.ToLower(p.Model())

	switch providerType {
	case "anthropic":
		// Claude 3+ models support vision
		// claude-3-opus, claude-3-sonnet, claude-3-haiku
		// claude-3-5-sonnet, claude-3-5-haiku
		// claude-sonnet-4, claude-opus-4
		if strings.Contains(model, "claude-3") ||
			strings.Contains(model, "claude-sonnet") ||
			strings.Contains(model, "claude-opus") ||
			strings.Contains(model, "claude-haiku") {
			return true
		}

	case "openai":
		// GPT-4V, GPT-4o, GPT-4-turbo with vision
		if strings.Contains(model, "gpt-4o") ||
			strings.Contains(model, "gpt-4-turbo") ||
			strings.Contains(model, "gpt-4-vision") ||
			strings.Contains(model, "gpt-4v") ||
			strings.Contains(model, "o1") ||
			strings.Contains(model, "o3") ||
			strings.Contains(model, "o4") {
			return true
		}

	case "xai":
		// Grok models - grok-2-vision, grok-vision-beta, etc.
		if strings.Contains(model, "vision") ||
			strings.Contains(model, "grok-2") ||
			strings.Contains(model, "grok-3") ||
			strings.Contains(model, "grok-4") {
			return true
		}

	case "ollama":
		// Ollama vision models: llava, bakllava, moondream, etc.
		if strings.Contains(model, "llava") ||
			strings.Contains(model, "bakllava") ||
			strings.Contains(model, "moondream") ||
			strings.Contains(model, "vision") {
			return true
		}
	}

	return false
}

// SupportsToolResultImages returns true if the provider/model can handle images
// embedded in tool results (not just user messages).
// This is more restrictive - not all vision models support this.
func SupportsToolResultImages(p Provider) bool {
	providerType := p.Type()
	model := strings.ToLower(p.Model())

	switch providerType {
	case "anthropic":
		// Anthropic supports images in tool results for Claude 3+ models
		if strings.Contains(model, "claude-3") ||
			strings.Contains(model, "claude-sonnet") ||
			strings.Contains(model, "claude-opus") ||
			strings.Contains(model, "claude-haiku") {
			return true
		}

	case "openai":
		// OpenAI GPT-4o and later support images in tool results
		if strings.Contains(model, "gpt-4o") ||
			strings.Contains(model, "o1") ||
			strings.Contains(model, "o3") ||
			strings.Contains(model, "o4") {
			return true
		}

	case "xai":
		// xAI Grok 2+ with vision capability
		if strings.Contains(model, "grok-2") ||
			strings.Contains(model, "grok-3") ||
			strings.Contains(model, "grok-4") {
			return true
		}
	}

	// Conservative default: Ollama and unknown providers don't support tool result images
	return false
}
