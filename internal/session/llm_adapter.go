package session

import (
	"context"
	"fmt"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// Type alias for SummarizationClient - defined in types package
type SummarizationClient = types.SummarizationClient

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
	// Use truncated version that fits within model context limits (150k tokens ≈ 600k chars)
	conversationText := BuildMessagesForSummary(messages, 150000)
	
	// Estimate tokens in final prompt
	estimatedTokens := len(conversationText) / 4
	L_debug("compaction: summary input prepared", 
		"messages", len(messages), 
		"textChars", len(conversationText),
		"estimatedTokens", estimatedTokens)

	userMessage := fmt.Sprintf("%s\n\nConversation to summarize:\n%s", CompactionSummaryPrompt, conversationText)
	systemPrompt := "You are a helpful assistant that creates concise conversation summaries."

	// Call LLM
	responseText, err := client.SimpleMessage(ctx, userMessage, systemPrompt)
	if err != nil {
		return "", fmt.Errorf("LLM call failed: %w", err)
	}

	return responseText, nil
}
