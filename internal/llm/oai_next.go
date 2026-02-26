package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/metadata"
	. "github.com/roelfdiedericks/goclaw/internal/metrics"
	"github.com/roelfdiedericks/goclaw/internal/tokens"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// OaiNextProvider implements Provider + StatefulProvider for OpenAI's Responses API
// over WebSocket. Designed for low-latency agentic workflows with context chaining
// via previous_response_id.
type OaiNextProvider struct {
	name             string
	config           LLMProviderConfig
	model            string
	maxTokens        int
	metadataProvider string
	metricPrefix     string

	// WebSocket connection (lazy init, persistent)
	ws   *oaiWSConn
	wsMu sync.Mutex

	// Context preservation state (saved/loaded via StatefulProvider)
	responseID       string
	lastMessageCount int
}

// NewOaiNextProvider creates a new oai-next provider from LLMProviderConfig.
func NewOaiNextProvider(name string, cfg LLMProviderConfig) (*OaiNextProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("oai-next: apiKey is required")
	}

	incrementalContext := true
	if cfg.IncrementalContext != nil {
		incrementalContext = *cfg.IncrementalContext
	}

	L_debug("oai-next provider created",
		"name", name,
		"maxTokens", cfg.MaxTokens,
		"incrementalContext", incrementalContext,
		"serverTools", cfg.ServerToolsAllowed,
	)

	return &OaiNextProvider{
		name:             name,
		config:           cfg,
		model:            "",
		maxTokens:        cfg.MaxTokens,
		metadataProvider: metadata.Get().ResolveProvider(cfg.Subtype, cfg.Driver, cfg.BaseURL),
	}, nil
}

// =============================================================================
// Identity Methods (Provider interface)
// =============================================================================

func (p *OaiNextProvider) Name() string             { return p.name }
func (p *OaiNextProvider) Type() string             { return "oai-next" }
func (p *OaiNextProvider) MetadataProvider() string { return p.metadataProvider }
func (p *OaiNextProvider) Model() string            { return p.model }

func (p *OaiNextProvider) WithModel(model string) Provider {
	return &OaiNextProvider{
		name:             p.name,
		config:           p.config,
		model:            model,
		maxTokens:        p.maxTokens,
		metadataProvider: p.metadataProvider,
		metricPrefix:     fmt.Sprintf("llm/%s/%s/%s", p.Type(), p.Name(), model),
	}
}

func (p *OaiNextProvider) WithMaxTokens(max int) Provider {
	return &OaiNextProvider{
		name:             p.name,
		config:           p.config,
		model:            p.model,
		maxTokens:        max,
		metadataProvider: p.metadataProvider,
		metricPrefix:     p.metricPrefix,
		ws:               p.ws,
		responseID:       p.responseID,
		lastMessageCount: p.lastMessageCount,
	}
}

// =============================================================================
// Availability Methods
// =============================================================================

func (p *OaiNextProvider) IsAvailable() bool {
	return p.config.APIKey != "" && p.model != ""
}

func (p *OaiNextProvider) MaxTokens() int {
	if p.maxTokens > 0 {
		return p.maxTokens
	}
	if p.metadataProvider != "" {
		if model, ok := metadata.Get().GetModel(p.metadataProvider, p.model); ok && model.MaxOutputTokens > 0 {
			return int(model.MaxOutputTokens)
		}
	}
	return DefaultMaxOutputTokens
}

func (p *OaiNextProvider) ContextTokens() int {
	if p.config.ContextTokens > 0 {
		return p.config.ContextTokens
	}
	if p.metadataProvider != "" {
		if ctx := metadata.Get().GetContextWindow(p.metadataProvider, p.model); ctx > 0 {
			return int(ctx)
		}
	}
	return DefaultContextTokens
}

// =============================================================================
// WebSocket Connection Management
// =============================================================================

func (p *OaiNextProvider) getWS(ctx context.Context) (*oaiWSConn, error) {
	p.wsMu.Lock()
	defer p.wsMu.Unlock()

	if p.ws == nil {
		p.ws = newOaiWSConn(p.config.APIKey)
	}

	if err := p.ws.ensureConnected(ctx); err != nil {
		return nil, err
	}

	return p.ws, nil
}

// =============================================================================
// Chat — StreamMessage
// =============================================================================

func (p *OaiNextProvider) StreamMessage(
	ctx context.Context,
	messages []types.Message,
	toolDefs []types.ToolDefinition,
	systemPrompt string,
	onDelta func(delta string),
	opts *StreamOptions,
) (*Response, error) {
	startTime := time.Now()
	contextWindow := p.ContextTokens()

	ws, err := p.getWS(ctx)
	if err != nil {
		return nil, fmt.Errorf("oai-next: connection failed: %w", err)
	}

	incrementalMode := true
	if p.config.IncrementalContext != nil {
		incrementalMode = *p.config.IncrementalContext
	}

	storeVal := incrementalMode
	req := p.buildRequest(messages, toolDefs, systemPrompt, opts, incrementalMode, &storeVal)

	usedPreviousResponseID := req.PreviousResponseID != ""

	// Estimate input tokens for metrics
	reqBytes, _ := json.Marshal(req)
	estimator := tokens.Get()
	estimatedInput := estimator.Count(string(reqBytes))

	L_info("oai-next: request started",
		"provider", p.name,
		"model", p.model,
		"messages", len(messages),
		"tools", len(req.Tools),
		"incremental", usedPreviousResponseID,
		"estimatedTokens", estimatedInput,
	)

	// Send request
	if err := ws.sendRequest(ctx, req); err != nil {
		// Connection issue — try reconnect + full context
		if usedPreviousResponseID {
			L_warn("oai-next: send failed with incremental, retrying full",
				"error", err,
			)
			return p.retryWithFullContext(ctx, ws, messages, toolDefs, systemPrompt, opts, incrementalMode, startTime)
		}
		return nil, fmt.Errorf("oai-next: send failed: %w", err)
	}

	// Process streaming events
	resp, err := p.processEvents(ctx, ws, onDelta, opts, toolDefs)
	if err != nil {
		// Check for previous_response_not_found
		if usedPreviousResponseID && isPreviousResponseNotFound(err) {
			L_warn("oai-next: previous_response_not_found, retrying with full context",
				"expiredResponseID", p.responseID,
			)
			p.responseID = ""
			p.lastMessageCount = 0
			return p.retryWithFullContext(ctx, ws, messages, toolDefs, systemPrompt, opts, incrementalMode, startTime)
		}

		// Transient error — try reconnect + retry once
		if isWSTransientError(err) && ctx.Err() == nil {
			L_warn("oai-next: transient error, reconnecting and retrying",
				"error", err,
			)
			time.Sleep(1 * time.Second)
			if reconnErr := ws.reconnect(ctx); reconnErr != nil {
				return nil, fmt.Errorf("oai-next: reconnect failed: %w", reconnErr)
			}
			p.responseID = ""
			p.lastMessageCount = 0
			return p.retryWithFullContext(ctx, ws, messages, toolDefs, systemPrompt, opts, incrementalMode, startTime)
		}

		return nil, err
	}

	p.lastMessageCount = len(messages)

	elapsed := time.Since(startTime)
	L_info("oai-next: request completed",
		"provider", p.name,
		"duration", elapsed.Round(time.Millisecond),
		"inputTokens", resp.InputTokens,
		"outputTokens", resp.OutputTokens,
	)

	// Record metrics
	if p.metricPrefix != "" {
		MetricDuration(p.metricPrefix, "request", elapsed)
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
		if contextWindow > 0 {
			usagePercent := float64(resp.InputTokens) / float64(contextWindow) * 100.0
			MetricSet(p.metricPrefix, "context_window", int64(contextWindow))
			MetricSet(p.metricPrefix, "context_used", int64(resp.InputTokens))
			MetricThreshold(p.metricPrefix, "context_usage_percent", usagePercent, 100.0)
		}
		emitCostMetrics(p.metricPrefix, PurposeFromContext(ctx), p.config, p.metadataProvider, p.model, resp)
	}

	return resp, nil
}

// retryWithFullContext reconnects if needed and sends a full-context request.
func (p *OaiNextProvider) retryWithFullContext(
	ctx context.Context,
	ws *oaiWSConn,
	messages []types.Message,
	toolDefs []types.ToolDefinition,
	systemPrompt string,
	opts *StreamOptions,
	incrementalMode bool,
	startTime time.Time,
) (*Response, error) {
	if !ws.isConnected() {
		if err := ws.reconnect(ctx); err != nil {
			return nil, fmt.Errorf("oai-next: reconnect failed: %w", err)
		}
	}

	p.responseID = ""
	p.lastMessageCount = 0
	storeVal := incrementalMode
	req := p.buildRequest(messages, toolDefs, systemPrompt, opts, false, &storeVal)

	if err := ws.sendRequest(ctx, req); err != nil {
		return nil, fmt.Errorf("oai-next: retry send failed: %w", err)
	}

	resp, err := p.processEvents(ctx, ws, nil, opts, toolDefs)
	if err != nil {
		return nil, fmt.Errorf("oai-next: retry failed: %w", err)
	}

	p.lastMessageCount = len(messages)
	return resp, nil
}

// =============================================================================
// Chat — SimpleMessage (for summarization)
// =============================================================================

func (p *OaiNextProvider) SimpleMessage(ctx context.Context, userMessage, systemPrompt string) (string, error) {
	messages := []types.Message{
		{Role: "user", Content: userMessage},
	}

	var result string
	_, err := p.StreamMessage(ctx, messages, nil, systemPrompt, func(delta string) {
		result += delta
	}, nil)
	if err != nil {
		return "", err
	}

	return result, nil
}

// =============================================================================
// StatefulProvider Interface
// =============================================================================

func (p *OaiNextProvider) LoadSessionState(state map[string]any) {
	if state == nil {
		p.responseID = ""
		p.lastMessageCount = 0
		return
	}

	if rid, ok := state["responseID"].(string); ok {
		p.responseID = rid
	} else {
		p.responseID = ""
	}

	if count, ok := state["lastMessageCount"].(float64); ok {
		p.lastMessageCount = int(count)
	} else {
		p.lastMessageCount = 0
	}

	L_trace("oai-next: loaded session state",
		"responseID", p.responseID != "",
		"lastMessageCount", p.lastMessageCount,
	)
}

func (p *OaiNextProvider) SaveSessionState() map[string]any {
	if p.responseID == "" {
		return nil
	}

	state := map[string]any{
		"responseID":       p.responseID,
		"lastMessageCount": p.lastMessageCount,
	}

	L_trace("oai-next: saving session state",
		"responseID", p.responseID != "",
		"lastMessageCount", p.lastMessageCount,
	)

	return state
}

// =============================================================================
// Embedding Methods — not supported, use openai driver for embeddings
// =============================================================================

func (p *OaiNextProvider) SupportsEmbeddings() bool { return false }
func (p *OaiNextProvider) Embed(_ context.Context, _ string) ([]float32, error) {
	return nil, fmt.Errorf("oai-next does not support embeddings")
}
func (p *OaiNextProvider) EmbedBatch(_ context.Context, _ []string) ([][]float32, error) {
	return nil, fmt.Errorf("oai-next does not support embeddings")
}
func (p *OaiNextProvider) EmbeddingDimensions() int { return 0 }

// =============================================================================
// Setup Wizard interfaces
// =============================================================================

// ListModels fetches available models from OpenAI's REST API.
func (p *OaiNextProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	if p.config.APIKey == "" {
		return nil, fmt.Errorf("API key required to list models")
	}

	modelsURL := "https://api.openai.com/v1/models"
	req, err := http.NewRequestWithContext(ctx, "GET", modelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	models := make([]ModelInfo, 0, len(result.Data))
	for _, m := range result.Data {
		if m.ID == "" {
			continue
		}
		models = append(models, ModelInfo{
			ID:          m.ID,
			DisplayName: m.ID,
		})
	}

	return models, nil
}

func (p *OaiNextProvider) TestConnection(ctx context.Context) error {
	_, err := p.ListModels(ctx)
	if err != nil {
		return fmt.Errorf("connection test failed: %w", err)
	}
	return nil
}

func (p *OaiNextProvider) GetSubtypes() []ProviderSubtype {
	return []ProviderSubtype{}
}

// =============================================================================
// Request Building
// =============================================================================

func (p *OaiNextProvider) buildRequest(
	messages []types.Message,
	toolDefs []types.ToolDefinition,
	systemPrompt string,
	opts *StreamOptions,
	useIncremental bool,
	store *bool,
) *oaiRequest {
	req := &oaiRequest{
		Type:            "response.create",
		Model:           p.model,
		Store:           store,
		MaxOutputTokens: p.MaxTokens(),
	}

	usePreviousResponse := useIncremental && p.responseID != "" && p.lastMessageCount > 0 && len(messages) > p.lastMessageCount

	if usePreviousResponse {
		req.PreviousResponseID = p.responseID
		for _, msg := range messages[p.lastMessageCount:] {
			items := p.convertMessage(msg)
			req.Input = append(req.Input, items...)
		}
		L_debug("oai-next: incremental request",
			"responseID", p.responseID,
			"previousCount", p.lastMessageCount,
			"newMessages", len(messages)-p.lastMessageCount,
		)
	} else {
		if systemPrompt != "" {
			req.Instructions = systemPrompt
		}
		for _, msg := range messages {
			items := p.convertMessage(msg)
			req.Input = append(req.Input, items...)
		}
	}

	// Add server-side tools
	serverToolNames := p.addServerTools(req)

	// Add client-side tools (with conflict prefixing)
	p.addClientTools(req, toolDefs, serverToolNames)

	return req
}

// =============================================================================
// Message Conversion
// =============================================================================

func (p *OaiNextProvider) convertMessage(msg types.Message) []oaiInputItem {
	switch msg.Role {
	case "user":
		return p.convertUserMessage(msg)
	case "assistant":
		if msg.Content == "" {
			return nil
		}
		return []oaiInputItem{{
			Type: oaiItemTypeMessage,
			Role: "assistant",
			Content: []oaiContentPart{{
				Type: "output_text",
				Text: msg.Content,
			}},
		}}
	case "tool_use":
		return []oaiInputItem{{
			Type:      oaiItemTypeFunctionCall,
			CallID:    msg.ToolUseID,
			Name:      msg.ToolName,
			Arguments: string(msg.ToolInput),
			Status:    "completed",
		}}
	case "tool_result":
		if msg.ToolUseID == "" {
			L_warn("oai-next: tool_result with empty ToolUseID, skipping")
			return nil
		}
		return []oaiInputItem{{
			Type:   oaiItemTypeFunctionCallOutput,
			CallID: msg.ToolUseID,
			Output: msg.Content,
		}}
	default:
		L_warn("oai-next: unknown message role, skipping", "role", msg.Role)
		return nil
	}
}

func (p *OaiNextProvider) convertUserMessage(msg types.Message) []oaiInputItem {
	var parts []oaiContentPart

	if msg.Content != "" {
		parts = append(parts, oaiContentPart{
			Type: "input_text",
			Text: msg.Content,
		})
	}

	for _, block := range msg.ContentBlocks {
		switch block.Type {
		case "text":
			if block.Text != "" {
				parts = append(parts, oaiContentPart{
					Type: "input_text",
					Text: block.Text,
				})
			}
		case "image":
			if block.Data != "" {
				dataURL := fmt.Sprintf("data:%s;base64,%s", block.MimeType, block.Data)
				parts = append(parts, oaiContentPart{
					Type:     "input_image",
					ImageURL: dataURL,
				})
			}
		}
	}

	if len(parts) == 0 {
		return nil
	}

	return []oaiInputItem{{
		Type:    oaiItemTypeMessage,
		Role:    "user",
		Content: parts,
	}}
}

// =============================================================================
// Tool Handling
// =============================================================================

func (p *OaiNextProvider) addServerTools(req *oaiRequest) []string {
	var names []string
	req.Tools = append(req.Tools, oaiToolDef{Type: "web_search"})
	names = append(names, "web_search")

	L_debug("oai-next: server tools configured", "added", names)
	return names
}

func (p *OaiNextProvider) addClientTools(req *oaiRequest, toolDefs []types.ToolDefinition, serverToolNames []string) {
	if len(toolDefs) == 0 {
		return
	}

	for _, td := range toolDefs {
		schemaJSON, err := json.Marshal(td.InputSchema)
		if err != nil {
			L_warn("oai-next: failed to marshal tool schema, skipping",
				"tool", td.Name, "error", err)
			continue
		}

		toolName := td.Name
		for _, serverName := range serverToolNames {
			if td.Name == serverName {
				toolName = clientToolPrefix + td.Name
				L_debug("oai-next: prefixed client tool to avoid conflict",
					"original", td.Name, "prefixed", toolName)
				break
			}
		}

		req.Tools = append(req.Tools, oaiToolDef{
			Type:        "function",
			Name:        toolName,
			Description: td.Description,
			Parameters:  schemaJSON,
		})
	}

	L_debug("oai-next: client tools configured", "count", len(toolDefs))
}

// =============================================================================
// Stream Processing
// =============================================================================

func (p *OaiNextProvider) processEvents(
	ctx context.Context,
	ws *oaiWSConn,
	onDelta func(delta string),
	opts *StreamOptions,
	toolDefs []ToolDefinition,
) (*Response, error) {
	clientToolNames := make(map[string]bool)
	for _, td := range toolDefs {
		clientToolNames[td.Name] = true
		clientToolNames[clientToolPrefix+td.Name] = true
	}

	var (
		textBuilder      strings.Builder
		reasoningBuilder strings.Builder
		responseID       string
		usage            *oaiUsage
		toolCall         *oaiOutputItem // first client tool call
		clientToolCount  int
	)

	for {
		event, err := ws.readEvent(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("oai-next: stream error: %w", err)
		}

		switch event.Type {
		case oaiEventResponseCreated:
			if event.Response != nil {
				responseID = event.Response.ID
				L_debug("oai-next: response created", "id", responseID)
			}

		case oaiEventOutputItemAdded:
			if event.Item != nil {
				L_trace("oai-next: output item added",
					"type", event.Item.Type,
					"id", event.Item.ID,
				)
			}

		case oaiEventOutputTextDelta:
			if event.Delta != "" {
				textBuilder.WriteString(event.Delta)
				if onDelta != nil {
					onDelta(event.Delta)
				}
			}

		case oaiEventReasoningTextDelta, oaiEventReasoningSummaryDelta:
			if event.Delta != "" {
				reasoningBuilder.WriteString(event.Delta)
				if opts != nil && opts.OnThinkingDelta != nil {
					opts.OnThinkingDelta(event.Delta)
				}
			}

		case oaiEventOutputItemDone:
			if event.Item != nil {
				p.handleOutputItemDone(event.Item, opts, clientToolNames, &toolCall, &clientToolCount)
			}

		case oaiEventResponseDone, oaiEventResponseCompleted:
			if event.Response != nil {
				if event.Response.ID != "" {
					responseID = event.Response.ID
				}
				usage = event.Response.Usage
			}
			goto done

		case oaiEventError:
			errMsg := "unknown error"
			errCode := ""
			if event.Error != nil {
				errMsg = event.Error.Message
				errCode = event.Error.Code
			}
			L_error("oai-next: server error",
				"code", errCode,
				"message", errMsg,
				"status", event.Status,
			)
			return nil, &oaiServerError{
				Code:    errCode,
				Message: errMsg,
				Status:  event.Status,
			}

		default:
			L_trace("oai-next: unhandled event", "type", event.Type)
		}
	}

done:
	p.responseID = responseID

	resp := &Response{
		Text:     textBuilder.String(),
		Thinking: reasoningBuilder.String(),
	}

	if usage != nil {
		resp.InputTokens = usage.InputTokens
		resp.OutputTokens = usage.OutputTokens
		if usage.InputTokensDetails != nil {
			resp.CacheReadTokens = usage.InputTokensDetails.CachedTokens
		}
		if usage.OutputTokensDetails != nil {
			resp.ReasoningTokens = usage.OutputTokensDetails.ReasoningTokens
		}
	}

	// Extract tool call
	if toolCall != nil {
		toolName := toolCall.Name
		if strings.HasPrefix(toolName, clientToolPrefix) {
			toolName = strings.TrimPrefix(toolName, clientToolPrefix)
		}
		resp.ToolUseID = toolCall.CallID
		resp.ToolName = toolName
		resp.ToolInput = json.RawMessage(toolCall.Arguments)
		resp.StopReason = "tool_use"
		if clientToolCount > 1 {
			L_warn("oai-next: multiple tool calls, processing first only",
				"total", clientToolCount, "processing", toolName)
		}
	} else {
		resp.StopReason = "end_turn"
	}

	L_debug("oai-next: stream complete",
		"responseID", responseID,
		"textLen", len(resp.Text),
		"thinkingLen", len(resp.Thinking),
		"stopReason", resp.StopReason,
		"inputTokens", resp.InputTokens,
		"outputTokens", resp.OutputTokens,
	)

	return resp, nil
}

// handleOutputItemDone processes a finalized output item (tool call, server tool, message).
func (p *OaiNextProvider) handleOutputItemDone(
	item *oaiOutputItem,
	opts *StreamOptions,
	clientToolNames map[string]bool,
	toolCall **oaiOutputItem,
	clientToolCount *int,
) {
	switch item.Type {
	case oaiItemTypeFunctionCall:
		if clientToolNames[item.Name] {
			if *toolCall == nil {
				*toolCall = item
			}
			*clientToolCount++
			L_debug("oai-next: client tool call",
				"name", item.Name, "callID", item.CallID)
		} else {
			L_debug("oai-next: ignoring non-client tool call",
				"name", item.Name, "callID", item.CallID)
		}

	case oaiItemTypeWebSearchCall:
		L_info("oai-next: server tool", "tool", "web_search",
			"id", item.ID, "status", item.Status)
		if opts != nil && opts.OnThinkingDelta != nil {
			formatted := fmt.Sprintf("\n[web_search] (%s)\n", item.Status)
			opts.OnThinkingDelta(formatted)
		}
		if opts != nil && opts.OnServerToolCall != nil {
			opts.OnServerToolCall("web_search", string(item.Action), item.Status, "")
		}

	case oaiItemTypeCodeInterpreter:
		L_info("oai-next: server tool", "tool", "code_interpreter",
			"id", item.ID, "status", item.Status)
		if opts != nil && opts.OnThinkingDelta != nil {
			formatted := fmt.Sprintf("\n[code_interpreter] (%s)\n", item.Status)
			opts.OnThinkingDelta(formatted)
		}
		if opts != nil && opts.OnServerToolCall != nil {
			opts.OnServerToolCall("code_interpreter", "", item.Status, "")
		}
	}
}

// =============================================================================
// Error Types and Helpers
// =============================================================================

type oaiServerError struct {
	Code    string
	Message string
	Status  int
}

func (e *oaiServerError) Error() string {
	return fmt.Sprintf("oai-next: server error [%s] (HTTP %d): %s", e.Code, e.Status, e.Message)
}

func isPreviousResponseNotFound(err error) bool {
	if se, ok := err.(*oaiServerError); ok {
		return se.Code == "previous_response_not_found"
	}
	return false
}

func isWSTransientError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "websocket") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "eof") ||
		strings.Contains(msg, "server_error")
}
