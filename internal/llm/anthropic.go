// Package llm provides LLM client implementations.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/roelfdiedericks/goclaw/internal/config"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/session"
	"github.com/roelfdiedericks/goclaw/internal/tools"
)

// Client wraps the Anthropic API client
type Client struct {
	client        *anthropic.Client
	model         string
	maxTokens     int
	promptCaching bool
}

// Response represents the LLM response
type Response struct {
	Text       string          // accumulated text response
	ToolUseID  string          // if tool use requested
	ToolName   string
	ToolInput  json.RawMessage
	StopReason string          // "end_turn", "tool_use", etc.

	InputTokens            int
	OutputTokens           int
	CacheCreationTokens    int // tokens used to create cache entry
	CacheReadTokens        int // tokens read from cache (saved cost!)
}

// HasToolUse returns true if the response contains a tool use request
func (r *Response) HasToolUse() bool {
	return r.ToolName != ""
}

// NewClient creates a new Anthropic client
func NewClient(cfg *config.LLMConfig) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("anthropic API key not configured")
	}

	client := anthropic.NewClient(option.WithAPIKey(cfg.APIKey))

	model := cfg.Model
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	maxTokens := cfg.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8192
	}

	L_debug("anthropic client created", "model", model, "maxTokens", maxTokens, "promptCaching", cfg.PromptCaching)

	return &Client{
		client:        &client,
		model:         model,
		maxTokens:     maxTokens,
		promptCaching: cfg.PromptCaching,
	}, nil
}

// NewClientWithModel creates a client using explicit API key and model.
// Useful for creating fallback clients with different models.
func NewClientWithModel(apiKey, model string, maxTokens int) (*Client, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("anthropic API key not provided")
	}
	if model == "" {
		return nil, fmt.Errorf("model not specified")
	}
	if maxTokens == 0 {
		maxTokens = 4096 // Conservative default for summarization
	}

	client := anthropic.NewClient(option.WithAPIKey(apiKey))

	L_debug("anthropic fallback client created", "model", model, "maxTokens", maxTokens)

	return &Client{
		client:        &client,
		model:         model,
		maxTokens:     maxTokens,
		promptCaching: false, // Fallback doesn't need caching
	}, nil
}

// Model returns the configured model name
func (c *Client) Model() string {
	return c.model
}

// IsAvailable returns true if the client is configured and ready
func (c *Client) IsAvailable() bool {
	return c != nil && c.client != nil
}

// ContextTokens returns the model's context window size in tokens
func (c *Client) ContextTokens() int {
	return getModelContextWindow(c.model)
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
func (c *Client) SimpleMessage(ctx context.Context, userMessage, systemPrompt string) (string, error) {
	messages := []session.Message{
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
func (c *Client) StreamMessage(
	ctx context.Context,
	messages []session.Message,
	toolDefs []tools.ToolDefinition,
	systemPrompt string,
	onDelta func(delta string),
) (*Response, error) {
	L_debug("preparing LLM request", "messages", len(messages), "tools", len(toolDefs))

	// Convert session messages to Anthropic format
	anthropicMessages := convertMessages(messages)

	// Repair tool_use/tool_result pairing before sending to API
	// This fixes orphaned results, duplicates, and missing results from merged history
	anthropicMessages, repairStats := repairToolPairing(anthropicMessages)
	if repairStats.modified {
		L_debug("repaired tool pairing",
			"droppedOrphans", repairStats.droppedOrphans,
			"droppedDuplicates", repairStats.droppedDuplicates,
			"insertedMissing", repairStats.insertedMissing)
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
			L_debug("tool use requested", "tool", variant.Name, "id", variant.ID)
		}
	}

	return response, nil
}

// convertMessages converts session messages to Anthropic format
func convertMessages(messages []session.Message) []anthropic.MessageParam {
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
func convertTools(defs []tools.ToolDefinition) []anthropic.ToolUnionParam {
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
}

// repairToolPairing fixes tool_use/tool_result pairing issues in the message stream.
// Anthropic requires that each tool_result must immediately follow its matching tool_use.
// This function:
// - Drops orphaned tool_results (no matching tool_use in previous assistant message)
// - Drops duplicate tool_results for the same tool_use_id
// - Inserts synthetic error results for tool_uses with no result
func repairToolPairing(messages []anthropic.MessageParam) ([]anthropic.MessageParam, repairStats) {
	var stats repairStats
	var result []anthropic.MessageParam
	seenToolResultIDs := make(map[string]bool)

	for i := 0; i < len(messages); i++ {
		msg := messages[i]

		// Handle user messages - check for tool_results
		if msg.Role == anthropic.MessageParamRoleUser {
			// Check if this user message contains tool_result blocks
			hasToolResult := false
			var validBlocks []anthropic.ContentBlockParamUnion
			var toolResultBlocks []anthropic.ToolResultBlockParam

			for _, block := range msg.Content {
				if block.OfToolResult != nil {
					hasToolResult = true
					toolResultBlocks = append(toolResultBlocks, *block.OfToolResult)
				} else {
					validBlocks = append(validBlocks, block)
				}
			}

			if !hasToolResult {
				// No tool results, keep message as-is
				result = append(result, msg)
				continue
			}

			// Get the previous message to check for matching tool_uses
			var prevAssistantToolIDs map[string]bool
			if len(result) > 0 {
				prevMsg := result[len(result)-1]
				if prevMsg.Role == anthropic.MessageParamRoleAssistant {
					prevAssistantToolIDs = extractToolUseIDs(prevMsg)
				}
			}

			// Filter tool results - keep only those matching previous assistant's tool_uses
			var keptToolResults []anthropic.ContentBlockParamUnion
			for _, tr := range toolResultBlocks {
				toolID := tr.ToolUseID
				
				// Check if duplicate
				if seenToolResultIDs[toolID] {
					stats.droppedDuplicates++
					stats.modified = true
					continue
				}

				// Check if orphan (no matching tool_use)
				if prevAssistantToolIDs == nil || !prevAssistantToolIDs[toolID] {
					stats.droppedOrphans++
					stats.modified = true
					continue
				}

				// Valid tool result
				seenToolResultIDs[toolID] = true
				keptToolResults = append(keptToolResults, anthropic.ContentBlockParamUnion{
					OfToolResult: &tr,
				})
			}

			// Build the repaired message
			if len(keptToolResults) > 0 {
				// Tool results go in their own user message
				result = append(result, anthropic.MessageParam{
					Role:    anthropic.MessageParamRoleUser,
					Content: keptToolResults,
				})
			}
			
			// If there were other blocks (text), add them separately
			if len(validBlocks) > 0 {
				result = append(result, anthropic.MessageParam{
					Role:    anthropic.MessageParamRoleUser,
					Content: validBlocks,
				})
			}
			continue
		}

		// Handle assistant messages - check for tool_uses that need results
		if msg.Role == anthropic.MessageParamRoleAssistant {
			result = append(result, msg)
			toolIDs := extractToolUseIDs(msg)
			
			if len(toolIDs) == 0 {
				continue
			}

			// Look ahead to find tool_results for these tool_uses
			foundResults := make(map[string]bool)
			if i+1 < len(messages) {
				nextMsg := messages[i+1]
				if nextMsg.Role == anthropic.MessageParamRoleUser {
					for _, block := range nextMsg.Content {
						if block.OfToolResult != nil {
							foundResults[block.OfToolResult.ToolUseID] = true
						}
					}
				}
			}

			// Check for missing results and insert synthetic ones
			var syntheticResults []anthropic.ContentBlockParamUnion
			for toolID := range toolIDs {
				if !foundResults[toolID] && !seenToolResultIDs[toolID] {
					// Insert synthetic error result
					syntheticResults = append(syntheticResults, anthropic.ContentBlockParamUnion{
						OfToolResult: &anthropic.ToolResultBlockParam{
							ToolUseID: toolID,
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
					seenToolResultIDs[toolID] = true
					stats.insertedMissing++
					stats.modified = true
				}
			}

			if len(syntheticResults) > 0 {
				result = append(result, anthropic.MessageParam{
					Role:    anthropic.MessageParamRoleUser,
					Content: syntheticResults,
				})
			}
			continue
		}

		// Other roles - keep as-is
		result = append(result, msg)
	}

	return result, stats
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
