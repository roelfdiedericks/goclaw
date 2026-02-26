// Package llm provides LLM client implementations.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/metadata"
	. "github.com/roelfdiedericks/goclaw/internal/metrics"
	"github.com/roelfdiedericks/goclaw/internal/types"
	"github.com/roelfdiedericks/xai-go"
)

// safeInt32 converts int to int32 with bounds checking to prevent overflow.
func safeInt32(n int) int32 {
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	if n < math.MinInt32 {
		return math.MinInt32
	}
	return int32(n)
}

// XAIProvider implements the Provider interface for xAI's Grok API.
// Supports streaming, native tool calling, server-side tools, and context preservation.
// Also implements StatefulProvider for session-scoped state (responseID).
type XAIProvider struct {
	name             string            // Provider instance name (e.g., "xai")
	config           LLMProviderConfig // Full provider configuration
	model            string            // Current model (e.g., "grok-4-1-fast-reasoning")
	maxTokens        int               // Output token limit
	metadataProvider string            // models.json provider ID ("xai")
	metricPrefix     string            // e.g., "llm/xai/xai/grok-4-1-fast-reasoning"

	// Client management (lazy initialization)
	client   *xai.Client
	clientMu sync.Mutex

	// Context preservation state (saved/loaded via StatefulProvider)
	responseID       string // xAI's previous_response_id for context
	lastMessageCount int    // Message count at last successful stream
}

// clientToolPrefix is added to client tool names that conflict with xAI server tools.
// This allows both tools to be available to the LLM. The prefix is stripped when
// processing tool calls, so persisted data uses the canonical tool name.
const clientToolPrefix = "local_"

// =============================================================================
// Model Info Cache - Fetched from API on startup, fallback to hardcoded
// =============================================================================

// xaiModelInfo holds cached model information from the API.
type xaiModelInfo struct {
	ContextTokens int     // MaxPromptLength from API
	InputPrice    float64 // USD per million tokens
	OutputPrice   float64 // USD per million tokens
}

var (
	// xaiModelInfoCache holds model info fetched from API (model name -> info)
	xaiModelInfoCache   = make(map[string]*xaiModelInfo)
	xaiModelInfoCacheMu sync.RWMutex
	xaiModelInfoFetched bool // true once we've attempted to fetch from API
)

// xaiModelContextFallback contains hardcoded context sizes as fallback
// Source: https://console.x.ai/ model availability page (2026-02)
var xaiModelContextFallback = map[string]int{
	// Grok-4-1 series: 2M context
	"grok-4-1-fast-reasoning":     2000000,
	"grok-4-1-fast-non-reasoning": 2000000,
	"grok-4-1":                    2000000,
	// Grok-4 series: 2M for fast, 256K for dated
	"grok-4-fast-reasoning":     2000000,
	"grok-4-fast-non-reasoning": 2000000,
	"grok-4-0709":               256000,
	"grok-4-0414":               256000,
	"grok-4":                    256000,
	// Grok-3 series: 131K context
	"grok-3":           131072,
	"grok-3-fast":      131072,
	"grok-3-mini":      131072,
	"grok-3-mini-fast": 131072,
	// Legacy
	"grok-2":           131072,
	"grok-2-mini":      131072,
	"grok-vision-beta": 8192,
}

// Default context size for unknown models
const defaultXAIContextSize = 2000000

// FetchXAIModelInfo queries the xAI API for model information and caches it.
// Called once on startup. If it fails, we fall back to hardcoded values.
// Thread-safe.
func FetchXAIModelInfo(apiKey string) {
	xaiModelInfoCacheMu.Lock()
	defer xaiModelInfoCacheMu.Unlock()

	if xaiModelInfoFetched {
		return // Already fetched (or attempted)
	}
	xaiModelInfoFetched = true

	// Create temporary client for model listing
	client, err := xai.New(xai.Config{
		APIKey:  xai.NewSecureString(apiKey),
		Timeout: 10 * time.Second, // Short timeout for startup
	})
	if err != nil {
		L_warn("xai: failed to create client for model info", "error", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	models, err := client.ListModels(ctx)
	if err != nil {
		L_warn("xai: failed to fetch model info from API, using fallback", "error", err)
		return
	}

	// Cache model info
	for _, m := range models {
		xaiModelInfoCache[m.Name] = &xaiModelInfo{
			ContextTokens: int(m.MaxPromptLength),
			InputPrice:    m.PromptTextPricing.PerMillionTokens,
			OutputPrice:   m.CompletionPricing.PerMillionTokens,
		}
		// Also cache by aliases
		for _, alias := range m.Aliases {
			xaiModelInfoCache[alias] = xaiModelInfoCache[m.Name]
		}
	}

	L_info("xai: fetched model info from API", "models", len(models))
	for name, info := range xaiModelInfoCache {
		L_trace("xai: model info cached",
			"model", name,
			"contextTokens", info.ContextTokens,
			"inputPrice", info.InputPrice,
			"outputPrice", info.OutputPrice,
		)
	}
}

// getXAIModelContextTokens returns the context window size for a model.
// Priority: API cache → models.json → hardcoded map → default.
func getXAIModelContextTokens(model string) int {
	xaiModelInfoCacheMu.RLock()
	if info, ok := xaiModelInfoCache[model]; ok {
		xaiModelInfoCacheMu.RUnlock()
		return info.ContextTokens
	}
	xaiModelInfoCacheMu.RUnlock()

	if ctx := metadata.Get().GetContextWindow("xai", model); ctx > 0 {
		return int(ctx)
	}

	if size, ok := xaiModelContextFallback[model]; ok {
		return size
	}
	return defaultXAIContextSize
}

// NewXAIProvider creates a new xAI provider from LLMProviderConfig.
// Client is lazily initialized on first use to support keepalive configuration.
func NewXAIProvider(name string, cfg LLMProviderConfig) (*XAIProvider, error) {
	// Fetch model info from API on first provider creation (async-safe, only runs once)
	if cfg.APIKey != "" {
		go FetchXAIModelInfo(cfg.APIKey)
	}

	// Default IncrementalContext to true if not explicitly set
	// (nil means use default, which is true for context chaining)
	incrementalContext := true
	if cfg.IncrementalContext != nil {
		incrementalContext = *cfg.IncrementalContext
	}

	L_debug("xai provider created",
		"name", name,
		"maxTokens", cfg.MaxTokens,
		"incrementalContext", incrementalContext,
		"serverTools", cfg.ServerToolsAllowed,
		"maxTurns", cfg.MaxTurns,
	)

	return &XAIProvider{
		name:             name,
		config:           cfg,
		model:            "", // Model set via WithModel()
		maxTokens:        cfg.MaxTokens,
		metadataProvider: "xai",
	}, nil
}

// getClient returns the xAI client, creating it lazily on first call.
// Thread-safe via mutex. Applies keepalive settings from config.
func (p *XAIProvider) getClient() (*xai.Client, error) {
	p.clientMu.Lock()
	defer p.clientMu.Unlock()

	if p.client != nil {
		return p.client, nil
	}

	// Build config
	cfg := xai.Config{
		APIKey: xai.NewSecureString(p.config.APIKey),
	}

	// Apply keepalive settings if configured
	if p.config.KeepaliveTime > 0 {
		cfg.KeepaliveTime = time.Duration(p.config.KeepaliveTime) * time.Second
	}
	if p.config.KeepaliveTimeout > 0 {
		cfg.KeepaliveTimeout = time.Duration(p.config.KeepaliveTimeout) * time.Second
	}

	// Apply timeout if configured
	if p.config.TimeoutSeconds > 0 {
		cfg.Timeout = time.Duration(p.config.TimeoutSeconds) * time.Second
	}

	client, err := xai.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create xai client: %w", err)
	}

	p.client = client
	L_debug("xai client: initialized",
		"name", p.name,
		"keepaliveTime", cfg.KeepaliveTime,
		"keepaliveTimeout", cfg.KeepaliveTimeout,
	)

	return p.client, nil
}

// =============================================================================
// Identity Methods (Provider interface)
// =============================================================================

// Name returns the provider instance name (e.g., "xai").
func (p *XAIProvider) Name() string {
	return p.name
}

// Type returns the provider type identifier.
func (p *XAIProvider) Type() string {
	return "xai"
}

// MetadataProvider returns the models.json provider ID for metadata lookups.
func (p *XAIProvider) MetadataProvider() string {
	return p.metadataProvider
}

// Model returns the current model name.
func (p *XAIProvider) Model() string {
	return p.model
}

// WithModel returns a new provider instance configured for the specified model.
// This is used by the registry to create model-specific provider instances.
func (p *XAIProvider) WithModel(model string) Provider {
	normalizedModel := strings.ToLower(strings.TrimSpace(model))
	normalizedModel = strings.TrimPrefix(normalizedModel, "xai/")
	normalizedModel = strings.ReplaceAll(normalizedModel, "grok-4.1", "grok-4-1")
	return &XAIProvider{
		name:             p.name,
		config:           p.config,
		model:            normalizedModel,
		maxTokens:        p.maxTokens,
		metadataProvider: p.metadataProvider,
		metricPrefix:     fmt.Sprintf("llm/%s/%s/%s", p.Type(), p.Name(), normalizedModel),
		// client is shared - no need to recreate
		client:   p.client,
		clientMu: sync.Mutex{},
		// State is NOT copied - each model instance has independent state
	}
}

// WithMaxTokens returns a new provider instance with the specified max output tokens.
func (p *XAIProvider) WithMaxTokens(maxTokens int) Provider {
	return &XAIProvider{
		name:             p.name,
		config:           p.config,
		model:            p.model,
		maxTokens:        maxTokens,
		metadataProvider: p.metadataProvider,
		metricPrefix:     p.metricPrefix,
		client:           p.client,
		clientMu:         sync.Mutex{},
		responseID:       p.responseID,
		lastMessageCount: p.lastMessageCount,
	}
}

// =============================================================================
// Availability Methods (Provider interface) - STUBS, replaced in p1-availability
// =============================================================================

// IsAvailable returns true if the provider is ready to accept requests.
func (p *XAIProvider) IsAvailable() bool {
	return p.config.APIKey != "" && p.model != ""
}

// MaxTokens returns the current output token limit.
// Priority: explicit config override → models.json max_output_tokens → fallback default.
func (p *XAIProvider) MaxTokens() int {
	if p.maxTokens > 0 {
		return p.maxTokens
	}
	if model, ok := metadata.Get().GetModel(p.metadataProvider, p.model); ok && model.MaxOutputTokens > 0 {
		return int(model.MaxOutputTokens)
	}
	return DefaultMaxOutputTokens
}

// ContextTokens returns the model's context window size.
func (p *XAIProvider) ContextTokens() int {
	// Check config override first
	if p.config.ContextTokens > 0 {
		return p.config.ContextTokens
	}
	// Use API cache with hardcoded fallback
	return getXAIModelContextTokens(p.model)
}

// =============================================================================
// Chat Methods (Provider interface) - STUBS, replaced in p1-stream-message/p1-simple-message
// =============================================================================

// StreamMessage sends messages to the xAI API with streaming response.
func (p *XAIProvider) StreamMessage(
	ctx context.Context,
	messages []types.Message,
	toolDefs []types.ToolDefinition,
	systemPrompt string,
	onDelta func(delta string),
	opts *StreamOptions,
) (*Response, error) {
	// Get or create client
	client, err := p.getClient()
	if err != nil {
		return nil, err
	}

	// IncrementalContext: nil = true (default), explicit value = use it
	incrementalMode := true
	if p.config.IncrementalContext != nil {
		incrementalMode = *p.config.IncrementalContext
	}
	L_debug("xai: incremental context config",
		"configValue", p.config.IncrementalContext,
		"incrementalMode", incrementalMode,
	)

	// Build request
	req := xai.NewChatRequest().
		WithModel(p.model).
		WithMaxTokens(safeInt32(p.MaxTokens())).
		WithStoreMessages(incrementalMode)

	usedPreviousResponseId := false
	if incrementalMode && p.responseID != "" && p.lastMessageCount > 0 && len(messages) > p.lastMessageCount {
		// Incremental mode: chain from previous response, send only new messages
		req.WithPreviousResponseId(p.responseID)
		for _, msg := range messages[p.lastMessageCount:] {
			p.addMessageToRequest(req, msg)
		}
		usedPreviousResponseId = true
		L_debug("xai: using incremental mode",
			"responseID", p.responseID,
			"previousCount", p.lastMessageCount,
			"totalCount", len(messages),
			"newMessages", len(messages)-p.lastMessageCount,
		)
	} else {
		// Full mode: system prompt + all messages
		if systemPrompt != "" {
			req.SystemMessage(xai.SystemContent{Text: systemPrompt})
		}
		for _, msg := range messages {
			p.addMessageToRequest(req, msg)
		}
	}

	// Add server-side tools (web_search, x_search, etc.)
	p.addServerTools(req)

	// Add client-side tools (GoClaw tools)
	p.addClientTools(req, toolDefs)

	// Apply reasoning effort from options. Only grok-3-mini uses it; other models
	// (e.g. grok-4-1-fast-reasoning) ignore it. No harm in setting it for all.
	if opts != nil && opts.ThinkingLevel != "" {
		level := ThinkingLevel(opts.ThinkingLevel)
		if effort := level.XAIEffort(); effort != nil {
			req.WithReasoningEffort(*effort)
			L_debug("xai: reasoning effort applied", "level", opts.ThinkingLevel, "effort", *effort)
		}
	}

	// Apply max turns if configured
	if p.config.MaxTurns > 0 {
		req.WithMaxTurns(safeInt32(p.config.MaxTurns))
	}

	serverTools := p.getEnabledServerToolNames()
	clientToolNames := make([]string, 0, len(toolDefs))
	for _, td := range toolDefs {
		name := td.Name
		for _, sn := range serverTools {
			if td.Name == sn {
				name = clientToolPrefix + td.Name
				break
			}
		}
		clientToolNames = append(clientToolNames, name)
	}
	L_info("xai: tools",
		"model", p.model,
		"server", serverTools,
		"client", clientToolNames,
	)

	// Start streaming
	stream, err := client.StreamChat(ctx, req)
	if err != nil {
		// Handle 404 error when previousResponseId has expired
		if usedPreviousResponseId && isNotFoundError(err) {
			L_warn("xai: responseID expired (404), retrying with full transcript",
				"expiredResponseID", p.responseID,
			)
			// Clear state
			p.responseID = ""
			p.lastMessageCount = 0

			// Rebuild request without previousResponseId
			req = xai.NewChatRequest().
				WithModel(p.model).
				WithMaxTokens(safeInt32(p.MaxTokens()))
			if systemPrompt != "" {
				req.SystemMessage(xai.SystemContent{Text: systemPrompt})
			}
			for _, msg := range messages {
				p.addMessageToRequest(req, msg)
			}
			p.addServerTools(req)
			p.addClientTools(req, toolDefs)
			if opts != nil && opts.ThinkingLevel != "" {
				level := ThinkingLevel(opts.ThinkingLevel)
				if effort := level.XAIEffort(); effort != nil {
					req.WithReasoningEffort(*effort)
				}
			}
			if p.config.MaxTurns > 0 {
				req.WithMaxTurns(safeInt32(p.config.MaxTurns))
			}
			req.WithStoreMessages(incrementalMode)

			stream, err = client.StreamChat(ctx, req)
			if err != nil {
				return nil, err
			}
		} else if isTransientServerError(err) && ctx.Err() == nil {
			// Transient server error (RST_STREAM, INTERNAL_ERROR) — retry once after backoff
			L_warn("xai: transient server error on connect, retrying in 1s",
				"model", p.model,
				"error", err,
			)
			time.Sleep(1 * time.Second)
			stream, err = client.StreamChat(ctx, req)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}
	defer stream.Close()

	// Process stream with opts for OnThinkingDelta and OnServerToolCall
	resp, err := p.processStream(ctx, stream, onDelta, opts, toolDefs)
	if err != nil {
		return nil, err
	}

	// Update message count tracking for incremental mode
	// (responseID is already captured in processStream)
	p.lastMessageCount = len(messages)

	return resp, nil
}

// toolCallStatusString maps xai.ToolCallStatus to string for callbacks.
func toolCallStatusString(s xai.ToolCallStatus) string {
	switch s {
	case xai.ToolCallStatusCompleted:
		return "completed"
	case xai.ToolCallStatusFailed:
		return "failed"
	default:
		return "pending"
	}
}

// SimpleMessage sends a single message without streaming (for summarization).
// This is a simpler interface used for tasks like compaction/summarization.
func (p *XAIProvider) SimpleMessage(ctx context.Context, userMessage, systemPrompt string) (string, error) {
	// Get or create client
	client, err := p.getClient()
	if err != nil {
		return "", err
	}

	// Build request
	req := xai.NewChatRequest().
		WithModel(p.model).
		WithMaxTokens(safeInt32(p.MaxTokens()))

	// Add system prompt
	if systemPrompt != "" {
		req.SystemMessage(xai.SystemContent{Text: systemPrompt})
	}

	// Add user message
	req.UserMessage(xai.UserContent{Text: userMessage})

	// No tools for simple messages (summarization doesn't need them)
	// No reasoning effort for simple messages (keep it fast)

	// Execute non-streaming request
	resp, err := client.CompleteChat(ctx, req)
	if err != nil {
		return "", err
	}

	L_debug("xai: simple message completed",
		"inputTokens", resp.Usage.PromptTokens,
		"outputTokens", resp.Usage.CompletionTokens,
		"cacheReadTokens", resp.Usage.CachedPromptTokens,
		"reasoningTokens", resp.Usage.ReasoningTokens,
		"responseLen", len(resp.Content),
	)

	// Record metrics
	if p.metricPrefix != "" {
		MetricAdd(p.metricPrefix, "input_tokens", int64(resp.Usage.PromptTokens))
		MetricAdd(p.metricPrefix, "output_tokens", int64(resp.Usage.CompletionTokens))
		if resp.Usage.CachedPromptTokens > 0 {
			MetricAdd(p.metricPrefix, "cache_read_tokens", int64(resp.Usage.CachedPromptTokens))
		}
		if resp.Usage.ReasoningTokens > 0 {
			MetricAdd(p.metricPrefix, "reasoning_tokens", int64(resp.Usage.ReasoningTokens))
		}
		MetricSuccess(p.metricPrefix, "request_status")
		simpleResp := &Response{
			InputTokens:     int(resp.Usage.PromptTokens),
			OutputTokens:    int(resp.Usage.CompletionTokens),
			CacheReadTokens: int(resp.Usage.CachedPromptTokens),
			ReasoningTokens: int(resp.Usage.ReasoningTokens),
		}
		emitCostMetrics(p.metricPrefix, PurposeFromContext(ctx), p.config, p.metadataProvider, p.model, simpleResp)
	}

	return resp.Content, nil
}

// =============================================================================
// StatefulProvider Interface - for context preservation
// =============================================================================

// LoadSessionState loads previously saved state for this provider.
// Called by the registry before StreamMessage.
func (p *XAIProvider) LoadSessionState(state map[string]any) {
	if state == nil {
		p.responseID = ""
		p.lastMessageCount = 0
		return
	}

	// Extract responseID
	if rid, ok := state["responseID"].(string); ok {
		p.responseID = rid
	} else {
		p.responseID = ""
	}

	// Extract lastMessageCount
	if count, ok := state["lastMessageCount"].(float64); ok {
		p.lastMessageCount = int(count) // JSON numbers are float64
	} else {
		p.lastMessageCount = 0
	}

	L_trace("xai: loaded session state",
		"responseID", p.responseID != "",
		"lastMessageCount", p.lastMessageCount,
	)
}

// SaveSessionState returns state to persist after StreamMessage.
// Called by the registry after StreamMessage (even on error).
func (p *XAIProvider) SaveSessionState() map[string]any {
	if p.responseID == "" {
		return nil // No state to save
	}

	state := map[string]any{
		"responseID":       p.responseID,
		"lastMessageCount": p.lastMessageCount,
	}

	L_trace("xai: saving session state",
		"responseID", p.responseID != "",
		"lastMessageCount", p.lastMessageCount,
	)

	return state
}

// =============================================================================
// Embedding Methods (Provider interface) - xAI does not support embeddings
// =============================================================================

// SupportsEmbeddings returns false - xAI does not support embeddings.
func (p *XAIProvider) SupportsEmbeddings() bool {
	return false
}

// Embed returns an error - xAI does not support embeddings.
func (p *XAIProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	return nil, fmt.Errorf("xai does not support embeddings")
}

// EmbedBatch returns an error - xAI does not support embeddings.
func (p *XAIProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, fmt.Errorf("xai does not support embeddings")
}

// EmbeddingDimensions returns 0 - xAI does not support embeddings.
func (p *XAIProvider) EmbeddingDimensions() int {
	return 0
}

// =============================================================================
// Message Helpers
// =============================================================================

// addMessageToRequest maps a GoClaw Message to the appropriate xai-go content type
// and adds it to the ChatRequest.
func (p *XAIProvider) addMessageToRequest(req *xai.ChatRequest, msg types.Message) {
	L_trace("xai: adding message",
		"role", msg.Role,
		"contentLen", len(msg.Content),
		"hasImages", msg.HasImages(),
		"toolName", msg.ToolName,
		"toolUseID", msg.ToolUseID,
	)

	switch msg.Role {
	case "user":
		// User message with optional image
		// Build text from Content + text ContentBlocks
		textParts := []string{}
		if msg.Content != "" {
			textParts = append(textParts, msg.Content)
		}

		// Note: audio blocks are converted to text by gateway's resolveMediaContent
		var imageURL string
		for _, block := range msg.ContentBlocks {
			switch block.Type {
			case "text":
				if block.Text != "" {
					textParts = append(textParts, block.Text)
				}
			case "image":
				// xai-go UserContent only supports one image - use first
				if block.Data != "" && imageURL == "" {
					imageURL = "data:" + block.MimeType + ";base64," + block.Data
					L_info("xai: user message with image",
						"mimeType", block.MimeType,
						"dataLen", len(block.Data))
				}
			}
		}

		content := xai.UserContent{Text: strings.Join(textParts, "\n")}
		if imageURL != "" {
			content.ImageURL = imageURL
		}
		req.UserMessage(content)

	case "assistant":
		// Assistant message (may include thinking in Content)
		if msg.Content == "" {
			L_debug("xai: skipping empty assistant message")
			return
		}
		req.AssistantMessage(xai.AssistantContent{Text: msg.Content})

	case "tool_use":
		// Tool use is an assistant message with tool calls
		// ToolInput is json.RawMessage, need to convert to string
		// Note: Text field explicitly set (can be empty) per xAI API requirements
		req.AssistantMessage(xai.AssistantContent{
			Text: "", // Explicit empty string, required by xAI
			ToolCalls: []xai.HistoryToolCall{
				{
					ID:        msg.ToolUseID,
					Name:      msg.ToolName,
					Arguments: string(msg.ToolInput),
				},
			},
		})

	case "tool_result":
		// Tool result
		if msg.ToolUseID == "" {
			L_warn("xai: tool_result with empty ToolUseID, skipping")
			return
		}
		req.ToolResult(xai.ToolContent{
			CallID: msg.ToolUseID,
			Result: msg.Content,
		})

	default:
		L_warn("xai: unknown message role, skipping",
			"role", msg.Role,
			"contentLen", len(msg.Content),
		)
	}
}

// addServerTools adds xAI server-side tools to the request.
// HACK: Always add all three. xAI executes them internally (web search, X search, code).
// Config ServerToolsAllowed is ignored for now.
func (p *XAIProvider) addServerTools(req *xai.ChatRequest) {
	req.AddTool(xai.NewWebSearchTool())
	req.AddTool(xai.NewXSearchTool())
	req.AddTool(xai.NewCodeExecutionTool())
	L_debug("xai: server tools configured",
		"added", []string{"web_search", "x_search", "code_execution"},
	)
}

// getEnabledServerToolNames returns the names of server-side tools that will be added.
func (p *XAIProvider) getEnabledServerToolNames() []string {
	return []string{"web_search", "x_search", "code_execution"}
}

// addClientTools converts GoClaw tool definitions to xai.FunctionTool and adds them.
// Also sets ToolChoiceAuto so the model can decide when to use tools.
// Tools that conflict with enabled server tools are prefixed with clientToolPrefix.
func (p *XAIProvider) addClientTools(req *xai.ChatRequest, toolDefs []types.ToolDefinition) {
	if len(toolDefs) == 0 {
		return
	}

	// Get server tool names to check for conflicts
	serverToolNames := p.getEnabledServerToolNames()

	for _, td := range toolDefs {
		// Convert InputSchema map to JSON
		schemaJSON, err := json.Marshal(td.InputSchema)
		if err != nil {
			L_warn("xai: failed to marshal tool schema, skipping",
				"tool", td.Name,
				"error", err,
			)
			continue
		}

		// Check for conflict with server tools
		toolName := td.Name
		for _, serverName := range serverToolNames {
			if td.Name == serverName {
				toolName = clientToolPrefix + td.Name
				L_debug("xai: prefixed client tool to avoid conflict",
					"original", td.Name,
					"prefixed", toolName,
				)
				break
			}
		}

		tool := xai.NewFunctionTool(toolName, td.Description).
			WithParameters(schemaJSON)
		req.AddTool(tool)
	}

	// Let the model decide when to use tools
	req.WithToolChoice(xai.ToolChoiceAuto)

	L_debug("xai: client tools configured", "count", len(toolDefs))
}

// =============================================================================
// Error Helpers
// =============================================================================

// isNotFoundError returns true if the error is a 404 not found error.
// This is used to detect expired responseIDs for context preservation.
func isNotFoundError(err error) bool {
	var xaiErr *xai.Error
	if errors.As(err, &xaiErr) {
		return xaiErr.Code == xai.ErrNotFound
	}
	return false
}

// isTransientServerError returns true if the error is a transient server-side
// failure that's worth retrying (RST_STREAM, gRPC INTERNAL, server_error).
func isTransientServerError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "rst_stream") ||
		strings.Contains(msg, "internal_error") ||
		strings.Contains(msg, "server_error") ||
		strings.Contains(msg, "code = internal")
}

// =============================================================================
// Stream Processing
// =============================================================================

// processStream iterates through streaming chunks and builds a Response.
// It captures responseID, accumulates text/reasoning, extracts tool calls, and tracks usage.
// opts provides OnThinkingDelta and OnServerToolCall callbacks (may be nil).
func (p *XAIProvider) processStream(
	ctx context.Context,
	stream *xai.ChunkStream,
	onDelta func(delta string),
	opts *StreamOptions,
	toolDefs []ToolDefinition,
) (*Response, error) {
	// Build set of registered client tool names (canonical and prefixed versions)
	clientToolNames := make(map[string]bool)
	for _, td := range toolDefs {
		clientToolNames[td.Name] = true
		clientToolNames[clientToolPrefix+td.Name] = true // Also accept prefixed version
	}

	var (
		textBuilder      strings.Builder
		reasoningBuilder strings.Builder
		responseID       string
		finishReason     xai.FinishReason
		usage            xai.Usage
		toolCall         *xai.ToolCallInfo
	)

	for {
		chunk, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		// Capture responseID from first chunk that has it
		if responseID == "" && chunk.ID != "" {
			responseID = chunk.ID
		}

		// Accumulate text delta
		if chunk.Delta != "" {
			textBuilder.WriteString(chunk.Delta)
			if onDelta != nil {
				onDelta(chunk.Delta)
			}
		}

		// Accumulate reasoning delta
		if chunk.ReasoningDelta != "" {
			reasoningBuilder.WriteString(chunk.ReasoningDelta)
			if opts != nil && opts.OnThinkingDelta != nil {
				opts.OnThinkingDelta(chunk.ReasoningDelta)
			}
		}

		// Process ALL tool calls in chunk (client and server)
		for _, tc := range chunk.ToolCalls {
			if tc.Function == nil {
				continue
			}
			name := tc.Function.Name
			args := tc.Function.Arguments
			statusStr := toolCallStatusString(tc.Status)

			if tc.IsServerSide() {
				// Server-side: log at INFO, inject into thinking, emit via callback
				L_info("xai: server tool",
					"tool", name,
					"args", args,
					"status", statusStr,
					"id", tc.ID,
				)
				if opts != nil && opts.OnThinkingDelta != nil {
					formatted := fmt.Sprintf("\n[%s: %s] (%s)\n", name, args, statusStr)
					if tc.ErrorMessage != "" {
						formatted = fmt.Sprintf("\n[%s: %s] (failed: %s)\n", name, args, tc.ErrorMessage)
					}
					opts.OnThinkingDelta(formatted)
				}
				if opts != nil && opts.OnServerToolCall != nil {
					opts.OnServerToolCall(name, args, statusStr, tc.ErrorMessage)
				}
				continue
			}

			// Client-side: capture first one for GoClaw to execute
			if toolCall == nil && clientToolNames[name] {
				L_debug("xai: client tool call received",
					"name", name,
					"id", tc.ID,
				)
				toolCall = tc
			} else if toolCall == nil {
				L_debug("xai: ignoring non-client tool call",
					"name", name,
					"id", tc.ID,
					"type", tc.Type,
				)
			}
		}

		// Update finish reason and usage from each chunk (last one wins)
		if chunk.FinishReason != "" {
			finishReason = chunk.FinishReason
		}
		usage = chunk.Usage
	}

	// Store responseID for context preservation
	p.responseID = responseID

	// Build response
	resp := &Response{
		Text:            textBuilder.String(),
		Thinking:        reasoningBuilder.String(),
		InputTokens:     int(usage.PromptTokens),
		OutputTokens:    int(usage.CompletionTokens),
		CacheReadTokens: int(usage.CachedPromptTokens),
		ReasoningTokens: int(usage.ReasoningTokens),
	}

	// Map finish reason to stop reason
	switch finishReason {
	case xai.FinishReasonStop:
		resp.StopReason = "end_turn"
	case xai.FinishReasonToolCalls:
		resp.StopReason = "tool_use"
	case xai.FinishReasonLength:
		resp.StopReason = "max_tokens"
	default:
		resp.StopReason = "end_turn"
	}

	// Extract tool call info if present
	if toolCall != nil && toolCall.Function != nil {
		resp.ToolUseID = toolCall.ID
		// Strip client tool prefix if present (restores canonical name for persistence)
		toolName := toolCall.Function.Name
		if strings.HasPrefix(toolName, clientToolPrefix) {
			toolName = strings.TrimPrefix(toolName, clientToolPrefix)
			L_debug("xai: stripped prefix from tool call",
				"prefixed", toolCall.Function.Name,
				"canonical", toolName,
			)
		}
		resp.ToolName = toolName
		resp.ToolInput = json.RawMessage(toolCall.Function.Arguments)
		resp.StopReason = "tool_use"
	}

	L_debug("xai: stream complete",
		"responseID", responseID,
		"textLen", len(resp.Text),
		"thinkingLen", len(resp.Thinking),
		"stopReason", resp.StopReason,
		"inputTokens", resp.InputTokens,
		"outputTokens", resp.OutputTokens,
		"cacheReadTokens", resp.CacheReadTokens,
		"reasoningTokens", resp.ReasoningTokens,
	)
	if toolCall != nil {
		L_debug("xai: tool call",
			"tool", resp.ToolName,
			"id", resp.ToolUseID,
			"argsLen", len(toolCall.Function.Arguments),
		)
	}

	// Record metrics
	if p.metricPrefix != "" {
		MetricAdd(p.metricPrefix, "input_tokens", int64(resp.InputTokens))
		MetricAdd(p.metricPrefix, "output_tokens", int64(resp.OutputTokens))
		if resp.CacheReadTokens > 0 {
			MetricAdd(p.metricPrefix, "cache_read_tokens", int64(resp.CacheReadTokens))
		}
		if resp.ReasoningTokens > 0 {
			MetricAdd(p.metricPrefix, "reasoning_tokens", int64(resp.ReasoningTokens))
		}
		MetricOutcome(p.metricPrefix, "stop_reason", resp.StopReason)
		MetricSuccess(p.metricPrefix, "request_status")
		emitCostMetrics(p.metricPrefix, PurposeFromContext(ctx), p.config, p.metadataProvider, p.model, resp)
	}

	return resp, nil
}

// ListModels fetches available models from xAI's API.
// Implements ModelLister interface.
func (p *XAIProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	if p.config.APIKey == "" {
		return nil, fmt.Errorf("API key required to list models")
	}

	client, err := xai.New(xai.Config{
		APIKey:  xai.NewSecureString(p.config.APIKey),
		Timeout: 10 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	models, err := client.ListModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch models: %w", err)
	}

	result := make([]ModelInfo, len(models))
	for i, m := range models {
		result[i] = ModelInfo{
			ID:            m.Name,
			DisplayName:   m.Name,
			ContextTokens: int(m.MaxPromptLength),
		}
	}

	return result, nil
}

// TestConnection verifies the API key is valid by listing models.
// Implements ConnectionTester interface.
func (p *XAIProvider) TestConnection(ctx context.Context) error {
	_, err := p.ListModels(ctx)
	if err != nil {
		return fmt.Errorf("connection test failed: %w", err)
	}
	return nil
}

// GetSubtypes returns available subtypes. xAI has no subtypes.
// Implements SubtypeProvider interface.
func (p *XAIProvider) GetSubtypes() []ProviderSubtype {
	return []ProviderSubtype{}
}
