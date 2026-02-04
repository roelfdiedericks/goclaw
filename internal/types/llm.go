package types

import "context"

// SummarizationClient is the interface for LLM clients used in checkpoint/compaction.
// Implemented by llm.AnthropicProvider and llm.OllamaProvider.
type SummarizationClient interface {
	SimpleMessage(ctx context.Context, userMessage, systemPrompt string) (string, error)
	Model() string
	IsAvailable() bool
}
