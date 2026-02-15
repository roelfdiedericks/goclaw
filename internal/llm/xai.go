// Package llm provides LLM client implementations.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/roelfdiedericks/xai-go"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// XAIProvider implements the Provider interface for xAI's Grok API.
// Supports streaming, native tool calling, server-side tools, and context preservation.
// Also implements StatefulProvider for session-scoped state (responseID).
type XAIProvider struct {
	name      string         // Provider instance name (e.g., "xai")
	config    ProviderConfig // Full provider configuration
	model     string         // Current model (e.g., "grok-4-1-fast-reasoning")
	maxTokens int            // Output token limit

	// Client management (lazy initialization)
	client   *xai.Client
	clientMu sync.Mutex

	// Context preservation state (saved/loaded via StatefulProvider)
	responseID       string // xAI's previous_response_id for context
	lastMessageCount int    // Message count at last successful stream
}

// Known xAI server-side tools
var knownXAIServerTools = []string{
	"web_search",
	"x_search",
	"code_execution",
	"collections_search",
	"attachment_search",
	"mcp",
}

// clientToolPrefix is added to client tool names that conflict with xAI server tools.
// This allows both tools to be available to the LLM. The prefix is stripped when
// processing tool calls, so persisted data uses the canonical tool name.
const clientToolPrefix = "local_"

// xAI model context window sizes (tokens)
var xaiModelContextSizes = map[string]int{
	"grok-4":                 131072,
	"grok-4-0414":            131072,
	"grok-4-1":               131072,
	"grok-4-1-fast-reasoning": 131072,
	"grok-3":                 131072,
	"grok-3-fast":            131072,
	"grok-3-mini":            131072,
	"grok-3-mini-fast":       131072,
	"grok-2":                 131072,
	"grok-2-mini":            131072,
	"grok-vision-beta":       8192,
}

// Default context size for unknown models
const defaultXAIContextSize = 131072

// Default model for xAI
const defaultXAIModel = "grok-4-1-fast-reasoning"

// NewXAIProvider creates a new xAI provider from ProviderConfig.
// Client is lazily initialized on first use to support keepalive configuration.
func NewXAIProvider(name string, cfg ProviderConfig) (*XAIProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("xai API key not configured")
	}

	// Default maxTokens
	maxTokens := cfg.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	// Default StoreResponses to true if not explicitly set
	// (nil means use default, which is true for context preservation)
	storeResponses := true
	if cfg.StoreResponses != nil {
		storeResponses = *cfg.StoreResponses
	}

	L_debug("xai provider created",
		"name", name,
		"maxTokens", maxTokens,
		"storeResponses", storeResponses,
		"serverTools", cfg.ServerToolsAllowed,
		"maxTurns", cfg.MaxTurns,
	)

	return &XAIProvider{
		name:      name,
		config:    cfg,
		model:     "", // Model set via WithModel()
		maxTokens: maxTokens,
		// client is lazily initialized in getClient()
		// responseID and lastMessageCount are loaded via LoadSessionState()
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

// Model returns the current model name.
func (p *XAIProvider) Model() string {
	return p.model
}

// WithModel returns a new provider instance configured for the specified model.
// This is used by the registry to create model-specific provider instances.
func (p *XAIProvider) WithModel(model string) Provider {
	return &XAIProvider{
		name:      p.name,
		config:    p.config,
		model:     model,
		maxTokens: p.maxTokens,
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
func (p *XAIProvider) MaxTokens() int {
	return p.maxTokens
}

// ContextTokens returns the model's context window size.
func (p *XAIProvider) ContextTokens() int {
	// Check config override first
	if p.config.ContextTokens > 0 {
		return p.config.ContextTokens
	}
	// Look up known model sizes
	if size, ok := xaiModelContextSizes[p.model]; ok {
		return size
	}
	return defaultXAIContextSize
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

	// Build request
	req := xai.NewChatRequest().
		WithModel(p.model).
		WithMaxTokens(int32(p.maxTokens))

	// Add system prompt
	if systemPrompt != "" {
		req.SystemMessage(xai.SystemContent{Text: systemPrompt})
	}

	// Add conversation messages
	for _, msg := range messages {
		p.addMessageToRequest(req, msg)
	}

	// Add server-side tools (web_search, x_search, etc.)
	p.addServerTools(req)

	// Add client-side tools (GoClaw tools)
	p.addClientTools(req, toolDefs)

	// Apply reasoning effort from options
	if opts != nil && opts.ThinkingLevel != "" {
		level := ThinkingLevel(opts.ThinkingLevel)
		if effort := level.XAIEffort(); effort != nil {
			req.WithReasoningEffort(*effort)
			L_debug("xai: reasoning effort applied", "level", opts.ThinkingLevel, "effort", *effort)
		}
	}

	// Apply max turns if configured
	if p.config.MaxTurns > 0 {
		req.WithMaxTurns(int32(p.config.MaxTurns))
	}

	// Enable message storage for context preservation
	// StoreResponses: nil = true (default), explicit value = use it
	storeMode := true
	if p.config.StoreResponses != nil {
		storeMode = *p.config.StoreResponses
	}
	req.WithStoreMessages(storeMode)

	// Incremental mode: if we have a responseID and storeMode is enabled,
	// we can use the server's stored context and only send new messages
	if storeMode && p.responseID != "" && p.lastMessageCount > 0 && len(messages) > p.lastMessageCount {
		// Use previous response to chain context
		req.WithPreviousResponseId(p.responseID)
		L_debug("xai: using incremental mode",
			"responseID", p.responseID,
			"previousCount", p.lastMessageCount,
			"totalCount", len(messages),
			"newMessages", len(messages)-p.lastMessageCount,
		)
		// Note: We still send all messages because the request builder doesn't
		// support partial messages. The server will use previousResponseId to
		// avoid reprocessing old context.
	}

	// Track if we used previousResponseId (for 404 retry logic)
	usedPreviousResponseId := storeMode && p.responseID != "" && p.lastMessageCount > 0 && len(messages) > p.lastMessageCount

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
				WithMaxTokens(int32(p.maxTokens))
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
				req.WithMaxTurns(int32(p.config.MaxTurns))
			}
			req.WithStoreMessages(storeMode)
			// Note: NOT using previousResponseId this time

			// Retry
			stream, err = client.StreamChat(ctx, req)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}
	defer stream.Close()

	// Get thinking delta callback from options
	var onThinkingDelta func(string)
	if opts != nil {
		onThinkingDelta = opts.OnThinkingDelta
	}

	// Process stream
	resp, err := p.processStream(stream, onDelta, onThinkingDelta)
	if err != nil {
		return nil, err
	}

	// Update message count tracking for incremental mode
	// (responseID is already captured in processStream)
	p.lastMessageCount = len(messages)

	return resp, nil
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
		WithMaxTokens(int32(p.maxTokens))

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
		"responseLen", len(resp.Content),
	)

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

	L_debug("xai: loaded session state",
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

	L_debug("xai: saving session state",
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
	L_debug("xai: adding message",
		"role", msg.Role,
		"contentLen", len(msg.Content),
		"hasImages", len(msg.Images) > 0,
		"toolName", msg.ToolName,
		"toolUseID", msg.ToolUseID,
	)

	switch msg.Role {
	case "user":
		// User message with optional image
		content := xai.UserContent{Text: msg.Content}
		// Add first image if present (xai-go UserContent only supports one image)
		// Convert base64 data to data URL format
		if len(msg.Images) > 0 && msg.Images[0].Data != "" {
			content.ImageURL = "data:" + msg.Images[0].MimeType + ";base64," + msg.Images[0].Data
			L_debug("xai: user message with image", "mimeType", msg.Images[0].MimeType)
		}
		req.UserMessage(content)

	case "assistant":
		// Assistant message (may include thinking in Content)
		if msg.Content == "" {
			L_debug("xai: assistant message has empty content, skipping")
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
		L_debug("xai: tool_use message added",
			"toolName", msg.ToolName,
			"toolUseID", msg.ToolUseID,
			"argsLen", len(msg.ToolInput),
		)

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
		L_debug("xai: tool_result message added",
			"toolUseID", msg.ToolUseID,
			"resultLen", len(msg.Content),
		)

	default:
		L_warn("xai: unknown message role, skipping",
			"role", msg.Role,
			"contentLen", len(msg.Content),
		)
	}
}

// addServerTools adds xAI server-side tools to the request based on config.
// If ServerToolsAllowed is empty, all known tools are enabled.
// If ServerToolsAllowed contains "none", no server tools are added.
// Otherwise, only tools in the allowlist are added.
func (p *XAIProvider) addServerTools(req *xai.ChatRequest) {
	allowed := p.config.ServerToolsAllowed

	// Check for explicit "none" to disable all server tools
	for _, a := range allowed {
		if a == "none" {
			L_trace("xai: server tools disabled via 'none'")
			return
		}
	}

	// Helper to check if a tool is allowed
	isAllowed := func(name string) bool {
		if len(allowed) == 0 {
			return true // Empty list = all tools allowed
		}
		for _, a := range allowed {
			if a == name {
				return true
			}
		}
		return false
	}

	// Add web_search if allowed
	if isAllowed("web_search") {
		req.AddTool(xai.NewWebSearchTool())
	}

	// Add x_search if allowed
	if isAllowed("x_search") {
		req.AddTool(xai.NewXSearchTool())
	}

	// Add code_execution if allowed
	if isAllowed("code_execution") {
		req.AddTool(xai.NewCodeExecutionTool())
	}

	// Note: collections_search requires collection IDs which we don't have configured
	// Note: attachment_search requires attachments which we don't have configured
	// Note: mcp requires server label/URL which we don't have configured
	// These can be added later if config fields are added

	L_trace("xai: server tools configured",
		"allowed", allowed,
		"addedAll", len(allowed) == 0,
	)
}

// getEnabledServerToolNames returns the names of server-side tools that will be added.
// Used to detect conflicts with client tool names.
func (p *XAIProvider) getEnabledServerToolNames() []string {
	allowed := p.config.ServerToolsAllowed

	// Check for explicit "none" - no server tools
	for _, a := range allowed {
		if a == "none" {
			return nil
		}
	}

	// If empty, all known tools are enabled
	if len(allowed) == 0 {
		// Only return tools we actually add (web_search, x_search, code_execution)
		return []string{"web_search", "x_search", "code_execution"}
	}

	// Return only the allowed ones that we actually add
	var result []string
	for _, name := range allowed {
		if name == "web_search" || name == "x_search" || name == "code_execution" {
			result = append(result, name)
		}
	}
	return result
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

	L_trace("xai: client tools configured", "count", len(toolDefs))
}

// =============================================================================
// Error Helpers
// =============================================================================

// classifyXAIError maps an xai.Error to GoClaw's ErrorType.
func classifyXAIError(err error) ErrorType {
	var xaiErr *xai.Error
	if !errors.As(err, &xaiErr) {
		return ErrorTypeUnknown
	}

	switch xaiErr.Code {
	case xai.ErrAuth:
		return ErrorTypeAuth
	case xai.ErrRateLimit, xai.ErrResourceExhausted:
		return ErrorTypeRateLimit
	case xai.ErrUnavailable, xai.ErrServerError:
		return ErrorTypeOverloaded
	case xai.ErrTimeout:
		return ErrorTypeTimeout
	case xai.ErrInvalidRequest:
		return ErrorTypeFormat
	default:
		return ErrorTypeUnknown
	}
}

// isXAIRetryable returns true if the error is transient and can be retried.
func isXAIRetryable(err error) bool {
	var xaiErr *xai.Error
	if errors.As(err, &xaiErr) {
		return xaiErr.IsRetryable()
	}
	return false
}

// isNotFoundError returns true if the error is a 404 not found error.
// This is used to detect expired responseIDs for context preservation.
func isNotFoundError(err error) bool {
	var xaiErr *xai.Error
	if errors.As(err, &xaiErr) {
		return xaiErr.Code == xai.ErrNotFound
	}
	return false
}

// =============================================================================
// Stream Processing
// =============================================================================

// processStream iterates through streaming chunks and builds a Response.
// It captures responseID, accumulates text/reasoning, extracts tool calls, and tracks usage.
// The onDelta callback is invoked for each text delta.
// The onThinkingDelta callback is invoked for each reasoning delta (may be nil).
func (p *XAIProvider) processStream(
	stream *xai.ChunkStream,
	onDelta func(delta string),
	onThinkingDelta func(delta string),
) (*Response, error) {
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
			if onThinkingDelta != nil {
				onThinkingDelta(chunk.ReasoningDelta)
			}
		}

		// Capture first tool call (GoClaw processes one at a time)
		if toolCall == nil && len(chunk.ToolCalls) > 0 {
			toolCall = chunk.ToolCalls[0]
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
		Text:         textBuilder.String(),
		Thinking:     reasoningBuilder.String(),
		InputTokens:  int(usage.PromptTokens),
		OutputTokens: int(usage.CompletionTokens),
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

	L_debug("xai: stream processed",
		"responseID", responseID,
		"textLen", len(resp.Text),
		"thinkingLen", len(resp.Thinking),
		"hasToolCall", toolCall != nil,
		"inputTokens", resp.InputTokens,
		"outputTokens", resp.OutputTokens,
	)

	return resp, nil
}
