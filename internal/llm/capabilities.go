// Package llm - Provider capability detection
package llm

import (
	"strings"

	"github.com/roelfdiedericks/goclaw/internal/metadata"
)

// SupportsVision returns true if the provider/model supports image input in user messages.
// Checks models.json first, falls back to hardcoded patterns for unknown models.
func SupportsVision(p Provider) bool {
	if mp := p.MetadataProvider(); mp != "" {
		if model, ok := metadata.Get().GetModel(mp, p.Model()); ok {
			return model.Capabilities.Vision
		}
	}

	// Hardcoded fallback for models not in metadata
	providerType := p.Type()
	model := strings.ToLower(p.Model())

	switch providerType {
	case "anthropic":
		if strings.Contains(model, "claude-3") ||
			strings.Contains(model, "claude-sonnet") ||
			strings.Contains(model, "claude-opus") ||
			strings.Contains(model, "claude-haiku") {
			return true
		}
	case "openai":
		if strings.Contains(model, "gpt-4o") ||
			strings.Contains(model, "gpt-4-turbo") ||
			strings.Contains(model, "gpt-4-vision") ||
			strings.Contains(model, "gpt-4v") ||
			strings.Contains(model, "gpt-5") ||
			strings.Contains(model, "o1") ||
			strings.Contains(model, "o3") ||
			strings.Contains(model, "o4") {
			return true
		}
	case "xai":
		if strings.Contains(model, "vision") ||
			strings.Contains(model, "grok-4") {
			return true
		}
	case "ollama":
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
// Checks models.json vision + tool_use capabilities, falls back to hardcoded patterns.
func SupportsToolResultImages(p Provider) bool {
	if mp := p.MetadataProvider(); mp != "" {
		if model, ok := metadata.Get().GetModel(mp, p.Model()); ok {
			return model.Capabilities.Vision && model.Capabilities.ToolUse
		}
	}

	// Hardcoded fallback for models not in metadata
	providerType := p.Type()
	model := strings.ToLower(p.Model())

	switch providerType {
	case "anthropic":
		if strings.Contains(model, "claude-3") ||
			strings.Contains(model, "claude-sonnet") ||
			strings.Contains(model, "claude-opus") ||
			strings.Contains(model, "claude-haiku") {
			return true
		}
	case "openai":
		if strings.Contains(model, "gpt-4o") ||
			strings.Contains(model, "gpt-5") ||
			strings.Contains(model, "o1") ||
			strings.Contains(model, "o3") ||
			strings.Contains(model, "o4") {
			return true
		}
	case "xai":
		if strings.Contains(model, "grok-4") {
			return true
		}
	}

	return false
}
