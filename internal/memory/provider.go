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
