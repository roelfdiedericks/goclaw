package session

import (
	"context"
	"fmt"
)

// GenericLLMClient is the interface for LLM clients that can be used for checkpoints/compaction
// This matches the signature of llm.Client but avoids import cycles
type GenericLLMClient interface {
	// StreamMessageSimple is a simplified streaming call for summaries
	StreamMessageSimple(ctx context.Context, userMessage, systemPrompt string) (string, error)
	Model() string
	IsAvailable() bool
}

// LLMAdapter wraps a general LLM client to implement checkpoint/compaction interfaces
type LLMAdapter struct {
	streamFunc   func(ctx context.Context, userMessage, systemPrompt string) (string, error)
	modelName    string
	available    bool
}

// NewLLMAdapterFunc creates an adapter using function callbacks (avoids import cycles)
func NewLLMAdapterFunc(
	streamFunc func(ctx context.Context, userMessage, systemPrompt string) (string, error),
	modelName string,
) *LLMAdapter {
	return &LLMAdapter{
		streamFunc: streamFunc,
		modelName:  modelName,
		available:  streamFunc != nil,
	}
}

// GenerateCheckpoint implements CheckpointLLMClient
func (a *LLMAdapter) GenerateCheckpoint(ctx context.Context, messages []Message, currentTokens int) (*CheckpointData, error) {
	if a.streamFunc == nil {
		return nil, fmt.Errorf("no LLM client available")
	}

	// Build a condensed conversation for the checkpoint prompt
	conversationText := BuildMessagesForCheckpoint(messages)
	
	userMessage := fmt.Sprintf("%s\n\nConversation to summarize:\n%s", CheckpointPrompt, conversationText)
	systemPrompt := "You are a helpful assistant that creates structured conversation summaries. Respond only with valid JSON."

	// Call LLM
	responseText, err := a.streamFunc(ctx, userMessage, systemPrompt)
	if err != nil {
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}

	// Parse the response
	checkpoint, err := ParseCheckpointResponse(responseText)
	if err != nil {
		return nil, fmt.Errorf("failed to parse checkpoint response: %w", err)
	}

	return checkpoint, nil
}

// GenerateSummary implements CompactionLLMClient
func (a *LLMAdapter) GenerateSummary(ctx context.Context, messages []Message, maxTokens int) (string, error) {
	if a.streamFunc == nil {
		return "", fmt.Errorf("no LLM client available")
	}

	// Build a condensed conversation for the summary prompt
	conversationText := BuildMessagesForCheckpoint(messages)
	
	userMessage := fmt.Sprintf("%s\n\nConversation to summarize:\n%s", CompactionSummaryPrompt, conversationText)
	systemPrompt := "You are a helpful assistant that creates concise conversation summaries."

	// Call LLM
	responseText, err := a.streamFunc(ctx, userMessage, systemPrompt)
	if err != nil {
		return "", fmt.Errorf("LLM call failed: %w", err)
	}

	return responseText, nil
}

// IsAvailable implements both interfaces
func (a *LLMAdapter) IsAvailable() bool {
	return a.available && a.streamFunc != nil
}

// Model returns the model name
func (a *LLMAdapter) Model() string {
	return a.modelName
}
