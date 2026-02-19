// Package llm - Provider factory
package llm

import "fmt"

// NewProvider creates a provider instance from config.
// Dispatches to the appropriate constructor based on cfg.Type.
// Used by registry.initProvider() and for standalone testing in the editor.
func NewProvider(name string, cfg LLMProviderConfig) (Provider, error) {
	switch cfg.Type {
	case "anthropic":
		return NewAnthropicProvider(name, cfg)
	case "openai":
		return NewOpenAIProvider(name, cfg)
	case "ollama":
		return NewOllamaProvider(name, cfg)
	case "xai":
		return NewXAIProvider(name, cfg)
	default:
		return nil, fmt.Errorf("unknown provider type: %s", cfg.Type)
	}
}
