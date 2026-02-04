package session

import (
	"context"
	"fmt"
)

// SummarizationClient is the interface for LLM clients used in checkpoint/compaction.
// This matches the llm.Provider interface methods, avoiding import cycles.
type SummarizationClient interface {
	SimpleMessage(ctx context.Context, userMessage, systemPrompt string) (string, error)
	Model() string
	IsAvailable() bool
}

// GenerateCheckpointWithClient generates a checkpoint using the provided client
func GenerateCheckpointWithClient(ctx context.Context, client SummarizationClient, messages []Message, currentTokens int) (*CheckpointData, error) {
	if client == nil || !client.IsAvailable() {
		return nil, fmt.Errorf("no LLM client available")
	}

	// Build a condensed conversation for the checkpoint prompt
	conversationText := BuildMessagesForCheckpoint(messages)

	userMessage := fmt.Sprintf("%s\n\nConversation to summarize:\n%s", CheckpointPrompt, conversationText)
	systemPrompt := "You are a helpful assistant that creates structured conversation summaries. Respond only with valid JSON."

	// Call LLM
	responseText, err := client.SimpleMessage(ctx, userMessage, systemPrompt)
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

// GenerateSummaryWithClient generates a compaction summary using the provided client
func GenerateSummaryWithClient(ctx context.Context, client SummarizationClient, messages []Message, maxTokens int) (string, error) {
	if client == nil || !client.IsAvailable() {
		return "", fmt.Errorf("no LLM client available")
	}

	// Build a condensed conversation for the summary prompt
	conversationText := BuildMessagesForCheckpoint(messages)

	userMessage := fmt.Sprintf("%s\n\nConversation to summarize:\n%s", CompactionSummaryPrompt, conversationText)
	systemPrompt := "You are a helpful assistant that creates concise conversation summaries."

	// Call LLM
	responseText, err := client.SimpleMessage(ctx, userMessage, systemPrompt)
	if err != nil {
		return "", fmt.Errorf("LLM call failed: %w", err)
	}

	return responseText, nil
}
