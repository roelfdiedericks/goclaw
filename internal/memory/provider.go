package memory

import (
	"context"
)

// EmbeddingProvider generates embeddings for text
type EmbeddingProvider interface {
	// ID returns the provider identifier (e.g., "ollama", "openai", "none")
	ID() string

	// Model returns the model name used for embeddings
	Model() string

	// Dimensions returns the embedding vector dimensions
	Dimensions() int

	// EmbedQuery generates an embedding for a search query
	EmbedQuery(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch generates embeddings for multiple texts
	// Returns embeddings in the same order as the input texts
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)

	// Available returns true if the provider is ready to generate embeddings
	Available() bool
}

// LLMEmbedder is an interface for LLM providers that support embeddings.
// This matches llm.OllamaProvider's embedding methods without importing llm package.
type LLMEmbedder interface {
	Name() string
	Model() string
	EmbeddingDimensions() int
	IsAvailable() bool
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// LLMProviderAdapter wraps an LLMEmbedder to implement EmbeddingProvider
type LLMProviderAdapter struct {
	provider LLMEmbedder
}

// NewLLMProviderAdapter creates an adapter for an LLMEmbedder (e.g., llm.OllamaProvider)
func NewLLMProviderAdapter(provider LLMEmbedder) *LLMProviderAdapter {
	return &LLMProviderAdapter{provider: provider}
}

func (a *LLMProviderAdapter) ID() string      { return a.provider.Name() }
func (a *LLMProviderAdapter) Model() string   { return a.provider.Model() }
func (a *LLMProviderAdapter) Dimensions() int { return a.provider.EmbeddingDimensions() }
func (a *LLMProviderAdapter) Available() bool { return a.provider.IsAvailable() }

func (a *LLMProviderAdapter) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	return a.provider.Embed(ctx, text)
}

func (a *LLMProviderAdapter) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	return a.provider.EmbedBatch(ctx, texts)
}

// Ensure LLMProviderAdapter implements EmbeddingProvider
var _ EmbeddingProvider = (*LLMProviderAdapter)(nil)

// NoopProvider is a provider that doesn't generate embeddings
// Used when embeddings are disabled or unavailable
type NoopProvider struct{}

func (p *NoopProvider) ID() string                   { return "none" }
func (p *NoopProvider) Model() string                { return "" }
func (p *NoopProvider) Dimensions() int              { return 0 }
func (p *NoopProvider) Available() bool              { return false }

func (p *NoopProvider) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	return nil, nil
}

func (p *NoopProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	return make([][]float32, len(texts)), nil
}

// Ensure NoopProvider implements EmbeddingProvider
var _ EmbeddingProvider = (*NoopProvider)(nil)
