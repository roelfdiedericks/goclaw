// Package llm provides LLM client implementations.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	. "github.com/roelfdiedericks/goclaw/internal/metrics"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// OpenAIProvider implements the Provider interface for OpenAI-compatible APIs.
// Supports streaming, native tool calling, and vision (images).
// Works with OpenAI, Kimi, OpenRouter, and other compatible APIs via BaseURL.
type OpenAIProvider struct {
	name         string // Provider instance name (e.g., "openai", "kimi")
	client       *openai.Client
	model        string
	maxTokens    int
	apiKey       string // Stored for cloning
	baseURL      string // Custom API base URL
	metricPrefix string // e.g., "llm/openai/kimi/kimi-k2.5"
}

// NewOpenAIProvider creates a new OpenAI-compatible provider from ProviderConfig.
func NewOpenAIProvider(name string, cfg ProviderConfig) (*OpenAIProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("openai API key not configured")
	}

	// Build client config
	config := openai.DefaultConfig(cfg.APIKey)
	if cfg.BaseURL != "" {
		config.BaseURL = cfg.BaseURL
	}

	client := openai.NewClientWithConfig(config)

	maxTokens := cfg.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8192
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "(default)"
	}
	L_debug("openai provider created", "name", name, "baseURL", baseURL, "maxTokens", maxTokens)

	return &OpenAIProvider{
		name:      name,
		client:    client,
		model:     "", // Model set via WithModel()
		maxTokens: maxTokens,
		apiKey:    cfg.APIKey,
		baseURL:   cfg.BaseURL,
	}, nil
}

// Name returns the provider instance name
func (p *OpenAIProvider) Name() string {
	return p.name
}

// Type returns the provider type
func (p *OpenAIProvider) Type() string {
	return "openai"
}

// Model returns the configured model name
func (p *OpenAIProvider) Model() string {
	return p.model
}

// WithModel returns a clone of the provider configured with a specific model
func (p *OpenAIProvider) WithModel(model string) Provider {
	clone := *p
	clone.model = model
	clone.metricPrefix = fmt.Sprintf("llm/%s/%s/%s", p.Type(), p.Name(), model)
	return &clone
}

// WithMaxTokens returns a clone of the provider with a different output limit
func (p *OpenAIProvider) WithMaxTokens(max int) Provider {
	clone := *p
	clone.maxTokens = max
	return &clone
}

// IsAvailable returns true if the provider is configured and ready
func (p *OpenAIProvider) IsAvailable() bool {
	return p != nil && p.client != nil && p.model != ""
}

// ContextTokens returns the model's context window size in tokens
func (p *OpenAIProvider) ContextTokens() int {
	return getOpenAIModelContextWindow(p.model)
}

// MaxTokens returns the current output limit
func (p *OpenAIProvider) MaxTokens() int {
	return p.maxTokens
}

// Embed is not supported by OpenAI provider (different endpoint structure)
func (p *OpenAIProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	return nil, ErrNotSupported{Provider: "openai", Operation: "embeddings"}
}

// EmbedBatch is not supported by OpenAI provider
func (p *OpenAIProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, ErrNotSupported{Provider: "openai", Operation: "embeddings"}
}

// EmbeddingDimensions returns 0 - embeddings not supported via this provider
func (p *OpenAIProvider) EmbeddingDimensions() int {
	return 0
}

// SupportsEmbeddings returns false
func (p *OpenAIProvider) SupportsEmbeddings() bool {
	return false
}

// getOpenAIModelContextWindow returns the context window size for a given model
func getOpenAIModelContextWindow(model string) int {
	// Kimi models
	if strings.HasPrefix(model, "kimi-k2") {
		return 262144 // 256K context (256 * 1024)
	}
	// GPT-4 variants
	if strings.Contains(model, "gpt-4") {
		if strings.Contains(model, "turbo") || strings.Contains(model, "o") {
			return 128000 // 128K for GPT-4 Turbo and GPT-4o
		}
		return 8192 // Original GPT-4
	}
	// GPT-3.5
	if strings.Contains(model, "gpt-3.5") {
		return 16385 // GPT-3.5 Turbo
	}
	// Default
	return 128000
}

// SimpleMessage sends a simple user message and returns the response text.
// This is used for checkpoint/compaction summaries where we don't need tools.
func (p *OpenAIProvider) SimpleMessage(ctx context.Context, userMessage, systemPrompt string) (string, error) {
	messages := []types.Message{
		{Role: "user", Content: userMessage},
	}

	var result string
	_, err := p.StreamMessage(ctx, messages, nil, systemPrompt, func(delta string) {
		result += delta
	})
	if err != nil {
		return "", err
	}

	return result, nil
}

// StreamMessage sends a message to the LLM and streams the response
// onDelta is called for each text chunk received
func (p *OpenAIProvider) StreamMessage(
	ctx context.Context,
	messages []types.Message,
	toolDefs []types.ToolDefinition,
	systemPrompt string,
	onDelta func(delta string),
) (*Response, error) {
	startTime := time.Now()
	L_info("llm: request started", "provider", p.name, "model", p.model, "messages", len(messages), "tools", len(toolDefs))
	L_debug("preparing OpenAI request", "messages", len(messages), "tools", len(toolDefs))

	// Convert session messages to OpenAI format
	openaiMessages, repairStats := convertToOpenAIMessages(messages)
	if repairStats.modified {
		L_debug("repaired tool pairing for OpenAI",
			"droppedOrphans", repairStats.droppedOrphans,
			"mergedToolCalls", repairStats.mergedToolCalls)
	}

	// Add system prompt if provided
	if systemPrompt != "" {
		openaiMessages = append([]openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
		}, openaiMessages...)
		L_trace("system prompt set", "length", len(systemPrompt))
	}

	// Convert tool definitions
	openaiTools := convertToOpenAITools(toolDefs)

	// Build request
	req := openai.ChatCompletionRequest{
		Model:     p.model,
		MaxTokens: p.maxTokens,
		Messages:  openaiMessages,
		Stream:    true,
		StreamOptions: &openai.StreamOptions{
			IncludeUsage: true, // Get token counts in stream
		},
	}

	// Add tools if any
	if len(openaiTools) > 0 {
		req.Tools = openaiTools
		L_trace("tools attached", "count", len(openaiTools))
	}

	L_debug("sending request to OpenAI-compatible API", "model", p.model)

	// Stream the response
	stream, err := p.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		L_error("stream creation failed", "error", err)
		// Record metrics for failed request
		if p.metricPrefix != "" {
			MetricDuration(p.metricPrefix, "request", time.Since(startTime))
			MetricFailWithReason(p.metricPrefix, "request_status", "stream_creation_error")
		}
		return nil, fmt.Errorf("stream error: %w", err)
	}
	defer stream.Close()

	response := &Response{}
	var toolCalls []openai.ToolCall
	var reasoningContent string

	for {
		chunk, err := stream.Recv()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			L_error("stream error", "error", err)
			// Record metrics for failed request
			if p.metricPrefix != "" {
				MetricDuration(p.metricPrefix, "request", time.Since(startTime))
				MetricFailWithReason(p.metricPrefix, "request_status", "stream_error")
			}
			return nil, fmt.Errorf("stream error: %w", err)
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]

		// Handle reasoning/thinking content (Kimi, Deepseek, etc.)
		if choice.Delta.ReasoningContent != "" {
			reasoningContent += choice.Delta.ReasoningContent
			L_trace("llm: reasoning content received", "length", len(choice.Delta.ReasoningContent))
		}

		// Handle text content
		if choice.Delta.Content != "" {
			response.Text += choice.Delta.Content
			if onDelta != nil {
				onDelta(choice.Delta.Content)
			}
		}

		// Handle tool calls
		for _, tc := range choice.Delta.ToolCalls {
			// Accumulate tool call data
			idx := 0
			if tc.Index != nil {
				idx = *tc.Index
			}
			for len(toolCalls) <= idx {
				toolCalls = append(toolCalls, openai.ToolCall{})
			}
			if tc.ID != "" {
				toolCalls[idx].ID = tc.ID
			}
			if tc.Type != "" {
				toolCalls[idx].Type = tc.Type
			}
			if tc.Function.Name != "" {
				toolCalls[idx].Function.Name += tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				toolCalls[idx].Function.Arguments += tc.Function.Arguments
			}
		}

		// Check finish reason
		if choice.FinishReason != "" {
			response.StopReason = string(choice.FinishReason)
		}

		// Capture usage from stream (comes with include_usage option)
		if chunk.Usage != nil {
			response.InputTokens = chunk.Usage.PromptTokens
			response.OutputTokens = chunk.Usage.CompletionTokens
		}
	}

	// Store accumulated reasoning content
	if reasoningContent != "" {
		response.Thinking = reasoningContent
		L_info("llm: reasoning content captured", "length", len(reasoningContent))
	}

	// Process accumulated tool calls
	if len(toolCalls) > 0 && toolCalls[0].ID != "" {
		tc := toolCalls[0] // Return first tool call (like Anthropic)
		response.ToolUseID = tc.ID
		response.ToolName = tc.Function.Name
		response.ToolInput = json.RawMessage(tc.Function.Arguments)
		response.StopReason = "tool_use"
		L_info("llm: tool use", "tool", tc.Function.Name, "id", tc.ID)
	}

	// If API didn't provide token counts, estimate them
	if response.InputTokens == 0 {
		response.InputTokens = estimateOpenAITokens(openaiMessages, systemPrompt)
		L_debug("llm: estimated input tokens (API didn't provide)", "estimated", response.InputTokens)
	}
	if response.OutputTokens == 0 && response.Text != "" {
		// Rough estimate: ~4 chars per token
		response.OutputTokens = len(response.Text) / 4
	}

	elapsed := time.Since(startTime)
	L_info("llm: request completed", "provider", p.name, "duration", elapsed.Round(time.Millisecond),
		"inputTokens", response.InputTokens, "outputTokens", response.OutputTokens)

	// Record metrics
	if p.metricPrefix != "" {
		MetricDuration(p.metricPrefix, "request", elapsed)
		MetricAdd(p.metricPrefix, "input_tokens", int64(response.InputTokens))
		MetricAdd(p.metricPrefix, "output_tokens", int64(response.OutputTokens))
		MetricOutcome(p.metricPrefix, "stop_reason", response.StopReason)
		MetricSuccess(p.metricPrefix, "request_status")
	}

	return response, nil
}

// openaiRepairStats tracks repairs made during message conversion
type openaiRepairStats struct {
	modified       bool
	droppedOrphans int
	mergedToolCalls int
}

// convertToOpenAIMessages converts internal messages to OpenAI format
// Handles tool_use/tool_result pairing and image attachments
func convertToOpenAIMessages(messages []types.Message) ([]openai.ChatCompletionMessage, openaiRepairStats) {
	var stats openaiRepairStats
	var result []openai.ChatCompletionMessage

	// First pass: build maps of tool_use and tool_result IDs
	toolUseIDs := make(map[string]bool)
	toolResultIDs := make(map[string]bool)
	for _, msg := range messages {
		if msg.Role == "tool_use" && msg.ToolUseID != "" {
			toolUseIDs[msg.ToolUseID] = true
		}
		if msg.Role == "tool_result" && msg.ToolUseID != "" {
			toolResultIDs[msg.ToolUseID] = true
		}
	}

	// Second pass: convert messages
	// Track pending tool_calls to merge into assistant messages
	var pendingToolCalls []openai.ToolCall
	var pendingReasoning string // Reasoning content to include with tool_calls

	for i, msg := range messages {
		switch msg.Role {
		case "user":
			// Flush any pending tool calls first
			if len(pendingToolCalls) > 0 {
				assistantMsg := openai.ChatCompletionMessage{
					Role:      openai.ChatMessageRoleAssistant,
					ToolCalls: pendingToolCalls,
				}
				if pendingReasoning != "" {
					assistantMsg.ReasoningContent = pendingReasoning
				}
				result = append(result, assistantMsg)
				pendingToolCalls = nil
				pendingReasoning = ""
			}

			// Handle images
			if len(msg.Images) > 0 {
				var parts []openai.ChatMessagePart
				// Add images first
				for _, img := range msg.Images {
					dataURL := fmt.Sprintf("data:%s;base64,%s", img.MimeType, img.Data)
					parts = append(parts, openai.ChatMessagePart{
						Type: openai.ChatMessagePartTypeImageURL,
						ImageURL: &openai.ChatMessageImageURL{
							URL:    dataURL,
							Detail: openai.ImageURLDetailAuto,
						},
					})
				}
				// Add text if present
				if msg.Content != "" {
					parts = append(parts, openai.ChatMessagePart{
						Type: openai.ChatMessagePartTypeText,
						Text: msg.Content,
					})
				}
				result = append(result, openai.ChatCompletionMessage{
					Role:         openai.ChatMessageRoleUser,
					MultiContent: parts,
				})
			} else if msg.Content != "" {
				result = append(result, openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleUser,
					Content: msg.Content,
				})
			}

		case "assistant":
			// Flush any pending tool calls first
			if len(pendingToolCalls) > 0 {
				assistantMsg := openai.ChatCompletionMessage{
					Role:      openai.ChatMessageRoleAssistant,
					ToolCalls: pendingToolCalls,
				}
				if pendingReasoning != "" {
					assistantMsg.ReasoningContent = pendingReasoning
				}
				result = append(result, assistantMsg)
				pendingToolCalls = nil
				pendingReasoning = ""
			}

			if msg.Content != "" {
				result = append(result, openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleAssistant,
					Content: msg.Content,
				})
			}

		case "tool_use":
			// Check if this tool_use has a matching tool_result
			if !toolResultIDs[msg.ToolUseID] {
				// No matching result - convert to text description
				stats.droppedOrphans++
				stats.modified = true
				var inputStr string
				if msg.ToolInput != nil {
					inputStr = string(msg.ToolInput)
					if len(inputStr) > 500 {
						inputStr = inputStr[:500] + "..."
					}
				}
				text := fmt.Sprintf("[Called tool: %s]\nInput: %s", msg.ToolName, inputStr)
				result = append(result, openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleAssistant,
					Content: text,
				})
				continue
			}

			// Capture reasoning content from first tool_use in a series
			if msg.Thinking != "" && pendingReasoning == "" {
				pendingReasoning = msg.Thinking
			}

			// Accumulate tool call
			toolCall := openai.ToolCall{
				ID:   msg.ToolUseID,
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      msg.ToolName,
					Arguments: string(msg.ToolInput),
				},
			}
			pendingToolCalls = append(pendingToolCalls, toolCall)

			// Check if next message is NOT a tool_result for this call
			// If so, flush the tool calls now
			if i+1 >= len(messages) || messages[i+1].Role != "tool_result" {
				assistantMsg := openai.ChatCompletionMessage{
					Role:      openai.ChatMessageRoleAssistant,
					ToolCalls: pendingToolCalls,
				}
				if pendingReasoning != "" {
					assistantMsg.ReasoningContent = pendingReasoning
				}
				result = append(result, assistantMsg)
				pendingToolCalls = nil
				pendingReasoning = ""
				stats.mergedToolCalls++
				stats.modified = true
			}

		case "tool_result":
			// First, flush any pending tool calls
			if len(pendingToolCalls) > 0 {
				assistantMsg := openai.ChatCompletionMessage{
					Role:      openai.ChatMessageRoleAssistant,
					ToolCalls: pendingToolCalls,
				}
				if pendingReasoning != "" {
					assistantMsg.ReasoningContent = pendingReasoning
				}
				result = append(result, assistantMsg)
				pendingToolCalls = nil
				pendingReasoning = ""
			}

			// Check if this tool_result has a matching tool_use
			if !toolUseIDs[msg.ToolUseID] {
				// No matching tool_use - convert to text
				stats.droppedOrphans++
				stats.modified = true
				content := msg.Content
				if len(content) > 1000 {
					content = content[:1000] + "...[truncated]"
				}
				text := fmt.Sprintf("[Tool result for %s]\n%s", msg.ToolName, content)
				result = append(result, openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleUser,
					Content: text,
				})
				continue
			}

			// OpenAI uses "tool" role for tool results
			result = append(result, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    msg.Content,
				ToolCallID: msg.ToolUseID,
			})

		case "system":
			// System messages are handled separately
			continue

		default:
			// Unknown role - skip or convert to user
			if msg.Content != "" {
				result = append(result, openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleUser,
					Content: msg.Content,
				})
			}
		}
	}

	// Flush any remaining tool calls
	if len(pendingToolCalls) > 0 {
		assistantMsg := openai.ChatCompletionMessage{
			Role:      openai.ChatMessageRoleAssistant,
			ToolCalls: pendingToolCalls,
		}
		if pendingReasoning != "" {
			assistantMsg.ReasoningContent = pendingReasoning
		}
		result = append(result, assistantMsg)
	}

	return result, stats
}

// convertToOpenAITools converts tool definitions to OpenAI format
func convertToOpenAITools(toolDefs []types.ToolDefinition) []openai.Tool {
	if len(toolDefs) == 0 {
		return nil
	}

	result := make([]openai.Tool, len(toolDefs))
	for i, td := range toolDefs {
		result[i] = openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        td.Name,
				Description: td.Description,
				Parameters:  td.InputSchema,
			},
		}
	}
	return result
}

// estimateOpenAITokens provides a fallback token estimate when the API doesn't return usage
func estimateOpenAITokens(messages []openai.ChatCompletionMessage, systemPrompt string) int {
	// Use tiktoken-compatible estimation: ~4 chars per token
	// This is a rough estimate but better than 0
	total := 0
	
	// System prompt
	total += len(systemPrompt) / 4
	
	// Messages
	for _, msg := range messages {
		// Role overhead
		total += 4
		
		// Content
		total += len(msg.Content) / 4
		
		// Multi-content (images, etc.)
		for _, part := range msg.MultiContent {
			total += len(part.Text) / 4
			if part.ImageURL != nil {
				total += 85 // Base cost for an image reference
			}
		}
		
		// Tool calls
		for _, tc := range msg.ToolCalls {
			total += len(tc.Function.Name) / 4
			total += len(tc.Function.Arguments) / 4
			total += 10 // overhead
		}
		
		// Reasoning content
		total += len(msg.ReasoningContent) / 4
	}
	
	return total
}
