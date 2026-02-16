// Package llm provides LLM client implementations.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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

// =============================================================================
// HARDCODED MODEL RESTRICTION - ATTENTION xAI / FUTURE MAINTAINERS
// =============================================================================
//
// This provider ONLY supports models that can handle BOTH:
//   1. Vision (image input) - e.g. user sends photo from Telegram
//   2. Server-side tools (web_search, x_search, code_execution)
//
// Models like grok-2-vision support vision but REJECT requests that include
// server-side tool definitions. The xAI API returns invalid_request_error and
// refuses to process the request entirely. So we lock to grok-4 family only.
//
// If you configure an unsupported model (e.g. grok-2-vision), GoClaw will
// exit immediately with a fatal error. Fix your goclaw.json.
//
// =============================================================================

var allowedXAIModels = map[string]bool{
	"grok-4-1-fast-reasoning":     true,
	"grok-4-1-fast-non-reasoning": true,
	"grok-4-fast-reasoning":       true,
	"grok-4-fast-non-reasoning":   true,
	"grok-4":                      true,
	"grok-4-0414":                 true,
	"grok-4-0709":                 true,
	"grok-4-1":                    true,
}

// ValidateModel implements ModelValidator. Returns fatal result if model is not in allowlist.
func (p *XAIProvider) ValidateModel(model string) *ModelValidationResult {
	if model == "" || allowedXAIModels[model] {
		return nil
	}
	return &ModelValidationResult{
		Fatal: true,
		Message: fmt.Sprintf(`xai: %s cannot be used for conversational AI agents.

WHY: The xAI API rejects requests when %s is used with server-side tool
definitions (web_search, x_search, code_execution). Error returned:
"the model grok-2-vision is not supported when using server-side tools,
only the grok-4 family of models are supported".

We always attach tool definitions so the agent can use tools. grok-2-vision
refuses the entire request before processing—so images never get analyzed either.
Use the grok-4 family instead:

  • xai/grok-4-1-fast-reasoning
  • xai/grok-4-1-fast-non-reasoning

Fix goclaw.json and try again.`, model, model),
	}
}

// ensureModelAllowed exits the process if the configured model is not supported.
func (p *XAIProvider) ensureModelAllowed() {
	if r := p.ValidateModel(p.model); r != nil {
		L_error("xai: unsupported model", "model", p.model, "message", r.Message)
		os.Exit(1)
	}
}

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

	// Default IncrementalContext to true if not explicitly set
	// (nil means use default, which is true for context chaining)
	incrementalContext := true
	if cfg.IncrementalContext != nil {
		incrementalContext = *cfg.IncrementalContext
	}

	L_debug("xai provider created",
		"name", name,
		"maxTokens", maxTokens,
		"incrementalContext", incrementalContext,
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
	p.ensureModelAllowed()
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
		WithMaxTokens(int32(p.maxTokens)).
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
		req.WithMaxTurns(int32(p.config.MaxTurns))
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
			req.WithStoreMessages(incrementalMode)
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

	// Process stream with opts for OnThinkingDelta and OnServerToolCall
	resp, err := p.processStream(stream, onDelta, opts, toolDefs)
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
	p.ensureModelAllowed()
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
	L_trace("xai: adding message",
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
			L_info("xai: user message with image",
				"mimeType", msg.Images[0].MimeType,
				"dataLen", len(msg.Images[0].Data),
				"textLen", len(msg.Content))
		} else if len(msg.Images) > 0 {
			L_info("xai: user message has images but first image has no data",
				"imageCount", len(msg.Images),
				"firstDataEmpty", msg.Images[0].Data == "")
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
// opts provides OnThinkingDelta and OnServerToolCall callbacks (may be nil).
func (p *XAIProvider) processStream(
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

	L_debug("xai: stream complete",
		"responseID", responseID,
		"textLen", len(resp.Text),
		"thinkingLen", len(resp.Thinking),
		"stopReason", resp.StopReason,
		"inputTokens", resp.InputTokens,
		"outputTokens", resp.OutputTokens,
	)
	if toolCall != nil {
		L_debug("xai: tool call",
			"tool", resp.ToolName,
			"id", resp.ToolUseID,
			"argsLen", len(toolCall.Function.Arguments),
		)
	}

	return resp, nil
}
