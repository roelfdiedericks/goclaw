// Package llm provides unified LLM provider interfaces and implementations.
package llm

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/metadata"
	"github.com/roelfdiedericks/goclaw/internal/types"
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

// providerCooldown tracks cooldown state for a provider after errors
type providerCooldown struct {
	until      time.Time // When cooldown expires
	errorCount int       // Consecutive error count (for exponential backoff)
	reason     ErrorType // Why the provider is in cooldown
}

// ProviderStatus represents the current status of a provider for /llm command
type ProviderStatus struct {
	Alias      string
	InCooldown bool
	Until      time.Time
	Reason     ErrorType
	ErrorCount int
}

// Registry manages LLM provider instances and model resolution.
// It supports multiple provider instances and purpose-based model selection.
type Registry struct {
	providers  map[string]providerInstance  // provider name -> instance
	purposes   map[string]LLMPurposeConfig  // purpose -> config with models array
	cooldowns  map[string]*providerCooldown // provider alias -> cooldown state
	mu         sync.RWMutex
	cooldownMu sync.RWMutex
}

// providerInstance holds a provider and its config
type providerInstance struct {
	config   LLMProviderConfig
	provider interface{} // *AnthropicProvider or *OllamaProvider
}

// purposeCapReq defines capability requirements for a model purpose.
type purposeCapReq struct {
	required []string // missing = remove from chain
	warnOnly []string // missing = warn but keep
}

// purposeCapabilities maps purpose names to their required model capabilities.
// Capabilities are checked against models.json metadata at startup.
var purposeCapabilities = map[string]purposeCapReq{
	"agent": {
		required: []string{"tool_use"},
		warnOnly: []string{"vision"},
	},
}

// RegistryConfig is the configuration for the LLM registry
type RegistryConfig struct {
	Providers     map[string]LLMProviderConfig `json:"providers"`
	Agent         LLMPurposeConfig             `json:"agent"`
	Summarization LLMPurposeConfig             `json:"summarization"`
	Embeddings    LLMPurposeConfig             `json:"embeddings"`
	Heartbeat     LLMPurposeConfig             `json:"heartbeat,omitempty"`
	Cron          LLMPurposeConfig             `json:"cron,omitempty"`
	Hass          LLMPurposeConfig             `json:"hass,omitempty"`
}

// NewRegistry creates a new provider registry from configuration
func NewRegistry(cfg RegistryConfig) (*Registry, error) {
	r := &Registry{
		providers: make(map[string]providerInstance),
		purposes: map[string]LLMPurposeConfig{
			"agent":         cfg.Agent,
			"summarization": cfg.Summarization,
			"embeddings":    cfg.Embeddings,
			"heartbeat":     cfg.Heartbeat,
			"cron":          cfg.Cron,
			"hass":          cfg.Hass,
		},
		cooldowns: make(map[string]*providerCooldown),
	}

	// Initialize all providers (but don't connect models yet)
	for name, provCfg := range cfg.Providers {
		if err := r.initProvider(name, provCfg); err != nil {
			return nil, fmt.Errorf("provider %s: %w", name, err)
		}
	}

	// Validate models for all purposes (skip empty chains — they fall back to agent)
	for _, purpose := range []string{"agent", "summarization", "embeddings", "heartbeat", "cron", "hass"} {
		if len(r.purposes[purpose].Models) == 0 {
			continue
		}
		if err := r.validatePurposeModels(purpose); err != nil {
			return nil, err
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
func (r *Registry) initProvider(name string, cfg LLMProviderConfig) error {
	var provider interface{}
	var err error

	switch cfg.Driver {
	case "anthropic":
		provider, err = NewAnthropicProvider(name, cfg)
	case "ollama":
		provider, err = NewOllamaProvider(name, cfg)
	case "openai":
		provider, err = NewOpenAIProvider(name, cfg)
	case "xai":
		provider, err = NewXAIProvider(name, cfg)
	default:
		return fmt.Errorf("unknown provider driver: %s", cfg.Driver)
	}

	if err != nil {
		return err
	}

	r.providers[name] = providerInstance{
		config:   cfg,
		provider: provider,
	}

	L_debug("llm: provider initialized", "name", name, "driver", cfg.Driver)
	return nil
}

// validatePurposeModels checks each model ref against driver restrictions and
// metadata capability requirements. Models missing required capabilities for
// their purpose are removed from the chain. If the agent chain ends up empty,
// returns a fatal error since the gateway cannot function without it.
func (r *Registry) validatePurposeModels(purpose string) error {
	cfg := r.purposes[purpose]
	models := cfg.Models
	var kept []string
	var removed []string

	for _, ref := range models {
		parts := strings.SplitN(ref, "/", 2)
		if len(parts) != 2 {
			kept = append(kept, ref)
			continue
		}
		providerName := parts[0]
		modelName := parts[1]

		r.mu.RLock()
		instance, ok := r.providers[providerName]
		r.mu.RUnlock()

		if !ok {
			kept = append(kept, ref)
			continue
		}

		// Phase 1: Driver-specific validation (e.g. xAI model restrictions)
		if v, ok := instance.provider.(ModelValidator); ok {
			result := v.ValidateModel(modelName)
			if result != nil {
				if result.Fatal {
					return fmt.Errorf("model validation fatal: %s", result.Message)
				}
				L_warn("llm: driver validation failed, removing from chain",
					"purpose", purpose, "model", ref, "message", result.Message)
				removed = append(removed, fmt.Sprintf("%s: %s", ref, result.Message))
				continue
			}
		}

		// Phase 2: Metadata capability validation
		if reason := checkMetadataCapabilities(instance.config, modelName, purpose); reason != "" {
			L_warn("llm: model lacks required capabilities, removing from chain",
				"purpose", purpose, "model", ref, "reason", reason)
			removed = append(removed, fmt.Sprintf("%s: %s", ref, reason))
			continue
		}

		kept = append(kept, ref)
	}

	// Empty-chain guard
	if len(kept) == 0 && len(models) > 0 {
		if purpose == "agent" {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("no valid models remaining for %q purpose after validation\n\n", purpose))
			sb.WriteString("All configured models were removed:\n")
			for _, r := range removed {
				sb.WriteString(fmt.Sprintf("  - %s\n", r))
			}
			sb.WriteString("\nThe agent purpose requires at least one model with tool_use capability.\n")
			sb.WriteString("Run 'goclaw setup' to reconfigure your model chains.")
			return fmt.Errorf("%s", sb.String())
		}
		L_warn("llm: no valid models remaining after validation",
			"purpose", purpose, "removed", len(removed))
	}

	r.mu.Lock()
	r.purposes[purpose] = LLMPurposeConfig{Models: kept, MaxInputTokens: cfg.MaxInputTokens}
	r.mu.Unlock()

	return nil
}

// checkMetadataCapabilities checks a model's capabilities against purpose
// requirements using models.json metadata. Returns a reason string if the
// model should be removed, or empty string if it passes (or is unknown).
func checkMetadataCapabilities(cfg LLMProviderConfig, modelName, purpose string) string {
	reqs, ok := purposeCapabilities[purpose]
	if !ok {
		return ""
	}

	metaProviderID := metadata.Get().ResolveProvider(cfg.Subtype, cfg.Driver, cfg.BaseURL)
	model, found := metadata.Get().GetModel(metaProviderID, modelName)
	if !found {
		L_debug("llm: model not in metadata, allowing optimistically",
			"provider", metaProviderID, "model", modelName, "purpose", purpose)
		return ""
	}

	caps := model.Capabilities
	capMap := map[string]bool{
		"tool_use":          caps.ToolUse,
		"vision":            caps.Vision,
		"streaming":         caps.Streaming,
		"reasoning":         caps.Reasoning,
		"structured_output": caps.StructuredOutput,
	}

	var missing []string
	for _, req := range reqs.required {
		if !capMap[req] {
			missing = append(missing, req)
		}
	}

	if len(missing) > 0 {
		return fmt.Sprintf("missing required capabilities: %s", strings.Join(missing, ", "))
	}

	// Warn-only checks (don't remove, just log)
	var warnings []string
	for _, w := range reqs.warnOnly {
		if !capMap[w] {
			warnings = append(warnings, w)
		}
	}
	if len(warnings) > 0 {
		L_warn("llm: model missing optional capabilities",
			"provider", metaProviderID, "model", modelName,
			"purpose", purpose, "missing", strings.Join(warnings, ", "))
	}

	return ""
}

// GetProvider returns the first available provider for a purpose.
// Iterates through the model chain until one is available.
// Falls back to the agent chain if the purpose has no models configured.
func (r *Registry) GetProvider(purpose string) (Provider, error) {
	r.mu.RLock()
	cfg, ok := r.purposes[purpose]
	if !ok || len(cfg.Models) == 0 {
		if purpose != "agent" {
			cfg = r.purposes["agent"]
			L_debug("llm: purpose has no models, falling back to agent", "purpose", purpose)
		}
	}
	r.mu.RUnlock()

	if len(cfg.Models) == 0 {
		return nil, fmt.Errorf("no models configured for purpose: %s", purpose)
	}

	for i, ref := range cfg.Models {
		resolved, err := r.resolveForPurpose(ref, purpose)
		if err != nil {
			L_debug("llm: failed to resolve model", "ref", ref, "error", err)
			continue
		}

		provider, ok := resolved.(Provider)
		if !ok {
			L_warn("llm: resolved provider does not implement Provider interface", "ref", ref)
			continue
		}

		if !provider.IsAvailable() {
			continue
		}

		if i > 0 {
			L_info("llm: using fallback", "purpose", purpose, "model", ref, "position", i+1)
		}
		L_debug("llm: provider selected", "purpose", purpose, "ref", ref)
		return provider, nil
	}

	return nil, fmt.Errorf("no available provider for %s (tried: %v)", purpose, cfg.Models)
}

// GetMaxInputTokens returns the configured maxInputTokens for a purpose.
// Returns 0 if not configured (use model context - buffer instead).
func (r *Registry) GetMaxInputTokens(purpose string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if cfg, ok := r.purposes[purpose]; ok {
		return cfg.MaxInputTokens
	}
	return 0
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

// ResolveForPurpose resolves a model reference with purpose context (no failover)
func (r *Registry) ResolveForPurpose(ref, purpose string) (interface{}, error) {
	return r.resolveForPurpose(ref, purpose)
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
	case *XAIProvider:
		// xAI doesn't support embeddings
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

// GetXAIProvider returns an xAI provider for a purpose (typed helper)
func (r *Registry) GetXAIProvider(purpose string) (*XAIProvider, error) {
	provider, err := r.GetProvider(purpose)
	if err != nil {
		return nil, err
	}
	p, ok := provider.(*XAIProvider)
	if !ok {
		return nil, fmt.Errorf("provider for %s is not xAI", purpose)
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

// ==================== Provider Cooldown Management ====================

// calculateCooldownDuration returns the cooldown duration based on error count and type.
// Non-billing: 1min → 5min → 25min → 1hr max (exponential base 5)
// Billing: 5hr → 10hr → 20hr → 24hr max (exponential base 2)
func calculateCooldownDuration(errorCount int, isBilling bool) time.Duration {
	if errorCount < 1 {
		errorCount = 1
	}

	if isBilling {
		// Billing: 5hr * 2^(n-1), max 24hr
		base := 5 * time.Hour
		maxDur := 24 * time.Hour
		exponent := min(errorCount-1, 2) // Cap at 2 (5h → 10h → 20h)
		dur := time.Duration(float64(base) * math.Pow(2, float64(exponent)))
		if dur > maxDur {
			return maxDur
		}
		return dur
	}

	// Non-billing: 1min * 5^(n-1), max 1hr
	base := time.Minute
	maxDur := time.Hour
	exponent := min(errorCount-1, 3) // Cap at 3 (1m → 5m → 25m → 125m capped to 1hr)
	dur := time.Duration(float64(base) * math.Pow(5, float64(exponent)))
	if dur > maxDur {
		return maxDur
	}
	return dur
}

// isProviderInCooldown checks if a provider is currently in cooldown.
func (r *Registry) isProviderInCooldown(alias string) bool {
	r.cooldownMu.RLock()
	defer r.cooldownMu.RUnlock()

	cd := r.cooldowns[alias]
	return cd != nil && time.Now().Before(cd.until)
}

// markProviderCooldown puts a provider into cooldown with exponential backoff.
func (r *Registry) markProviderCooldown(alias string, errType ErrorType) {
	r.cooldownMu.Lock()
	defer r.cooldownMu.Unlock()

	cd := r.cooldowns[alias]
	if cd == nil {
		cd = &providerCooldown{}
		r.cooldowns[alias] = cd
	}

	cd.errorCount++
	cd.reason = errType
	cd.until = time.Now().Add(calculateCooldownDuration(cd.errorCount, errType == ErrorTypeBilling))

	L_warn("llm: provider cooldown",
		"provider", alias,
		"until", cd.until.Format("15:04:05"),
		"reason", errType,
		"errorCount", cd.errorCount,
		"duration", time.Until(cd.until).Round(time.Second))
}

// clearProviderCooldown removes cooldown state for a provider.
// Returns whether the provider was in cooldown and the reason.
func (r *Registry) clearProviderCooldown(alias string) (wasInCooldown bool, reason ErrorType) {
	r.cooldownMu.Lock()
	defer r.cooldownMu.Unlock()

	cd := r.cooldowns[alias]
	if cd != nil {
		wasInCooldown = true
		reason = cd.reason
		delete(r.cooldowns, alias)
		L_info("llm: provider cooldown cleared", "provider", alias, "wasReason", reason)
	}
	return
}

// ClearAllCooldowns removes all provider cooldowns (for /llm reset command).
// Returns the number of cooldowns cleared.
func (r *Registry) ClearAllCooldowns() int {
	r.cooldownMu.Lock()
	defer r.cooldownMu.Unlock()

	count := len(r.cooldowns)
	r.cooldowns = make(map[string]*providerCooldown)

	if count > 0 {
		L_info("llm: all cooldowns cleared", "count", count)
	}
	return count
}

// GetProviderStatus returns the status of all providers for /llm command.
func (r *Registry) GetProviderStatus() []ProviderStatus {
	r.mu.RLock()
	providers := make([]string, 0, len(r.providers))
	for name := range r.providers {
		providers = append(providers, name)
	}
	r.mu.RUnlock()

	r.cooldownMu.RLock()
	defer r.cooldownMu.RUnlock()

	now := time.Now()
	statuses := make([]ProviderStatus, 0, len(providers))

	for _, alias := range providers {
		status := ProviderStatus{Alias: alias}
		if cd := r.cooldowns[alias]; cd != nil && now.Before(cd.until) {
			status.InCooldown = true
			status.Until = cd.until
			status.Reason = cd.reason
			status.ErrorCount = cd.errorCount
		}
		statuses = append(statuses, status)
	}

	return statuses
}

// getModelsWithAgentFallback returns the model chain for a purpose,
// appending agent chain models as last-resort fallbacks for non-agent purposes.
// This ensures summarization/output can fall back to the agent model if its own
// chain is exhausted or empty, since these operations are critical to gateway function.
func (r *Registry) getModelsWithAgentFallback(purpose string) []string {
	r.mu.RLock()
	cfg := r.purposes[purpose]
	agentCfg := r.purposes["agent"]
	r.mu.RUnlock()

	models := make([]string, len(cfg.Models))
	copy(models, cfg.Models)

	if purpose == "agent" || len(agentCfg.Models) == 0 {
		return models
	}

	// Build set of already-present models to avoid duplicates
	seen := make(map[string]bool, len(models))
	for _, m := range models {
		seen[m] = true
	}

	for _, m := range agentCfg.Models {
		if !seen[m] {
			models = append(models, m)
		}
	}

	if len(models) > len(cfg.Models) {
		L_debug("failover: agent chain appended as fallback",
			"purpose", purpose,
			"purposeModels", len(cfg.Models),
			"totalCandidates", len(models))
	}

	return models
}

// ==================== Failover Streaming ====================

// FailoverAttempt records a single attempt in the failover chain
type FailoverAttempt struct {
	Model   string    // Model reference that was tried
	Reason  ErrorType // Error type (if failed)
	Skipped bool      // True if skipped due to cooldown (no network call)
}

// RecoveryInfo records when a provider recovered from cooldown
type RecoveryInfo struct {
	Provider  string
	WasReason ErrorType
}

// FailoverResult contains the result of a failover-enabled stream call
type FailoverResult struct {
	Response   *Response
	ModelUsed  string            // Model reference that succeeded
	Attempts   []FailoverAttempt // All attempts (for notification)
	FailedOver bool              // True if not using primary model
	Recovered  *RecoveryInfo     // Non-nil if provider recovered from cooldown
}

// StreamMessageWithFailover tries models in the chain for a purpose, handling
// failover and cooldowns. Returns detailed result for notification purposes.
// stateAccessor is used for stateful providers (like xAI) to load/save session state.
func (r *Registry) StreamMessageWithFailover(
	ctx context.Context,
	purpose string,
	stateAccessor ProviderStateAccessor,
	messages []types.Message,
	toolDefs []types.ToolDefinition,
	systemPrompt string,
	onDelta func(delta string),
	opts *StreamOptions,
) (*FailoverResult, error) {
	r.mu.RLock()
	purposeCfg, ok := r.purposes[purpose]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown purpose: %s", purpose)
	}

	candidates := r.getModelsWithAgentFallback(purpose)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no models configured for purpose: %s", purpose)
	}

	// Track which models belong to this purpose vs agent fallback
	purposeModels := make(map[string]bool, len(purposeCfg.Models))
	for _, m := range purposeCfg.Models {
		purposeModels[m] = true
	}

	result := &FailoverResult{
		Attempts: make([]FailoverAttempt, 0, len(candidates)),
	}

	var lastErr error
	primaryModel := candidates[0]

	for _, modelRef := range candidates {
		// Parse provider alias from "provider/model" or "provider/subpath/model"
		parts := strings.SplitN(modelRef, "/", 2)
		if len(parts) < 2 {
			L_debug("failover: invalid model ref", "ref", modelRef)
			continue
		}
		providerAlias := parts[0]

		// Check cooldown (no network call if in cooldown)
		if r.isProviderInCooldown(providerAlias) {
			result.Attempts = append(result.Attempts, FailoverAttempt{
				Model:   modelRef,
				Skipped: true,
			})
			L_debug("failover: provider in cooldown, skipping", "model", modelRef)
			continue
		}

		// Resolve provider with model
		resolved, err := r.resolveForPurpose(modelRef, purpose)
		if err != nil {
			L_debug("failover: model unavailable", "model", modelRef, "error", err)
			continue
		}

		p, ok := resolved.(Provider)
		if !ok || !p.IsAvailable() {
			continue
		}

		// Build state key for stateful providers: providerName:model
		stateKey := p.Name() + ":" + p.Model()

		// Load state if provider supports it
		if sp, ok := p.(StatefulProvider); ok && stateAccessor != nil {
			state := stateAccessor.GetProviderState(stateKey)
			sp.LoadSessionState(state)
			L_debug("stateful provider: loaded state", "key", stateKey, "hasState", state != nil)
		}

		// Try the call (inject purpose into context for per-purpose metrics)
		purposeCtx := ContextWithPurpose(ctx, purpose)
		resp, err := p.StreamMessage(purposeCtx, messages, toolDefs, systemPrompt, onDelta, opts)

		// Save state after call (even on error - state may have changed)
		if sp, ok := p.(StatefulProvider); ok && stateAccessor != nil {
			state := sp.SaveSessionState()
			stateAccessor.SetProviderState(stateKey, state)
			L_debug("stateful provider: saved state", "key", stateKey, "hasState", state != nil)
		}

		if err == nil {
			// Success!
			result.Response = resp
			result.ModelUsed = modelRef
			result.FailedOver = modelRef != primaryModel

			// Check if provider recovered from cooldown
			wasInCooldown, wasReason := r.clearProviderCooldown(providerAlias)
			if wasInCooldown {
				result.Recovered = &RecoveryInfo{
					Provider:  providerAlias,
					WasReason: wasReason,
				}
			}

			if !purposeModels[modelRef] {
				L_warn("failover: using agent model for non-agent purpose",
					"purpose", purpose, "model", modelRef)
			} else if result.FailedOver {
				L_info("failover: using fallback model", "model", modelRef, "primary", primaryModel)
			}
			return result, nil
		}

		// Classify the error
		errType := ClassifyError(err.Error())
		result.Attempts = append(result.Attempts, FailoverAttempt{
			Model:   modelRef,
			Reason:  errType,
			Skipped: false,
		})

		// Non-failover errors: return immediately
		if !IsFailoverError(errType) {
			result.ModelUsed = modelRef
			L_warn("failover: non-failover error, stopping",
				"model", modelRef,
				"errType", errType,
				"error", err)
			return result, err
		}

		// Failover error: mark cooldown and try next
		r.markProviderCooldown(providerAlias, errType)
		L_warn("failover: trying next model",
			"failed", modelRef,
			"reason", errType,
			"error", err)
		lastErr = err
	}

	// All models failed
	return result, fmt.Errorf("all models failed for %s (last: %w)", purpose, lastErr)
}

// SimpleMessageResult contains the result of a failover-enabled simple message call
type SimpleMessageResult struct {
	Text       string
	ModelUsed  string
	FailedOver bool
	Recovered  *RecoveryInfo
}

// SimpleMessageWithFailover tries models in the chain for a purpose using SimpleMessage.
// This is for non-streaming calls like summarization.
// stateAccessor is used for stateful providers (like xAI) to load/save session state.
func (r *Registry) SimpleMessageWithFailover(
	ctx context.Context,
	purpose string,
	stateAccessor ProviderStateAccessor,
	userMessage string,
	systemPrompt string,
) (*SimpleMessageResult, error) {
	r.mu.RLock()
	purposeCfg, ok := r.purposes[purpose]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown purpose: %s", purpose)
	}

	candidates := r.getModelsWithAgentFallback(purpose)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no models configured for purpose: %s", purpose)
	}

	// Track which models belong to this purpose vs agent fallback
	purposeModels := make(map[string]bool, len(purposeCfg.Models))
	for _, m := range purposeCfg.Models {
		purposeModels[m] = true
	}

	result := &SimpleMessageResult{}
	var lastErr error
	primaryModel := candidates[0]

	for _, modelRef := range candidates {
		// Parse provider alias
		parts := strings.SplitN(modelRef, "/", 2)
		if len(parts) < 2 {
			L_debug("failover: invalid model ref", "ref", modelRef)
			continue
		}
		providerAlias := parts[0]

		// Check cooldown
		if r.isProviderInCooldown(providerAlias) {
			L_debug("failover: provider in cooldown, skipping", "model", modelRef)
			continue
		}

		// Resolve provider
		resolved, err := r.resolveForPurpose(modelRef, purpose)
		if err != nil {
			L_debug("failover: model unavailable", "model", modelRef, "error", err)
			continue
		}

		p, ok := resolved.(Provider)
		if !ok || !p.IsAvailable() {
			continue
		}

		// Build state key for stateful providers: providerName:model
		stateKey := p.Name() + ":" + p.Model()

		// Load state if provider supports it
		if sp, ok := p.(StatefulProvider); ok && stateAccessor != nil {
			state := stateAccessor.GetProviderState(stateKey)
			sp.LoadSessionState(state)
			L_debug("stateful provider: loaded state", "key", stateKey, "hasState", state != nil)
		}

		// Try the call (inject purpose into context for per-purpose metrics)
		purposeCtx := ContextWithPurpose(ctx, purpose)
		text, err := p.SimpleMessage(purposeCtx, userMessage, systemPrompt)

		// Save state after call (even on error - state may have changed)
		if sp, ok := p.(StatefulProvider); ok && stateAccessor != nil {
			state := sp.SaveSessionState()
			stateAccessor.SetProviderState(stateKey, state)
			L_debug("stateful provider: saved state", "key", stateKey, "hasState", state != nil)
		}

		if err == nil {
			// Success!
			result.Text = text
			result.ModelUsed = modelRef
			result.FailedOver = modelRef != primaryModel

			// Check if provider recovered from cooldown
			wasInCooldown, wasReason := r.clearProviderCooldown(providerAlias)
			if wasInCooldown {
				result.Recovered = &RecoveryInfo{
					Provider:  providerAlias,
					WasReason: wasReason,
				}
			}

			if !purposeModels[modelRef] {
				L_warn("failover: using agent model for non-agent purpose",
					"purpose", purpose, "model", modelRef)
			} else if result.FailedOver {
				L_info("failover: using fallback model", "model", modelRef, "primary", primaryModel, "purpose", purpose)
			}
			return result, nil
		}

		// Classify the error
		errType := ClassifyError(err.Error())

		// Non-failover errors: return immediately
		if !IsFailoverError(errType) {
			result.ModelUsed = modelRef
			L_warn("failover: non-failover error, stopping",
				"model", modelRef,
				"errType", errType,
				"error", err,
				"purpose", purpose)
			return result, err
		}

		// Failover error: mark cooldown and try next
		r.markProviderCooldown(providerAlias, errType)
		L_warn("failover: trying next model",
			"failed", modelRef,
			"reason", errType,
			"error", err,
			"purpose", purpose)
		lastErr = err
	}

	// All models failed
	return result, fmt.Errorf("all models failed for %s (last: %w)", purpose, lastErr)
}
