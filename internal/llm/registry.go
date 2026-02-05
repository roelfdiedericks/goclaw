// Package llm provides unified LLM provider interfaces and implementations.
package llm

import (
	"fmt"
	"strings"
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

// Registry manages LLM provider instances and model resolution.
// It supports multiple provider instances and purpose-based model selection.
type Registry struct {
	providers map[string]providerInstance // provider name -> instance
	purposes  map[string]PurposeConfig    // purpose -> config with models array
	mu        sync.RWMutex
}

// providerInstance holds a provider and its config
type providerInstance struct {
	config   ProviderConfig
	provider interface{} // *AnthropicProvider or *OllamaProvider
}

// PurposeConfig defines the model chain for a specific purpose
type PurposeConfig struct {
	Models    []string `json:"models"`    // First = primary, rest = fallbacks
	MaxTokens int      `json:"maxTokens"` // Output limit override (0 = use model default)
}

// RegistryConfig is the configuration for the LLM registry
type RegistryConfig struct {
	Providers     map[string]ProviderConfig `json:"providers"`
	Agent         PurposeConfig             `json:"agent"`
	Summarization PurposeConfig             `json:"summarization"`
	Embeddings    PurposeConfig             `json:"embeddings"`
}

// NewRegistry creates a new provider registry from configuration
func NewRegistry(cfg RegistryConfig) (*Registry, error) {
	r := &Registry{
		providers: make(map[string]providerInstance),
		purposes: map[string]PurposeConfig{
			"agent":         cfg.Agent,
			"summarization": cfg.Summarization,
			"embeddings":    cfg.Embeddings,
		},
	}

	// Initialize all providers (but don't connect models yet)
	for name, provCfg := range cfg.Providers {
		if err := r.initProvider(name, provCfg); err != nil {
			return nil, fmt.Errorf("provider %s: %w", name, err)
		}
	}

	L_info("llm: registry created",
		"providers", len(r.providers),
		"agentModels", len(cfg.Agent.Models),
		"summarizationModels", len(cfg.Summarization.Models),
		"embeddingModels", len(cfg.Embeddings.Models))

	return r, nil
}

// initProvider initializes a provider instance
func (r *Registry) initProvider(name string, cfg ProviderConfig) error {
	var provider interface{}
	var err error

	switch cfg.Type {
	case "anthropic":
		provider, err = NewAnthropicProvider(name, cfg)
	case "ollama":
		provider, err = NewOllamaProvider(name, cfg)
	case "openai":
		provider, err = NewOpenAIProvider(name, cfg)
	default:
		return fmt.Errorf("unknown provider type: %s", cfg.Type)
	}

	if err != nil {
		return err
	}

	r.providers[name] = providerInstance{
		config:   cfg,
		provider: provider,
	}

	L_debug("llm: provider initialized", "name", name, "type", cfg.Type)
	return nil
}

// GetProvider returns the first available provider for a purpose.
// Iterates through the model chain until one is available.
// Also applies maxTokens override from PurposeConfig if set.
func (r *Registry) GetProvider(purpose string) (Provider, error) {
	r.mu.RLock()
	cfg, ok := r.purposes[purpose]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown purpose: %s", purpose)
	}

	if len(cfg.Models) == 0 {
		return nil, fmt.Errorf("no models configured for purpose: %s", purpose)
	}

	for i, ref := range cfg.Models {
		provider, err := r.resolveForPurpose(ref, purpose)
		if err != nil {
			L_debug("llm: failed to resolve model", "ref", ref, "error", err)
			continue
		}

		// Check availability and apply maxTokens override
		var result Provider
		switch p := provider.(type) {
		case *AnthropicProvider:
			if !p.IsAvailable() {
				continue
			}
			if cfg.MaxTokens > 0 {
				result = p.WithMaxTokens(cfg.MaxTokens)
			} else {
				result = p
			}
		case *OllamaProvider:
			if !p.IsAvailable() {
				continue
			}
			if cfg.MaxTokens > 0 {
				result = p.WithMaxTokens(cfg.MaxTokens)
			} else {
				result = p
			}
		case *OpenAIProvider:
			if !p.IsAvailable() {
				continue
			}
			if cfg.MaxTokens > 0 {
				result = p.WithMaxTokens(cfg.MaxTokens)
			} else {
				result = p
			}
		default:
			L_warn("llm: unknown provider type", "purpose", purpose, "ref", ref)
			continue
		}

		if i > 0 {
			// Log fallback event when not using primary
			L_info("llm: using fallback", "purpose", purpose, "model", ref, "position", i+1)
		}
		L_debug("llm: provider selected", "purpose", purpose, "ref", ref)
		return result, nil
	}

	return nil, fmt.Errorf("no available provider for %s (tried: %v)", purpose, cfg.Models)
}

// Resolve returns a provider for a specific model reference, no fallback chain.
// Format: "provider-alias/model-name" (e.g., "anthropic/claude-opus-4-5")
//
// Future use: Enables per-session model selection via /model command.
// Users can select a specific model from the agent chain to use cheaper
// models for basic chat. The gateway would check session.PreferredAgentModel
// first, resolve it directly, then fall back to GetProvider("agent") if
// the preferred model is unavailable.
func (r *Registry) Resolve(ref string) (interface{}, error) {
	return r.resolve(ref)
}

// resolve parses a model reference and returns the configured provider
func (r *Registry) resolve(ref string) (interface{}, error) {
	return r.resolveForPurpose(ref, "")
}

// resolveForPurpose parses a model reference with purpose context
func (r *Registry) resolveForPurpose(ref, purpose string) (interface{}, error) {
	// Parse "provider/model" format
	parts := strings.SplitN(ref, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid model reference: %s (expected provider/model)", ref)
	}

	providerName := parts[0]
	modelName := parts[1]

	r.mu.RLock()
	instance, ok := r.providers[providerName]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", providerName)
	}

	// Clone provider with specific model
	switch p := instance.provider.(type) {
	case *AnthropicProvider:
		return p.WithModel(modelName), nil
	case *OllamaProvider:
		// Use embedding-specific initialization for embeddings purpose
		if purpose == "embeddings" {
			return p.WithModelForEmbedding(modelName), nil
		}
		return p.WithModel(modelName), nil
	case *OpenAIProvider:
		// Use embedding-specific initialization for embeddings purpose
		if purpose == "embeddings" {
			return p.WithModelForEmbedding(modelName), nil
		}
		return p.WithModel(modelName), nil
	default:
		return nil, fmt.Errorf("provider %s has unexpected type", providerName)
	}
}

// GetAnthropicProvider returns an Anthropic provider for a purpose (typed helper)
func (r *Registry) GetAnthropicProvider(purpose string) (*AnthropicProvider, error) {
	provider, err := r.GetProvider(purpose)
	if err != nil {
		return nil, err
	}
	p, ok := provider.(*AnthropicProvider)
	if !ok {
		return nil, fmt.Errorf("provider for %s is not Anthropic", purpose)
	}
	return p, nil
}

// GetOllamaProvider returns an Ollama provider for a purpose (typed helper)
func (r *Registry) GetOllamaProvider(purpose string) (*OllamaProvider, error) {
	provider, err := r.GetProvider(purpose)
	if err != nil {
		return nil, err
	}
	p, ok := provider.(*OllamaProvider)
	if !ok {
		return nil, fmt.Errorf("provider for %s is not Ollama", purpose)
	}
	return p, nil
}

// GetOpenAIProvider returns an OpenAI provider for a purpose (typed helper)
func (r *Registry) GetOpenAIProvider(purpose string) (*OpenAIProvider, error) {
	provider, err := r.GetProvider(purpose)
	if err != nil {
		return nil, err
	}
	p, ok := provider.(*OpenAIProvider)
	if !ok {
		return nil, fmt.Errorf("provider for %s is not OpenAI", purpose)
	}
	return p, nil
}

// ListProviders returns the names of all configured providers
func (r *Registry) ListProviders() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}

// ListModelsForPurpose returns the model chain for a purpose
func (r *Registry) ListModelsForPurpose(purpose string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if cfg, ok := r.purposes[purpose]; ok {
		return cfg.Models
	}
	return nil
}
