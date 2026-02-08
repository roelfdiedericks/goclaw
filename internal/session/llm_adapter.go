package session

import (
	"context"
	"fmt"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// Type alias for SummarizationClient - defined in types package
type SummarizationClient = types.SummarizationClient

// GenerateCheckpointWithClient generates a checkpoint using the provided client.
// maxInputTokens is the configured limit (0 = use model context - buffer).
func GenerateCheckpointWithClient(ctx context.Context, client SummarizationClient, messages []Message, maxInputTokens int) (*CheckpointData, error) {
	if client == nil || !client.IsAvailable() {
		return nil, fmt.Errorf("no LLM client available")
	}

	// Calculate effective input token limit (same logic as summary generation)
	modelContext := client.ContextTokens()
	outputBuffer := 2048 // reserve more tokens for JSON checkpoint output
	
	effectiveLimit := modelContext - outputBuffer
	if effectiveLimit < 1000 {
		effectiveLimit = 1000
	}
	
	if maxInputTokens > 0 && maxInputTokens < effectiveLimit {
		effectiveLimit = maxInputTokens
	}

	// Build a condensed conversation (reuse summary builder for truncation)
	conversationText := BuildMessagesForSummary(messages, effectiveLimit)

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

// GenerateSummaryWithClient generates a compaction summary using the provided client.
// maxInputTokens is the configured limit (0 = use model context - buffer).
func GenerateSummaryWithClient(ctx context.Context, client SummarizationClient, messages []Message, maxInputTokens int) (string, error) {
	if client == nil || !client.IsAvailable() {
		return "", fmt.Errorf("no LLM client available")
	}

	// Calculate effective input token limit
	// Use the smaller of: configured limit OR model context - output buffer
	modelContext := client.ContextTokens()
	outputBuffer := 1024 // reserve tokens for summary output
	
	effectiveLimit := modelContext - outputBuffer
	if effectiveLimit < 1000 {
		effectiveLimit = 1000 // minimum reasonable limit
	}
	
	// If config specifies a limit, use the smaller of config and model limit
	if maxInputTokens > 0 && maxInputTokens < effectiveLimit {
		effectiveLimit = maxInputTokens
	}
	
	L_info("compaction: input limit calculated",
		"modelContext", modelContext,
		"configLimit", maxInputTokens,
		"effectiveLimit", effectiveLimit)

	// Build a condensed conversation for the summary prompt
	conversationText := BuildMessagesForSummary(messages, effectiveLimit)
	
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
