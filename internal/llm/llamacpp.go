package llm

import (
	"fmt"
	"sync"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/localllm"
)

func init() {
	RegisterDriver(DriverDescriptor{
		ID:                 "llamacpp",
		Label:              "Llama.cpp",
		Order:              35,
		IsLocal:            true,
		SupportsEmbeddings: true,
		New: func(name string, cfg LLMProviderConfig) (Provider, error) {
			return NewLlamaCppProvider(name, cfg)
		},
	})
}

// LlamaCppProvider is a dedicated provider identity for llama.cpp.
// It intentionally reuses the OpenAI-compatible transport surface for phase 1,
// while keeping a distinct driver and provider type for future llama.cpp-specific behavior.
type LlamaCppProvider struct {
	*OpenAIProvider
}

func NewLlamaCppProvider(name string, cfg LLMProviderConfig) (*LlamaCppProvider, error) {
	base, err := NewOpenAIProvider(name, cfg)
	if err != nil {
		return nil, err
	}
	base.metadataProvider = "llamacpp"
	L_debug("llamacpp provider created", "name", name, "baseURL", base.baseURL)
	return &LlamaCppProvider{OpenAIProvider: base}, nil
}

func (p *LlamaCppProvider) Type() string {
	return "llamacpp"
}

func (p *LlamaCppProvider) MetadataProvider() string {
	return "llamacpp"
}

func (p *LlamaCppProvider) IsAvailable() bool {
	if p == nil || p.OpenAIProvider == nil {
		return false
	}
	if normalizedLlamaCppMode(p.config) != LlamaCppModeManaged {
		return p.OpenAIProvider.IsAvailable()
	}
	status := localllm.GetManager().Status()
	if !status.Configured {
		return false
	}
	return status.Server.Healthy
}

func (p *LlamaCppProvider) WithModel(model string) Provider {
	clone := p.clone()
	clone.available = false
	clone.embeddingDimensions = 0
	clone.model = model
	clone.metricPrefix = fmt.Sprintf("llm/%s/%s/%s", p.Type(), p.Name(), model)
	return clone
}

func (p *LlamaCppProvider) WithMaxTokens(max int) Provider {
	clone := p.clone()
	clone.maxTokens = max
	return clone
}

func (p *LlamaCppProvider) WithEmbeddingModel(model string) Provider {
	clone := p.clone()
	clone.available = false
	clone.embeddingDimensions = 0
	clone.model = model
	clone.embeddingOnly = true
	clone.metricPrefix = fmt.Sprintf("llm/%s/%s/%s", p.Type(), p.Name(), model)
	clone.checkEmbeddingAvailability()
	return clone
}

func (p *LlamaCppProvider) clone() *LlamaCppProvider {
	base := *p.OpenAIProvider               //nolint:govet // mu is reset immediately below
	base.mu = sync.RWMutex{}               // Fresh mutex for the clone
	base.metadataProvider = "llamacpp"
	return &LlamaCppProvider{OpenAIProvider: &base}
}
