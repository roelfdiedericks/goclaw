// Package llm provides LLM client implementations.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	. "github.com/roelfdiedericks/goclaw/internal/metrics"
	"github.com/roelfdiedericks/goclaw/internal/tokens"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// thinkingUnsupportedModels caches models that don't support extended thinking.
// This is populated when a thinking request fails with an unsupported error.
var thinkingUnsupportedModels sync.Map // map[modelName]bool

// modelMaxOutputTokens caches learned max output token limits for models.
// Populated when a max_tokens error reveals the model's actual limit.
// Key: model name, Value: max output tokens (int)
var modelMaxOutputTokens sync.Map

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
	traceEnabled  bool   // Per-provider trace logging control

	// HTTP transport for capturing request/response (for error dumps)
	transport     *CapturingTransport
	dumpOnSuccess bool // Keep dumps even on success (for debugging)
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

	// Create capturing transport for request/response debugging
	transport := &CapturingTransport{Base: http.DefaultTransport}
	httpClient := &http.Client{Transport: transport}

	// Build client options
	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
		option.WithHTTPClient(httpClient),
	}
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

	// Determine trace enabled - default to true if not explicitly set to false
	traceEnabled := true
	if cfg.Trace != nil && !*cfg.Trace {
		traceEnabled = false
	}

	L_debug("anthropic provider created", "name", name, "baseURL", baseURL, "maxTokens", maxTokens, "promptCaching", cfg.PromptCaching, "trace", traceEnabled)

	return &AnthropicProvider{
		name:          name,
		client:        &client,
		model:         "", // Model set via WithModel()
		maxTokens:     maxTokens,
		promptCaching: cfg.PromptCaching,
		apiKey:        cfg.APIKey,
		baseURL:       cfg.BaseURL,
		traceEnabled:  traceEnabled,
		transport:     transport,
		dumpOnSuccess: cfg.DumpOnSuccess,
	}, nil
}

// trace logs a trace message if tracing is enabled for this provider.
// Use this instead of L_trace for per-provider trace control.
func (p *AnthropicProvider) trace(msg string, args ...any) {
	if p.traceEnabled {
		L_trace(msg, args...)
	}
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
	}, nil)
	if err != nil {
		return "", err
	}
	
	return result, nil
}

// StreamMessage sends a message to the LLM and streams the response
// onDelta is called for each text chunk received
// opts can be nil for default behavior
func (c *AnthropicProvider) StreamMessage(
	ctx context.Context,
	messages []types.Message,
	toolDefs []types.ToolDefinition,
	systemPrompt string,
	onDelta func(delta string),
	opts *StreamOptions,
) (*Response, error) {
	startTime := time.Now()

	// Determine if we should try extended thinking
	enableThinking := false
	thinkingBudget := 10000 // default
	if opts != nil {
		// Use ThinkingLevel if set, fall back to legacy EnableThinking
		level := ThinkingLevel(opts.ThinkingLevel)
		if level == "" && opts.EnableThinking {
			// Legacy: EnableThinking without level
			level = DefaultThinkingLevel
		}

		if level.IsEnabled() {
			// Check if model is known to not support thinking
			if _, unsupported := thinkingUnsupportedModels.Load(c.model); unsupported {
				L_info("llm: thinking requested but model doesn't support it", "model", c.model, "level", level)
			} else {
				enableThinking = true
				// Use explicit budget if set, otherwise compute from level
				if opts.ThinkingBudget > 0 {
					thinkingBudget = opts.ThinkingBudget
				} else {
					thinkingBudget = level.AnthropicBudgetTokens()
				}
			}
		}
	}
	
	contextWindow := c.ContextTokens()
	L_info("llm: request started", "provider", c.name, "model", c.model, "messages", len(messages), "tools", len(toolDefs), "thinking", enableThinking)
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

	// Estimate input tokens and cap max_tokens to fit within context window
	estimator := tokens.Get()
	estimatedInput := 0
	for _, m := range messages {
		estimatedInput += estimator.CountWithOverhead(m.Content, 4)
	}
	estimatedInput += estimator.Count(systemPrompt)
	maxTokens := tokens.CapMaxTokens(c.maxTokens, contextWindow, estimatedInput, 100)
	if maxTokens != c.maxTokens {
		L_debug("anthropic: capped max_tokens to fit context",
			"provider", c.name,
			"original", c.maxTokens,
			"capped", maxTokens,
			"contextWindow", contextWindow,
			"estimatedInput", estimatedInput)
	}

	// Check if we have a cached output limit for this model
	if cachedLimit, ok := modelMaxOutputTokens.Load(c.model); ok {
		limit := cachedLimit.(int)
		if maxTokens > limit {
			L_debug("anthropic: capping max_tokens to cached model limit",
				"model", c.model,
				"requested", maxTokens,
				"limit", limit)
			maxTokens = limit
		}
	}

	// Add extended thinking if enabled
	// max_tokens must be greater than thinking.budget_tokens
	if enableThinking {
		minRequired := thinkingBudget + 4096 // budget + buffer for actual output
		if maxTokens < minRequired {
			L_debug("llm: adjusting max_tokens for thinking", "original", maxTokens, "required", minRequired)
			maxTokens = minRequired
		}
	}

	// Build request params
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: int64(maxTokens),
		Messages:  anthropicMessages,
	}

	// Add extended thinking if enabled
	if enableThinking {
		params.Thinking = anthropic.ThinkingConfigParamOfEnabled(int64(thinkingBudget))
		L_info("llm: extended thinking enabled", "model", c.model, "budget", thinkingBudget, "maxTokens", maxTokens)
	}

	// Add system prompt if provided
	if systemPrompt != "" {
		block := anthropic.TextBlockParam{Text: systemPrompt}
		if c.promptCaching {
			// Enable prompt caching - system prompt is stable and benefits from caching
			// Cache expires after 5 minutes of inactivity, reducing costs by up to 90%
			block.CacheControl = anthropic.NewCacheControlEphemeralParam()
			c.trace("system prompt set with caching", "length", len(systemPrompt))
		} else {
			c.trace("system prompt set (caching disabled)", "length", len(systemPrompt))
		}
		params.System = []anthropic.TextBlockParam{block}
	}

	// Add tools if any
	if len(anthropicTools) > 0 {
		params.Tools = anthropicTools
		c.trace("tools attached", "count", len(anthropicTools))
	}

	L_debug("sending request to Anthropic", "model", c.model)

	// Start dump for debugging (captures request context)
	dumpCtx := StartDump(c.name, c.model, c.baseURL, anthropicMessages, anthropicTools, systemPrompt, 1)
	dumpCtx.SetTokenInfo(TokenInfo{
		ContextWindow:  contextWindow,
		EstimatedInput: estimatedInput,
		ConfiguredMax:  c.maxTokens,
		CappedMax:      maxTokens,
		SafetyMargin:   tokens.SafetyMargin,
		Buffer:         100,
	})

	// Create per-request capture for concurrency safety
	reqCapture := NewRequestCapture()
	dumpCtx.SetRequestCapture(reqCapture)
	captureCtx := WithRequestCapture(ctx, reqCapture)

	// Stream the response
	stream := c.client.Messages.NewStreaming(captureCtx, params)

	response := &Response{}
	message := anthropic.Message{}
	var firstTokenTime time.Time
	var thinkingContent strings.Builder

	for stream.Next() {
		event := stream.Current()
		
		// Accumulate the message
		if err := message.Accumulate(event); err != nil {
			FinishDumpError(dumpCtx, err, c.transport)
			// Check if response body contains real error (e.g., context overflow)
			if reqCapture != nil {
				_, respBody, _, _ := reqCapture.GetData()
				err = CheckResponseBody(err, respBody)
			}
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
			case anthropic.ThinkingDelta:
				// Accumulate thinking content
				if thinkingContent.Len() == 0 {
					L_info("llm: thinking started (streaming reasoning content)")
				}
				thinkingContent.WriteString(deltaVariant.Thinking)
				// Stream thinking delta to callback if provided
				if opts != nil && opts.OnThinkingDelta != nil {
					opts.OnThinkingDelta(deltaVariant.Thinking)
				}
				c.trace("llm: thinking delta received", "length", len(deltaVariant.Thinking))
			}
		}
	}

	if err := stream.Err(); err != nil {
		errStr := err.Error()

		// Check if this is a max_tokens limit error
		// Parse the limit from the error, cache it, and retry with capped value
		if isMaxTokens, parsedLimit := ParseMaxTokensLimit(errStr); isMaxTokens && parsedLimit > 0 {
			L_warn("anthropic: max_tokens exceeds model limit, caching and retrying",
				"model", c.model,
				"requestedTokens", maxTokens,
				"modelLimit", parsedLimit)
			modelMaxOutputTokens.Store(c.model, parsedLimit)
			// Retry - the cached limit will be applied on retry
			return c.StreamMessage(ctx, messages, toolDefs, systemPrompt, onDelta, opts)
		}

		// Check if this is a "thinking not supported" error
		// Anthropic returns errors like "thinking is not supported for this model"
		if enableThinking && isThinkingNotSupportedError(errStr) {
			L_warn("llm: model doesn't support extended thinking, disabling for future requests",
				"model", c.model, "error", errStr)
			thinkingUnsupportedModels.Store(c.model, true)

			// Retry without thinking
			disabledOpts := &StreamOptions{EnableThinking: false}
			return c.StreamMessage(ctx, messages, toolDefs, systemPrompt, onDelta, disabledOpts)
		}

		L_error("stream error", "error", err)
		// Dump full request/response to file for debugging
		FinishDumpError(dumpCtx, err, c.transport)
		// Check if response body contains real error (e.g., context overflow)
		if reqCapture != nil {
			_, respBody, _, _ := reqCapture.GetData()
			err = CheckResponseBody(err, respBody)
		}
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

	// Check for tool use and thinking in the response
	for _, block := range message.Content {
		switch variant := block.AsAny().(type) {
		case anthropic.ToolUseBlock:
			response.ToolUseID = variant.ID
			response.ToolName = variant.Name
			// Marshal the input back to JSON
			inputBytes, _ := json.Marshal(variant.Input)
			response.ToolInput = inputBytes
			L_info("llm: tool use", "tool", variant.Name, "id", variant.ID)
		case anthropic.ThinkingBlock:
			// Capture thinking content from final block
			if variant.Thinking != "" {
				response.Thinking = variant.Thinking
				L_info("llm: thinking completed", "length", len(variant.Thinking))
			}
		}
	}
	
	// If we accumulated thinking from deltas but didn't get a final block, use that
	if response.Thinking == "" && thinkingContent.Len() > 0 {
		response.Thinking = thinkingContent.String()
		L_info("llm: thinking completed (from stream)", "length", len(response.Thinking))
	}

	// Log request completion with timing and token stats
	duration := time.Since(startTime)
	usagePercent := 0.0
	if contextWindow > 0 {
		usagePercent = float64(response.InputTokens) / float64(contextWindow) * 100.0
	}
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

		// Context window metrics (contextWindow/usagePercent calculated above)
		if contextWindow > 0 {
			MetricSet(c.metricPrefix, "context_window", int64(contextWindow))
			MetricSet(c.metricPrefix, "context_used", int64(response.InputTokens))
			MetricThreshold(c.metricPrefix, "context_usage_percent", usagePercent, 100.0)
		}
	}

	// Finalize dump (delete on success unless dumpOnSuccess is enabled)
	FinishDumpSuccess(dumpCtx, c.dumpOnSuccess)

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
					// If we get here, the tool_result wasn't placed by the assistant handler.
					// This means the assistant message containing the matching tool_use
					// isn't in the output (likely removed by compaction).
					// Drop it - it's orphaned from Anthropic's API perspective.
					stats.droppedOrphans++
					stats.modified = true
					L_debug("dropped orphaned tool_result (tool_use not in output)", "toolID", sanitizedID)
					continue
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

// isThinkingNotSupportedError checks if an error indicates the model doesn't support thinking
// Uses specific phrase matching to avoid false positives from generic errors
func isThinkingNotSupportedError(errStr string) bool {
	errLower := strings.ToLower(errStr)
	// Only match very specific error phrases about thinking not being supported
	// Avoid broad patterns that could match unrelated errors
	specificPhrases := []string{
		"thinking is not supported",
		"thinking not supported",
		"thinking is not available",
		"thinking not available",
		"does not support thinking",
		"doesn't support thinking",
		"model does not support extended thinking",
		"extended thinking is not supported",
		"extended thinking not supported",
	}
	for _, phrase := range specificPhrases {
		if strings.Contains(errLower, phrase) {
			return true
		}
	}
	return false
}
