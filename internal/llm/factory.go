// Package llm - Provider factory
package llm

import "fmt"

// NewProvider creates a provider instance from config.
// Dispatches to the appropriate constructor based on cfg.Driver.
// Used by registry.initProvider() and for standalone testing in the editor.
func NewProvider(name string, cfg LLMProviderConfig) (Provider, error) {
	desc, ok := GetDriver(cfg.Driver)
	if !ok {
		return nil, fmt.Errorf("unknown provider driver: %s", cfg.Driver)
	}
	return desc.New(name, cfg)
}
