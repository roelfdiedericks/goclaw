// Package llm provides LLM client implementations.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	. "github.com/roelfdiedericks/goclaw/internal/metrics"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// AnthropicProvider implements the Provider interface for Anthropic's Claude API.
// Supports streaming, native tool calling, and prompt caching.
// Also works with Anthropic-compatible APIs (e.g., Kimi K2) via BaseURL.
type AnthropicProvider struct {
	name          string // Provider instance name (e.g., "anthropic")
	client        *anthropic.Client
	model         string
	maxTokens     int
	promptCaching bool
	apiKey        string // Stored for cloning
	baseURL       string // Custom API base URL (for Kimi K2, etc.)
	metricPrefix  string // e.g., "llm/anthropic/anthropic/claude-opus-4-5"
}

// Response represents the LLM response
type Response struct {
	Text       string          // accumulated text response
	ToolUseID  string          // if tool use requested
	ToolName   string
	ToolInput  json.RawMessage
	StopReason string          // "end_turn", "tool_use", etc.
	Thinking   string          // reasoning/thinking content (Kimi, Deepseek, etc.)

	InputTokens            int
	OutputTokens           int
	CacheCreationTokens    int // tokens used to create cache entry
	CacheReadTokens        int // tokens read from cache (saved cost!)
}

// HasToolUse returns true if the response contains a tool use request
func (r *Response) HasToolUse() bool {
	return r.ToolName != ""
}

// Note: AnthropicProvider does not yet fully implement Provider interface.
// StreamMessage signature needs to be updated. Legacy callers still work.
// TODO: Complete Provider implementation by updating StreamMessage signature.

// NewAnthropicProvider creates a new Anthropic provider from ProviderConfig.
// This is the preferred constructor for the unified provider system.
// Supports custom BaseURL for Anthropic-compatible APIs (e.g., Kimi K2).
func NewAnthropicProvider(name string, cfg ProviderConfig) (*AnthropicProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("anthropic API key not configured")
	}

	// Build client options
	opts := []option.RequestOption{option.WithAPIKey(cfg.APIKey)}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	client := anthropic.NewClient(opts...)

	maxTokens := cfg.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8192
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "(default)"
	}
	L_debug("anthropic provider created", "name", name, "baseURL", baseURL, "maxTokens", maxTokens, "promptCaching", cfg.PromptCaching)

	return &AnthropicProvider{
		name:          name,
		client:        &client,
		model:         "", // Model set via WithModel()
		maxTokens:     maxTokens,
		promptCaching: cfg.PromptCaching,
		apiKey:        cfg.APIKey,
		baseURL:       cfg.BaseURL,
	}, nil
}

// Name returns the provider instance name
func (p *AnthropicProvider) Name() string {
	return p.name
}

// Type returns the provider type
func (p *AnthropicProvider) Type() string {
	return "anthropic"
}

// Model returns the configured model name
func (p *AnthropicProvider) Model() string {
	return p.model
}

// WithModel returns a clone of the provider configured with a specific model
func (p *AnthropicProvider) WithModel(model string) Provider {
	clone := *p
	clone.model = model
	clone.metricPrefix = fmt.Sprintf("llm/%s/%s/%s", p.Type(), p.Name(), model)
	return &clone
}

// WithMaxTokens returns a clone of the provider with a different output limit
func (p *AnthropicProvider) WithMaxTokens(max int) Provider {
	clone := *p
	clone.maxTokens = max
	return &clone
}

// IsAvailable returns true if the provider is configured and ready
func (p *AnthropicProvider) IsAvailable() bool {
	return p != nil && p.client != nil && p.model != ""
}

// ContextTokens returns the model's context window size in tokens
func (p *AnthropicProvider) ContextTokens() int {
	return getModelContextWindow(p.model)
}

// MaxTokens returns the current output limit
func (p *AnthropicProvider) MaxTokens() int {
	return p.maxTokens
}

// Embed is not supported by Anthropic - returns error
func (p *AnthropicProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	return nil, ErrNotSupported{Provider: "anthropic", Operation: "embeddings"}
}

// EmbedBatch is not supported by Anthropic - returns error
func (p *AnthropicProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, ErrNotSupported{Provider: "anthropic", Operation: "embeddings"}
}

// EmbeddingDimensions returns 0 - Anthropic doesn't support embeddings
func (p *AnthropicProvider) EmbeddingDimensions() int {
	return 0
}

// SupportsEmbeddings returns false - Anthropic doesn't support embeddings
func (p *AnthropicProvider) SupportsEmbeddings() bool {
	return false
}

// getModelContextWindow returns the context window size for a given model
// Based on: https://docs.anthropic.com/en/docs/about-claude/models
// Standard context window is 200k for all Claude models
// (Extended 1M context is a separate beta feature)
func getModelContextWindow(_ string) int {
	return 200000
}

// SimpleMessage sends a simple user message and returns the response text.
// This is used for checkpoint/compaction summaries where we don't need tools.
func (c *AnthropicProvider) SimpleMessage(ctx context.Context, userMessage, systemPrompt string) (string, error) {
	messages := []types.Message{
		{Role: "user", Content: userMessage},
	}
	
	var result string
	_, err := c.StreamMessage(ctx, messages, nil, systemPrompt, func(delta string) {
		result += delta
	})
	if err != nil {
		return "", err
	}
	
	return result, nil
}

// StreamMessage sends a message to the LLM and streams the response
// onDelta is called for each text chunk received
func (c *AnthropicProvider) StreamMessage(
	ctx context.Context,
	messages []types.Message,
	toolDefs []types.ToolDefinition,
	systemPrompt string,
	onDelta func(delta string),
) (*Response, error) {
	startTime := time.Now()
	L_info("llm: request started", "provider", c.name, "model", c.model, "messages", len(messages), "tools", len(toolDefs))
	L_debug("preparing LLM request", "messages", len(messages), "tools", len(toolDefs))

	// Convert session messages to Anthropic format
	anthropicMessages := convertMessages(messages)

	// Repair tool_use/tool_result pairing before sending to API
	// This fixes orphaned results, duplicates, and missing results from merged history
	repairStart := time.Now()
	anthropicMessages, repairStats := repairToolPairing(anthropicMessages)
	repairDuration := time.Since(repairStart)
	if repairStats.modified {
		L_debug("repaired tool pairing",
			"droppedOrphans", repairStats.droppedOrphans,
			"droppedDuplicates", repairStats.droppedDuplicates,
			"insertedMissing", repairStats.insertedMissing,
			"sanitizedIDs", repairStats.sanitizedIDs,
			"duration", repairDuration)
	}

	// Convert tool definitions
	anthropicTools := convertTools(toolDefs)

	// Build request params
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: int64(c.maxTokens),
		Messages:  anthropicMessages,
	}

	// Add system prompt if provided
	if systemPrompt != "" {
		block := anthropic.TextBlockParam{Text: systemPrompt}
		if c.promptCaching {
			// Enable prompt caching - system prompt is stable and benefits from caching
			// Cache expires after 5 minutes of inactivity, reducing costs by up to 90%
			block.CacheControl = anthropic.NewCacheControlEphemeralParam()
			L_trace("system prompt set with caching", "length", len(systemPrompt))
		} else {
			L_trace("system prompt set (caching disabled)", "length", len(systemPrompt))
		}
		params.System = []anthropic.TextBlockParam{block}
	}

	// Add tools if any
	if len(anthropicTools) > 0 {
		params.Tools = anthropicTools
		L_trace("tools attached", "count", len(anthropicTools))
	}

	L_debug("sending request to Anthropic", "model", c.model)

	// Stream the response
	stream := c.client.Messages.NewStreaming(ctx, params)

	response := &Response{}
	message := anthropic.Message{}
	var firstTokenTime time.Time

	for stream.Next() {
		event := stream.Current()
		
		// Accumulate the message
		if err := message.Accumulate(event); err != nil {
			return nil, fmt.Errorf("accumulate error: %w", err)
		}

		// Handle different event types
		switch eventVariant := event.AsAny().(type) {
		case anthropic.ContentBlockDeltaEvent:
			switch deltaVariant := eventVariant.Delta.AsAny().(type) {
			case anthropic.TextDelta:
				// Capture time to first token
				if firstTokenTime.IsZero() {
					firstTokenTime = time.Now()
				}
				if onDelta != nil {
					onDelta(deltaVariant.Text)
				}
				response.Text += deltaVariant.Text
			}
		}
	}

	if err := stream.Err(); err != nil {
		L_error("stream error", "error", err)
		// Dump full error to file for debugging
		dumpAPIError(err, c.model, len(anthropicMessages), len(anthropicTools))
		// Record metrics for failed request
		if c.metricPrefix != "" {
			MetricDuration(c.metricPrefix, "request", time.Since(startTime))
			MetricFailWithReason(c.metricPrefix, "request_status", "stream_error")
		}
		return nil, fmt.Errorf("stream error: %w", err)
	}

	// Extract final information from accumulated message
	response.StopReason = string(message.StopReason)
	response.InputTokens = int(message.Usage.InputTokens)
	response.OutputTokens = int(message.Usage.OutputTokens)
	response.CacheCreationTokens = int(message.Usage.CacheCreationInputTokens)
	response.CacheReadTokens = int(message.Usage.CacheReadInputTokens)
	
	// Log with cache info if caching is active
	if response.CacheReadTokens > 0 || response.CacheCreationTokens > 0 {
		L_debug("response received (cache active)",
			"stopReason", response.StopReason,
			"inputTokens", response.InputTokens,
			"outputTokens", response.OutputTokens,
			"cacheCreated", response.CacheCreationTokens,
			"cacheRead", response.CacheReadTokens,
		)
	} else {
		L_debug("response received",
			"stopReason", response.StopReason,
			"inputTokens", response.InputTokens,
			"outputTokens", response.OutputTokens,
		)
	}

	// Check for tool use in the response
	for _, block := range message.Content {
		switch variant := block.AsAny().(type) {
		case anthropic.ToolUseBlock:
			response.ToolUseID = variant.ID
			response.ToolName = variant.Name
			// Marshal the input back to JSON
			inputBytes, _ := json.Marshal(variant.Input)
			response.ToolInput = inputBytes
			L_info("llm: tool use", "tool", variant.Name, "id", variant.ID)
		}
	}

	// Log request completion with timing and token stats
	duration := time.Since(startTime)
	if response.CacheReadTokens > 0 || response.CacheCreationTokens > 0 {
		L_info("llm: request completed", "provider", c.name, "duration", duration.Round(time.Millisecond),
			"inputTokens", response.InputTokens, "outputTokens", response.OutputTokens,
			"cacheRead", response.CacheReadTokens, "cacheCreated", response.CacheCreationTokens)
	} else {
		L_info("llm: request completed", "provider", c.name, "duration", duration.Round(time.Millisecond),
			"inputTokens", response.InputTokens, "outputTokens", response.OutputTokens)
	}

	// Record metrics
	if c.metricPrefix != "" {
		MetricDuration(c.metricPrefix, "request", duration)
		MetricAdd(c.metricPrefix, "input_tokens", int64(response.InputTokens))
		MetricAdd(c.metricPrefix, "output_tokens", int64(response.OutputTokens))
		if response.CacheReadTokens > 0 {
			MetricAdd(c.metricPrefix, "cache_read_tokens", int64(response.CacheReadTokens))
		}
		if response.CacheCreationTokens > 0 {
			MetricAdd(c.metricPrefix, "cache_creation_tokens", int64(response.CacheCreationTokens))
		}
		MetricOutcome(c.metricPrefix, "stop_reason", response.StopReason)
		MetricSuccess(c.metricPrefix, "request_status")

		// Time to first token (streaming latency)
		if !firstTokenTime.IsZero() {
			MetricDuration(c.metricPrefix, "time_to_first_token", firstTokenTime.Sub(startTime))
		}

		// Tool repair timing (always record, even if no repairs needed)
		MetricDuration(c.metricPrefix, "tool_repair", repairDuration)

		// Cache hit/miss tracking (only when caching is enabled)
		if c.promptCaching {
			if response.CacheReadTokens > 0 {
				MetricHit(c.metricPrefix, "prompt_cache")
			} else {
				MetricMiss(c.metricPrefix, "prompt_cache")
			}
		}

		// Tool use tracking
		if response.ToolName != "" {
			MetricOutcome(c.metricPrefix, "tool_requested", response.ToolName)
		}

		// Context usage as percentage of window (threshold at 80%)
		contextWindow := c.ContextTokens()
		if contextWindow > 0 && response.InputTokens > 0 {
			usagePct := float64(response.InputTokens) / float64(contextWindow) * 100
			MetricThreshold(c.metricPrefix, "context_usage_pct", usagePct, 80.0)
		}
	}

	return response, nil
}

// convertMessages converts session messages to Anthropic format
func convertMessages(messages []types.Message) []anthropic.MessageParam {
	// First pass: collect tool_use and tool_result IDs
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

	var result []anthropic.MessageParam
	convertedToolUses := 0
	convertedToolResults := 0

	for _, msg := range messages {
		switch msg.Role {
		case "user":
			// Skip messages with empty content and no images
			if msg.Content == "" && !msg.HasImages() {
				L_trace("skipping empty user message", "id", msg.ID)
				continue
			}

			// Build content blocks for user message
			var contentBlocks []anthropic.ContentBlockParamUnion

			// Add text block if content is not empty
			if msg.Content != "" {
				contentBlocks = append(contentBlocks, anthropic.NewTextBlock(msg.Content))
			}

			// Add image blocks if present
			for _, img := range msg.Images {
				imageBlock := anthropic.NewImageBlockBase64(img.MimeType, img.Data)
				contentBlocks = append(contentBlocks, imageBlock)
				L_trace("added image block to message", "mimeType", img.MimeType, "source", img.Source)
			}

			result = append(result, anthropic.NewUserMessage(contentBlocks...))

		case "assistant":
			// Skip messages with empty content (tool-only responses handled separately)
			if msg.Content == "" && msg.ToolName == "" {
				L_trace("skipping empty assistant message", "id", msg.ID)
				continue
			}
			if msg.Content != "" {
				result = append(result, anthropic.NewAssistantMessage(
					anthropic.NewTextBlock(msg.Content),
				))
			}

		case "tool_use":
			// If no corresponding tool_result, convert to text representation
			if !toolResultIDs[msg.ToolUseID] {
				L_trace("converting orphaned tool_use to text", "id", msg.ID, "tool", msg.ToolName)
				convertedToolUses++
				// Convert to assistant text message showing what tool was called
				var inputStr string
				if msg.ToolInput != nil {
					inputStr = string(msg.ToolInput)
					if len(inputStr) > 500 {
						inputStr = inputStr[:500] + "..."
					}
				}
				text := fmt.Sprintf("[Called tool: %s]\nInput: %s", msg.ToolName, inputStr)
				result = append(result, anthropic.NewAssistantMessage(
					anthropic.NewTextBlock(text),
				))
				continue
			}
			// Tool use is part of assistant message
			var input map[string]any
			json.Unmarshal(msg.ToolInput, &input)
			result = append(result, anthropic.NewAssistantMessage(
				anthropic.ContentBlockParamUnion{
					OfToolUse: &anthropic.ToolUseBlockParam{
						ID:    msg.ToolUseID,
						Name:  msg.ToolName,
						Input: input,
					},
				},
			))

		case "tool_result":
			// If no corresponding tool_use, convert to text representation
			if !toolUseIDs[msg.ToolUseID] {
				L_trace("converting orphaned tool_result to text", "id", msg.ID, "tool", msg.ToolName)
				convertedToolResults++
				// Convert to user text message showing the result
				content := msg.Content
				if len(content) > 1000 {
					content = content[:1000] + "...[truncated]"
				}
				text := fmt.Sprintf("[Tool result for %s]\n%s", msg.ToolName, content)
				result = append(result, anthropic.NewUserMessage(
					anthropic.NewTextBlock(text),
				))
				continue
			}
			// Tool results must have content
			content := msg.Content
			if content == "" {
				content = "[empty result]"
			}
			result = append(result, anthropic.NewUserMessage(
				anthropic.NewToolResultBlock(msg.ToolUseID, content, false),
			))
		}
	}

	if convertedToolUses > 0 || convertedToolResults > 0 {
		L_debug("converted orphaned tool messages to text",
			"toolUses", convertedToolUses,
			"toolResults", convertedToolResults)
	}

	return result
}

// convertTools converts our tool definitions to Anthropic format
func convertTools(defs []types.ToolDefinition) []anthropic.ToolUnionParam {
	result := make([]anthropic.ToolUnionParam, 0, len(defs))

	for _, def := range defs {
		// Extract properties from the schema
		var properties any
		if props, ok := def.InputSchema["properties"]; ok {
			properties = props
		}

		param := anthropic.ToolInputSchemaParam{
			Properties: properties,
		}

		result = append(result, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        def.Name,
				Description: anthropic.String(def.Description),
				InputSchema: param,
			},
		})
	}

	return result
}

// dumpAPIError writes API error details to apierror.txt for debugging
func dumpAPIError(err error, model string, messageCount, toolCount int) {
	content := fmt.Sprintf(`Anthropic API Error
====================
Timestamp: %s
Model: %s
Messages: %d
Tools: %d

Error:
%v

Full Error String:
%s
`,
		time.Now().Format(time.RFC3339),
		model,
		messageCount,
		toolCount,
		err,
		err.Error(),
	)

	if writeErr := os.WriteFile("apierror.txt", []byte(content), 0644); writeErr != nil {
		L_warn("failed to write apierror.txt", "error", writeErr)
	} else {
		L_info("API error dumped to apierror.txt")
	}
}

// repairStats contains statistics about tool pairing repairs
type repairStats struct {
	modified          bool
	droppedOrphans    int
	droppedDuplicates int
	insertedMissing   int
	sanitizedIDs      int
}

// validToolIDPattern matches Anthropic's required format: ^[a-zA-Z0-9_-]+$
var validToolIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// isValidToolID checks if the ID matches Anthropic's required pattern
func isValidToolID(id string) bool {
	return id != "" && validToolIDPattern.MatchString(id)
}

// sanitizeToolID ensures the ID matches Anthropic's pattern ^[a-zA-Z0-9_-]+$
// Invalid characters are replaced with underscores
func sanitizeToolID(id string) string {
	if isValidToolID(id) {
		return id
	}
	var result strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-' {
			result.WriteRune(r)
		} else {
			result.WriteRune('_')
		}
	}
	// Ensure non-empty result
	if result.Len() == 0 {
		return "tool_" + fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return result.String()
}

// repairToolPairing fixes tool_use/tool_result pairing issues in the message stream.
// Anthropic requires that each tool_result must immediately follow its matching tool_use.
// This function uses a two-pass approach:
// Pass 1: Scan all messages to build complete inventory of tool_uses and tool_results
// Pass 2: Reconstruct messages ensuring proper pairing:
// - For each tool_use, find its result from anywhere in the history and place it immediately after
// - Insert synthetic error results only for tool_uses with NO result anywhere
// - Drop orphaned tool_results that have no matching tool_use
func repairToolPairing(messages []anthropic.MessageParam) ([]anthropic.MessageParam, repairStats) {
	var stats repairStats

	// === PASS 1: Build complete inventory with sanitized IDs ===
	// Map sanitized tool_use ID -> the tool_use block (with sanitized ID)
	allToolUses := make(map[string]anthropic.ToolUseBlockParam)
	// Map sanitized tool_result ID -> the tool_result block (with sanitized ID)
	allToolResults := make(map[string]anthropic.ToolResultBlockParam)
	// Map original ID -> sanitized ID (for cross-provider compatibility)
	idMapping := make(map[string]string)

	for _, msg := range messages {
		for _, block := range msg.Content {
			if block.OfToolUse != nil {
				originalID := block.OfToolUse.ID
				sanitizedID := sanitizeToolID(originalID)
				if sanitizedID != originalID {
					stats.sanitizedIDs++
					stats.modified = true
				}
				idMapping[originalID] = sanitizedID
				// Store with sanitized ID
				toolUse := *block.OfToolUse
				toolUse.ID = sanitizedID
				allToolUses[sanitizedID] = toolUse
			}
			if block.OfToolResult != nil {
				originalID := block.OfToolResult.ToolUseID
				sanitizedID := sanitizeToolID(originalID)
				if sanitizedID != originalID {
					stats.sanitizedIDs++
					stats.modified = true
				}
				idMapping[originalID] = sanitizedID
				// Keep the first occurrence (in case of duplicates)
				if _, exists := allToolResults[sanitizedID]; !exists {
					toolResult := *block.OfToolResult
					toolResult.ToolUseID = sanitizedID
					allToolResults[sanitizedID] = toolResult
				}
			}
		}
	}

	L_trace("repair pass 1 complete", "toolUses", len(allToolUses), "toolResults", len(allToolResults), "sanitizedIDs", stats.sanitizedIDs)

	// === PASS 2: Reconstruct with proper pairing ===
	var result []anthropic.MessageParam
	usedToolResultIDs := make(map[string]bool) // Track which results we've already placed (uses sanitized IDs)

	for _, msg := range messages {
		// Handle assistant messages - ensure tool_uses have results immediately after
		if msg.Role == anthropic.MessageParamRoleAssistant {
			// Rebuild assistant message with sanitized tool IDs
			var newContent []anthropic.ContentBlockParamUnion
			for _, block := range msg.Content {
				if block.OfToolUse != nil {
					// Use sanitized ID
					sanitizedID := sanitizeToolID(block.OfToolUse.ID)
					toolUse := *block.OfToolUse
					toolUse.ID = sanitizedID
					newContent = append(newContent, anthropic.ContentBlockParamUnion{
						OfToolUse: &toolUse,
					})
				} else {
					newContent = append(newContent, block)
				}
			}
			newMsg := anthropic.MessageParam{
				Role:    msg.Role,
				Content: newContent,
			}
			result = append(result, newMsg)

			// Extract sanitized tool IDs
			toolIDs := make(map[string]bool)
			for _, block := range newContent {
				if block.OfToolUse != nil {
					toolIDs[block.OfToolUse.ID] = true
				}
			}

			if len(toolIDs) == 0 {
				continue
			}

			// Build the tool_result message that must follow
			var toolResults []anthropic.ContentBlockParamUnion
			for sanitizedID := range toolIDs {
				if usedToolResultIDs[sanitizedID] {
					// Already placed this result (shouldn't happen, but safety check)
					continue
				}

				if tr, exists := allToolResults[sanitizedID]; exists {
					// Found the result in history - use it (already has sanitized ID)
					toolResults = append(toolResults, anthropic.ContentBlockParamUnion{
						OfToolResult: &tr,
					})
					usedToolResultIDs[sanitizedID] = true
					stats.modified = true
					L_trace("relocated tool_result to proper position", "toolID", sanitizedID)
				} else {
					// No result anywhere - insert synthetic
					toolResults = append(toolResults, anthropic.ContentBlockParamUnion{
						OfToolResult: &anthropic.ToolResultBlockParam{
							ToolUseID: sanitizedID,
							Content: []anthropic.ToolResultBlockParamContentUnion{
								{
									OfText: &anthropic.TextBlockParam{
										Text: "[goclaw] missing tool result in session history; inserted synthetic error result for transcript repair.",
									},
								},
							},
							IsError: anthropic.Bool(true),
						},
					})
					usedToolResultIDs[sanitizedID] = true
					stats.insertedMissing++
					stats.modified = true
					L_trace("inserted synthetic tool_result", "toolID", sanitizedID)
				}
			}

			if len(toolResults) > 0 {
				result = append(result, anthropic.MessageParam{
					Role:    anthropic.MessageParamRoleUser,
					Content: toolResults,
				})
			}
			continue
		}

		// Handle user messages - filter out tool_results (we handle them above)
		if msg.Role == anthropic.MessageParamRoleUser {
			var nonToolResultBlocks []anthropic.ContentBlockParamUnion

			for _, block := range msg.Content {
				if block.OfToolResult != nil {
					// Use sanitized ID for lookup
					sanitizedID := sanitizeToolID(block.OfToolResult.ToolUseID)
					if usedToolResultIDs[sanitizedID] {
						// Already placed by the assistant message handler
						continue
					}
					// Orphaned result - no matching tool_use exists
					if _, hasToolUse := allToolUses[sanitizedID]; !hasToolUse {
						stats.droppedOrphans++
						stats.modified = true
						L_trace("dropped orphaned tool_result", "toolID", sanitizedID)
						continue
					}
					// This shouldn't happen - result exists but wasn't used?
					// Keep it to avoid data loss (with sanitized ID)
					L_warn("unexpected tool_result not yet placed", "toolID", sanitizedID)
					toolResult := *block.OfToolResult
					toolResult.ToolUseID = sanitizedID
					nonToolResultBlocks = append(nonToolResultBlocks, anthropic.ContentBlockParamUnion{
						OfToolResult: &toolResult,
					})
					usedToolResultIDs[sanitizedID] = true
				} else {
					nonToolResultBlocks = append(nonToolResultBlocks, block)
				}
			}

			// Only add the message if it has content
			if len(nonToolResultBlocks) > 0 {
				result = append(result, anthropic.MessageParam{
					Role:    anthropic.MessageParamRoleUser,
					Content: nonToolResultBlocks,
				})
			}
			continue
		}

		// Other roles - keep as-is
		result = append(result, msg)
	}

	// Count duplicates that were skipped (results that appeared multiple times in input)
	for toolID := range allToolResults {
		count := 0
		for _, msg := range messages {
			for _, block := range msg.Content {
				if block.OfToolResult != nil && block.OfToolResult.ToolUseID == toolID {
					count++
				}
			}
		}
		if count > 1 {
			stats.droppedDuplicates += count - 1
			stats.modified = true
		}
	}

	L_trace("repair pass 2 complete", "inputMessages", len(messages), "outputMessages", len(result))

	return result, stats
}

// immediatelyFollows checks if a tool_result for toolID immediately follows the given assistant message
// in the original message list (used for logging whether we relocated a result)
func immediatelyFollows(messages []anthropic.MessageParam, assistantMsg anthropic.MessageParam, toolID string) bool {
	for i, msg := range messages {
		if msg.Role == assistantMsg.Role && len(msg.Content) == len(assistantMsg.Content) {
			// Found the assistant message, check next
			if i+1 < len(messages) {
				nextMsg := messages[i+1]
				if nextMsg.Role == anthropic.MessageParamRoleUser {
					for _, block := range nextMsg.Content {
						if block.OfToolResult != nil && block.OfToolResult.ToolUseID == toolID {
							return true
						}
					}
				}
			}
			return false
		}
	}
	return false
}

// extractToolUseIDs extracts all tool_use IDs from an assistant message
func extractToolUseIDs(msg anthropic.MessageParam) map[string]bool {
	ids := make(map[string]bool)
	for _, block := range msg.Content {
		if block.OfToolUse != nil {
			ids[block.OfToolUse.ID] = true
		}
	}
	return ids
}
