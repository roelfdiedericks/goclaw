package voicellm

import (
	"fmt"
	"sync"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Global registry singleton
var (
	globalRegistry *Registry
	globalMu       sync.RWMutex
)

// SetGlobalRegistry sets the global registry instance (called once at startup)
func SetGlobalRegistry(r *Registry) {
	globalMu.Lock()
	defer globalMu.Unlock()
	globalRegistry = r
}

// GetRegistry returns the global registry instance
func GetRegistry() *Registry {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalRegistry
}

// Factory creates a new VoiceLLM provider instance from config
type Factory func(name string, cfg ProviderConfig) (Provider, error)

// Registry manages VoiceLLM provider factories and configurations.
// Unlike the text LLM registry, this creates new provider instances per voice session
// since each session maintains its own WebSocket connection.
type Registry struct {
	factories map[string]Factory        // driver -> factory (e.g., "xai" -> NewXAIProvider)
	configs   map[string]ProviderConfig // provider name -> config
	dflt      string                    // default provider name
	globalCfg Config                    // global voicellm config
	mu        sync.RWMutex
}

// NewRegistry creates a new VoiceLLM registry from configuration
func NewRegistry(cfg Config) (*Registry, error) {
	r := &Registry{
		factories: make(map[string]Factory),
		configs:   make(map[string]ProviderConfig),
		dflt:      cfg.Default,
		globalCfg: cfg,
	}

	// Register built-in factories
	r.RegisterFactory("xai", NewXAIProvider)

	// Store provider configs
	for name, provCfg := range cfg.Providers {
		r.configs[name] = provCfg
		L_debug("voicellm: provider configured", "name", name, "driver", provCfg.Driver)
	}

	// Validate default provider exists
	if cfg.Default != "" {
		if _, ok := r.configs[cfg.Default]; !ok {
			return nil, fmt.Errorf("voicellm: default provider %q not found in configured providers", cfg.Default)
		}
	}

	L_info("voicellm: registry created",
		"providers", len(r.configs),
		"default", cfg.Default,
		"enabled", cfg.Enabled)

	return r, nil
}

// RegisterFactory registers a factory function for a driver type
func (r *Registry) RegisterFactory(driver string, factory Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[driver] = factory
	L_debug("voicellm: factory registered", "driver", driver)
}

// CreateSession creates a new voice provider instance for a session.
// Each voice session gets its own provider instance with a dedicated WebSocket.
// If providerName is empty, uses the default provider.
func (r *Registry) CreateSession(providerName string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if providerName == "" {
		providerName = r.dflt
	}

	if providerName == "" {
		return nil, fmt.Errorf("voicellm: no provider specified and no default configured")
	}

	cfg, ok := r.configs[providerName]
	if !ok {
		return nil, fmt.Errorf("voicellm: unknown provider %q", providerName)
	}

	factory, ok := r.factories[cfg.Driver]
	if !ok {
		return nil, fmt.Errorf("voicellm: unknown driver %q for provider %q", cfg.Driver, providerName)
	}

	provider, err := factory(providerName, cfg)
	if err != nil {
		return nil, fmt.Errorf("voicellm: failed to create provider %q: %w", providerName, err)
	}

	L_debug("voicellm: session created", "provider", providerName, "driver", cfg.Driver)
	return provider, nil
}

// GetDefaultProvider returns the name of the default provider
func (r *Registry) GetDefaultProvider() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.dflt
}

// GetConfig returns the global VoiceLLM configuration
func (r *Registry) GetConfig() Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.globalCfg
}

// IsEnabled returns whether VoiceLLM is enabled
func (r *Registry) IsEnabled() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.globalCfg.Enabled && len(r.configs) > 0
}

// ListProviders returns names of all configured providers
func (r *Registry) ListProviders() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.configs))
	for name := range r.configs {
		names = append(names, name)
	}
	return names
}

// GetPromptConfig returns the prompt configuration
func (r *Registry) GetPromptConfig() PromptConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.globalCfg.Prompt
}
