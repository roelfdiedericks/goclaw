package session

import (
	"context"
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// Type alias for SummarizationClient - defined in types package
type SummarizationClient = types.SummarizationClient

// GenerateCheckpointWithClient generates a checkpoint using the provided client.
// maxInputTokens is the configured limit (0 = use model context - buffer).
// Deprecated: Use GenerateCheckpointWithRegistry for failover support.
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

	// Try with current limit, retry with reduced limit on context overflow
	const maxRetries = 2
	currentLimit := effectiveLimit

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Build a condensed conversation (reuse summary builder for truncation)
		conversationText := BuildMessagesForSummary(messages, currentLimit)

		userMessage := fmt.Sprintf("%s\n\nConversation to summarize:\n%s", CheckpointPrompt, conversationText)
		systemPrompt := "You are a helpful assistant that creates structured conversation summaries. Respond only with valid JSON."

		// Call LLM
		responseText, err := client.SimpleMessage(ctx, userMessage, systemPrompt)
		if err != nil {
			// Check if this is a context overflow - retry with reduced limit
			if llm.IsContextOverflowError(err) && attempt < maxRetries {
				reducedLimit := currentLimit * 3 / 4 // 25% reduction
				if reducedLimit >= 500 {
					L_warn("checkpoint: context overflow, retrying with reduced limit",
						"attempt", attempt+1,
						"originalLimit", currentLimit,
						"reducedLimit", reducedLimit,
						"error", err)
					currentLimit = reducedLimit
					continue
				}
			}
			return nil, fmt.Errorf("LLM call failed: %w", err)
		}

		// Parse the response
		checkpoint, err := ParseCheckpointResponse(responseText)
		if err != nil {
			return nil, fmt.Errorf("failed to parse checkpoint response: %w", err)
		}

		return checkpoint, nil
	}

	return nil, fmt.Errorf("LLM call failed after %d retries", maxRetries+1)
}

// GenerateSummaryWithClient generates a compaction summary using the provided client.
// maxInputTokens is the configured limit (0 = use model context - buffer).
// Deprecated: Use GenerateSummaryWithRegistry for failover support.
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

	// Try with current limit, retry with reduced limit on context overflow
	const maxRetries = 2
	currentLimit := effectiveLimit

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Build a condensed conversation for the summary prompt
		conversationText := BuildMessagesForSummary(messages, currentLimit)

		// Estimate tokens in final prompt
		estimatedTokens := len(conversationText) / 4
		L_debug("compaction: summary input prepared",
			"attempt", attempt+1,
			"messages", len(messages),
			"textChars", len(conversationText),
			"estimatedTokens", estimatedTokens,
			"inputLimit", currentLimit)

		userMessage := fmt.Sprintf("%s\n\nConversation to summarize:\n%s", CompactionSummaryPrompt, conversationText)
		systemPrompt := "You are a helpful assistant that creates concise conversation summaries."

		// Call LLM
		responseText, err := client.SimpleMessage(ctx, userMessage, systemPrompt)
		if err == nil {
			return responseText, nil
		}

		// Check if this is a context overflow - retry with reduced limit
		if llm.IsContextOverflowError(err) && attempt < maxRetries {
			reducedLimit := currentLimit * 3 / 4 // 25% reduction
			if reducedLimit >= 500 {
				L_warn("compaction: context overflow, retrying with reduced limit",
					"attempt", attempt+1,
					"originalLimit", currentLimit,
					"reducedLimit", reducedLimit,
					"error", err)
				currentLimit = reducedLimit
				continue
			}
		}

		// Non-overflow error or max retries reached
		return "", fmt.Errorf("LLM call failed: %w", err)
	}

	return "", fmt.Errorf("LLM call failed after %d retries", maxRetries+1)
}

// GenerateSummaryWithRegistry generates a compaction summary using the registry with failover.
// Handles both failover (rate_limit, auth, billing, timeout) and context overflow (reduce input).
// Returns: summary text, model used, error
func GenerateSummaryWithRegistry(ctx context.Context, registry *llm.Registry, messages []Message, maxInputTokens int) (string, string, error) {
	if registry == nil {
		return "", "", fmt.Errorf("no registry available")
	}

	// Get model list to determine effective limit from primary model
	models := registry.ListModelsForPurpose("summarization")
	if len(models) == 0 {
		return "", "", fmt.Errorf("no models configured for summarization")
	}

	// Use configured maxInputTokens or a reasonable default
	effectiveLimit := maxInputTokens
	if effectiveLimit <= 0 {
		effectiveLimit = 4000 // default limit if not configured
	}

	L_info("compaction: generating summary with failover",
		"models", len(models),
		"effectiveLimit", effectiveLimit)

	// Try with current limit, retry with reduced limit on context overflow
	const maxRetries = 2
	currentLimit := effectiveLimit

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Build a condensed conversation for the summary prompt
		conversationText := BuildMessagesForSummary(messages, currentLimit)

		// Estimate tokens in final prompt
		estimatedTokens := len(conversationText) / 4
		L_debug("compaction: summary input prepared",
			"attempt", attempt+1,
			"messages", len(messages),
			"textChars", len(conversationText),
			"estimatedTokens", estimatedTokens,
			"inputLimit", currentLimit)

		userMessage := fmt.Sprintf("%s\n\nConversation to summarize:\n%s", CompactionSummaryPrompt, conversationText)
		systemPrompt := "You are a helpful assistant that creates concise conversation summaries."

		// Call LLM with failover
		result, err := registry.SimpleMessageWithFailover(ctx, "summarization", userMessage, systemPrompt)
		if err == nil {
			if result.FailedOver {
				L_info("compaction: used fallback model", "model", result.ModelUsed)
			}
			return result.Text, result.ModelUsed, nil
		}

		// Check if this is a context overflow - retry with reduced limit
		errType := llm.ClassifyError(err.Error())
		if errType == llm.ErrorTypeContextOverflow && attempt < maxRetries {
			reducedLimit := currentLimit * 3 / 4 // 25% reduction
			if reducedLimit >= 500 {
				L_warn("compaction: context overflow, retrying with reduced limit",
					"attempt", attempt+1,
					"originalLimit", currentLimit,
					"reducedLimit", reducedLimit,
					"error", err)
				currentLimit = reducedLimit
				continue
			}
		}

		// Non-overflow error or max retries reached
		return "", "", fmt.Errorf("LLM call failed: %w", err)
	}

	return "", "", fmt.Errorf("LLM call failed after %d retries", maxRetries+1)
}

// GenerateCheckpointWithRegistry generates a checkpoint using the registry with failover.
// Handles both failover (rate_limit, auth, billing, timeout) and context overflow (reduce input).
// Returns: checkpoint data, model used, error
func GenerateCheckpointWithRegistry(ctx context.Context, registry *llm.Registry, messages []Message, maxInputTokens int) (*CheckpointData, string, error) {
	if registry == nil {
		return nil, "", fmt.Errorf("no registry available")
	}

	// Use configured maxInputTokens or a reasonable default
	effectiveLimit := maxInputTokens
	if effectiveLimit <= 0 {
		effectiveLimit = 4000 // default limit if not configured
	}

	// Try with current limit, retry with reduced limit on context overflow
	const maxRetries = 2
	currentLimit := effectiveLimit

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Build a condensed conversation (reuse summary builder for truncation)
		conversationText := BuildMessagesForSummary(messages, currentLimit)

		userMessage := fmt.Sprintf("%s\n\nConversation to summarize:\n%s", CheckpointPrompt, conversationText)
		systemPrompt := "You are a helpful assistant that creates structured conversation summaries. Respond only with valid JSON."

		// Call LLM with failover
		result, err := registry.SimpleMessageWithFailover(ctx, "summarization", userMessage, systemPrompt)
		if err == nil {
			// Parse the response
			checkpoint, parseErr := ParseCheckpointResponse(result.Text)
			if parseErr != nil {
				return nil, result.ModelUsed, fmt.Errorf("failed to parse checkpoint response: %w", parseErr)
			}
			return checkpoint, result.ModelUsed, nil
		}

		// Check if this is a context overflow - retry with reduced limit
		errType := llm.ClassifyError(err.Error())
		if errType == llm.ErrorTypeContextOverflow && attempt < maxRetries {
			reducedLimit := currentLimit * 3 / 4 // 25% reduction
			if reducedLimit >= 500 {
				L_warn("checkpoint: context overflow, retrying with reduced limit",
					"attempt", attempt+1,
					"originalLimit", currentLimit,
					"reducedLimit", reducedLimit,
					"error", err)
				currentLimit = reducedLimit
				continue
			}
		}

		// Non-overflow error or max retries reached
		return nil, "", fmt.Errorf("LLM call failed: %w", err)
	}

	return nil, "", fmt.Errorf("LLM call failed after %d retries", maxRetries+1)
}
