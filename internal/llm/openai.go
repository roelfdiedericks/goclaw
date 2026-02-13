// Package llm provides LLM client implementations.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	openai "github.com/sashabaranov/go-openai"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	. "github.com/roelfdiedericks/goclaw/internal/metrics"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// openRouterTransport adds GoClaw attribution headers to OpenRouter requests
type openRouterTransport struct {
	base http.RoundTripper
}

func (t *openRouterTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("HTTP-Referer", "https://goclaw.org")
	req.Header.Set("X-Title", "GoClaw")
	if t.base == nil {
		return http.DefaultTransport.RoundTrip(req)
	}
	return t.base.RoundTrip(req)
}

// OpenAIProvider implements the Provider interface for OpenAI-compatible APIs.
// Supports streaming, native tool calling, vision (images), and embeddings.
// Works with OpenAI, Kimi, LM Studio, OpenRouter, and other compatible APIs via BaseURL.
type OpenAIProvider struct {
	name          string // Provider instance name (e.g., "openai", "kimi", "lmstudio")
	client        *openai.Client
	model         string
	maxTokens     int
	contextTokens int    // Context window size override (0 = auto-detect from model name)
	apiKey        string // Stored for cloning
	baseURL       string // Custom API base URL
	metricPrefix  string // e.g., "llm/openai/kimi/kimi-k2.5"

	// Embedding support
	embeddingOnly       bool // If true, only used for embeddings (not chat)
	embeddingDimensions int  // Cached embedding dimensions (detected on first use)

	// Per-provider trace logging control
	traceEnabled bool // If false, suppress L_trace calls for this provider

	// Model metadata cache (context_length from /v1/models endpoint)
	// Populated at startup if the provider supports extended model metadata
	modelContextCache map[string]int

	// HTTP transport for capturing request/response (for error dumps)
	transport     *CapturingTransport
	dumpOnSuccess bool // Keep dumps even on success (for debugging)

	// Thread-safe availability tracking
	mu        sync.RWMutex
	available bool
}

// NewOpenAIProvider creates a new OpenAI-compatible provider from ProviderConfig.
// Supports both "baseUrl" (standard) and "url" (for compatibility with Ollama-style configs).
// API key is optional for local servers like LM Studio.
func NewOpenAIProvider(name string, cfg ProviderConfig) (*OpenAIProvider, error) {
	// Determine the base URL - accept both "baseUrl" and "url" fields
	baseURL := cfg.BaseURL
	if baseURL == "" && cfg.URL != "" {
		baseURL = cfg.URL
	}

	// API key is optional for local servers (LM Studio, LocalAI, etc.)
	apiKey := cfg.APIKey
	if apiKey == "" {
		apiKey = "not-needed" // Placeholder for local servers that don't require auth
	}

	// Build client config
	config := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		// Ensure the URL ends with /v1 for OpenAI-compatible APIs
		if !strings.HasSuffix(baseURL, "/v1") && !strings.HasSuffix(baseURL, "/v1/") {
			baseURL = strings.TrimSuffix(baseURL, "/") + "/v1"
		}
		config.BaseURL = baseURL
	}

	// Create capturing transport for request/response debugging
	// For OpenRouter, wrap with header transport first
	var baseTransport http.RoundTripper = http.DefaultTransport
	if strings.Contains(strings.ToLower(baseURL), "openrouter") {
		baseTransport = &openRouterTransport{base: http.DefaultTransport}
		L_debug("openai: using OpenRouter headers", "referer", "https://goclaw.org", "title", "GoClaw")
	}
	transport := &CapturingTransport{Base: baseTransport}
	config.HTTPClient = &http.Client{Transport: transport}

	client := openai.NewClientWithConfig(config)

	maxTokens := cfg.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8192
	}

	displayURL := baseURL
	if displayURL == "" {
		displayURL = "(default)"
	}

	// Determine trace enabled - default to true if not explicitly set to false
	traceEnabled := true
	if cfg.Trace != nil && !*cfg.Trace {
		traceEnabled = false
	}

	L_debug("openai provider created", "name", name, "baseURL", displayURL, "maxTokens", maxTokens, "contextTokens", cfg.ContextTokens, "trace", traceEnabled)

	p := &OpenAIProvider{
		name:          name,
		client:        client,
		model:         "", // Model set via WithModel()
		maxTokens:     maxTokens,
		contextTokens: cfg.ContextTokens,
		apiKey:        cfg.APIKey,
		baseURL:       baseURL,
		traceEnabled:  traceEnabled,
		transport:     transport,
		dumpOnSuccess: cfg.DumpOnSuccess,
	}

	// Fetch model metadata from /v1/models endpoint (if supported)
	// This populates context_length for accurate context window detection
	if baseURL != "" {
		p.fetchModelMetadata(baseURL, apiKey)
	}

	return p, nil
}

// fetchModelMetadata fetches model metadata from the /v1/models endpoint.
// This is an OpenAI-compatible endpoint that some providers (like OpenRouter)
// extend with additional fields like context_length.
// The fetch has a 10s timeout and failures are logged but don't block startup.
func (p *OpenAIProvider) fetchModelMetadata(baseURL, apiKey string) {
	// Build the models endpoint URL
	modelsURL := strings.TrimSuffix(baseURL, "/") + "/models"

	// Create HTTP client with timeout
	client := &http.Client{Timeout: 10 * time.Second}

	req, err := http.NewRequest("GET", modelsURL, nil)
	if err != nil {
		L_debug("openai: failed to create models request", "provider", p.name, "error", err)
		return
	}

	// Add auth header if API key is provided and not a placeholder
	if apiKey != "" && apiKey != "not-needed" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Accept", "application/json")

	L_debug("openai: fetching model metadata", "provider", p.name, "url", modelsURL)

	resp, err := client.Do(req)
	if err != nil {
		L_debug("openai: model metadata fetch failed", "provider", p.name, "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		L_debug("openai: model metadata fetch returned non-200", "provider", p.name, "status", resp.StatusCode)
		return
	}

	// Read the full response body so we can log it if needed
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		L_debug("openai: failed to read models response", "provider", p.name, "error", err)
		return
	}

	// Parse the response - we expect {"data": [...]} or {"object": "list", "data": [...]}
	// Different providers use different field names for context length
	var result struct {
		Data []struct {
			ID               string `json:"id"`
			ContextLength    *int   `json:"context_length"`     // OpenRouter
			MaxContextLength *int   `json:"max_context_length"` // LM Studio, others
			ContextWindow    *int   `json:"context_window"`     // Some providers
			NCtx             *int   `json:"n_ctx"`              // llama.cpp style
			MaxModelLen      *int   `json:"max_model_len"`      // vLLM
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		L_debug("openai: failed to parse models response", "provider", p.name, "error", err)
		return
	}

	// Build the cache - coalesce all possible context length fields
	cache := make(map[string]int)
	for _, model := range result.Data {
		if model.ID == "" {
			continue
		}
		// Try each field in priority order
		var ctx int
		switch {
		case model.ContextLength != nil && *model.ContextLength > 0:
			ctx = *model.ContextLength
		case model.MaxContextLength != nil && *model.MaxContextLength > 0:
			ctx = *model.MaxContextLength
		case model.ContextWindow != nil && *model.ContextWindow > 0:
			ctx = *model.ContextWindow
		case model.NCtx != nil && *model.NCtx > 0:
			ctx = *model.NCtx
		case model.MaxModelLen != nil && *model.MaxModelLen > 0:
			ctx = *model.MaxModelLen
		}
		if ctx > 0 {
			cache[model.ID] = ctx
		}
	}

	if len(cache) > 0 {
		p.modelContextCache = cache
		L_info("openai: cached model context windows", "provider", p.name, "models", len(cache))
	} else {
		// Log raw response at DEBUG to help identify new field names
		sample := string(body)
		if len(sample) > 1000 {
			sample = sample[:1000] + "... (truncated)"
		}
		L_debug("openai: no context_length data in models response",
			"provider", p.name,
			"modelsReturned", len(result.Data),
			"rawResponse", sample,
		)
	}
}

// trace logs a trace message if tracing is enabled for this provider.
// Use this instead of L_trace for per-provider trace control.
func (p *OpenAIProvider) trace(msg string, args ...any) {
	if p.traceEnabled {
		L_trace(msg, args...)
	}
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

// WithModelForEmbedding returns a clone configured for embedding-only use.
// Initialization is synchronous (blocking) because embeddings are typically
// needed immediately when GetProvider("embeddings") is called.
func (p *OpenAIProvider) WithModelForEmbedding(model string) *OpenAIProvider {
	clone := *p
	clone.model = model
	clone.embeddingOnly = true
	clone.metricPrefix = fmt.Sprintf("llm/%s/%s/%s", p.Type(), p.Name(), model)
	// Initialize synchronously - test that embeddings actually work
	clone.checkEmbeddingAvailability()
	return &clone
}

// checkEmbeddingAvailability tests if the embedding endpoint works
func (p *OpenAIProvider) checkEmbeddingAvailability() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	L_info("openai: checking embedding availability", "name", p.name, "model", p.model, "baseURL", p.baseURL)

	// Test with a simple embedding request
	req := openai.EmbeddingRequest{
		Model: openai.EmbeddingModel(p.model),
		Input: []string{"test"},
	}

	resp, err := p.client.CreateEmbeddings(ctx, req)
	if err != nil {
		L_warn("openai: embedding not available", "error", err, "name", p.name, "model", p.model)
		p.mu.Lock()
		p.available = false
		p.mu.Unlock()
		return
	}

	if len(resp.Data) > 0 && len(resp.Data[0].Embedding) > 0 {
		p.mu.Lock()
		p.available = true
		p.embeddingDimensions = len(resp.Data[0].Embedding)
		p.mu.Unlock()
		L_info("openai: embedding ready", "name", p.name, "model", p.model, "dimensions", len(resp.Data[0].Embedding))
	} else {
		L_warn("openai: embedding returned empty data", "name", p.name, "model", p.model)
		p.mu.Lock()
		p.available = false
		p.mu.Unlock()
	}
}

// IsAvailable returns true if the provider is configured and ready
func (p *OpenAIProvider) IsAvailable() bool {
	if p == nil || p.client == nil || p.model == "" {
		return false
	}
	// For embedding-only providers, check the availability flag (set by initialization)
	if p.embeddingOnly {
		p.mu.RLock()
		defer p.mu.RUnlock()
		return p.available
	}
	// For chat providers, always available if configured
	return true
}

// ContextTokens returns the model's context window size in tokens.
// Priority: 1) Config override, 2) Cached from /v1/models, 3) Hardcoded patterns, 4) Default
func (p *OpenAIProvider) ContextTokens() int {
	// 1. Config override always wins
	if p.contextTokens > 0 {
		return p.contextTokens
	}

	// 2. Check cache from /v1/models endpoint (populated at startup)
	if p.modelContextCache != nil {
		if ctx, ok := p.modelContextCache[p.model]; ok && ctx > 0 {
			return ctx
		}
	}

	// 3. Fall back to hardcoded patterns / default
	return getOpenAIModelContextWindow(p.model)
}

// MaxTokens returns the current output limit
func (p *OpenAIProvider) MaxTokens() int {
	return p.maxTokens
}

// Embed generates an embedding for a single text using the OpenAI-compatible /v1/embeddings endpoint.
// Works with OpenAI, LM Studio, and other compatible APIs.
func (p *OpenAIProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	if !p.embeddingOnly {
		return nil, ErrNotSupported{Provider: p.name, Operation: "embeddings (not configured as embedding provider)"}
	}

	req := openai.EmbeddingRequest{
		Model: openai.EmbeddingModel(p.model),
		Input: []string{text},
	}

	resp, err := p.client.CreateEmbeddings(ctx, req)
	if err != nil {
		L_error("openai: embedding failed", "error", err, "model", p.model)
		return nil, fmt.Errorf("embedding failed: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("embedding returned no data")
	}

	// Convert float64 to float32
	embedding := make([]float32, len(resp.Data[0].Embedding))
	for i, v := range resp.Data[0].Embedding {
		embedding[i] = float32(v)
	}

	// Cache dimensions on first successful embedding
	if p.embeddingDimensions == 0 && len(embedding) > 0 {
		p.mu.Lock()
		p.embeddingDimensions = len(embedding)
		p.mu.Unlock()
		L_debug("openai: cached embedding dimensions", "dimensions", len(embedding), "model", p.model)
	}

	return embedding, nil
}

// EmbedBatch generates embeddings for multiple texts in a single request.
func (p *OpenAIProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if !p.embeddingOnly {
		return nil, ErrNotSupported{Provider: p.name, Operation: "embeddings (not configured as embedding provider)"}
	}

	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	req := openai.EmbeddingRequest{
		Model: openai.EmbeddingModel(p.model),
		Input: texts,
	}

	resp, err := p.client.CreateEmbeddings(ctx, req)
	if err != nil {
		L_error("openai: batch embedding failed", "error", err, "model", p.model, "count", len(texts))
		return nil, fmt.Errorf("batch embedding failed: %w", err)
	}

	// Convert response to [][]float32, maintaining input order
	result := make([][]float32, len(texts))
	for _, data := range resp.Data {
		if data.Index >= len(result) {
			continue
		}
		embedding := make([]float32, len(data.Embedding))
		for i, v := range data.Embedding {
			embedding[i] = float32(v)
		}
		result[data.Index] = embedding
	}

	// Cache dimensions on first successful batch
	if p.embeddingDimensions == 0 && len(result) > 0 && len(result[0]) > 0 {
		p.mu.Lock()
		p.embeddingDimensions = len(result[0])
		p.mu.Unlock()
		L_debug("openai: cached embedding dimensions from batch", "dimensions", len(result[0]), "model", p.model)
	}

	L_debug("openai: batch embedding complete", "count", len(result), "model", p.model)
	return result, nil
}

// EmbeddingDimensions returns the embedding vector dimensions.
// Returns cached value or 0 if not yet determined.
func (p *OpenAIProvider) EmbeddingDimensions() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.embeddingDimensions
}

// SupportsEmbeddings returns true if this provider is configured for embeddings
func (p *OpenAIProvider) SupportsEmbeddings() bool {
	return p.embeddingOnly
}

// getOpenAIModelContextWindow returns the context window size for a given model
func getOpenAIModelContextWindow(model string) int {
	model = strings.ToLower(model)

	// Claude models (including OpenRouter format like "anthropic/claude-opus-4.5")
	if strings.Contains(model, "claude") {
		if strings.Contains(model, "opus") || strings.Contains(model, "sonnet") {
			return 200000 // 200K context for Claude 3+ Opus/Sonnet
		}
		if strings.Contains(model, "haiku") {
			return 200000 // Haiku also has 200K
		}
		return 100000 // Conservative default for Claude
	}
	// Kimi models
	if strings.HasPrefix(model, "kimi-k2") || strings.Contains(model, "kimi-k2") {
		return 262144 // 256K context (256 * 1024)
	}
	// DeepSeek models (OpenRouter format: "deepseek/deepseek-v3.2")
	if strings.Contains(model, "deepseek") {
		return 128000 // 128K context
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
	// Default: conservative limit for unknown/local models
	// Use contextTokens in provider config to override for specific models
	return 4096
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
	}, nil)
	if err != nil {
		return "", err
	}

	return result, nil
}

// StreamMessage sends a message to the LLM and streams the response
// onDelta is called for each text chunk received
// opts is currently unused for OpenAI but accepted for interface compliance
func (p *OpenAIProvider) StreamMessage(
	ctx context.Context,
	messages []types.Message,
	toolDefs []types.ToolDefinition,
	systemPrompt string,
	onDelta func(delta string),
	opts *StreamOptions,
) (*Response, error) {
	// Note: OpenAI reasoning models (o1, etc.) handle thinking differently
	// The ReasoningContent field is already captured from the response
	_ = opts // Currently unused, reasoning is automatic for compatible models
	startTime := time.Now()
	contextWindow := p.ContextTokens()
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
		p.trace("system prompt set", "length", len(systemPrompt))
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
		// Log tool names for debugging
		var toolNames []string
		for _, t := range openaiTools {
			if t.Function != nil {
				toolNames = append(toolNames, t.Function.Name)
			}
		}
		p.trace("tools attached", "count", len(openaiTools), "names", toolNames)
	}

	// Calculate approximate request size for debugging
	reqBytes, _ := json.Marshal(req)
	reqSizeKB := len(reqBytes) / 1024

	// Log request details for debugging (trace level to avoid clutter)
	p.trace("openai: sending request",
		"provider", p.name,
		"model", p.model,
		"baseURL", p.baseURL,
		"maxTokens", p.maxTokens,
		"messageCount", len(openaiMessages),
		"toolCount", len(openaiTools),
		"requestSizeKB", reqSizeKB,
	)

	// Log first few messages for debugging (roles and content lengths)
	for i, msg := range openaiMessages {
		if i >= 5 {
			p.trace("openai: request messages truncated", "shown", 5, "total", len(openaiMessages))
			break
		}
		contentLen := len(msg.Content)
		if len(msg.MultiContent) > 0 {
			contentLen = len(msg.MultiContent)
		}
		p.trace("openai: request message",
			"idx", i,
			"role", msg.Role,
			"contentLen", contentLen,
			"toolCallsCount", len(msg.ToolCalls),
			"toolCallID", msg.ToolCallID,
		)
	}

	// Start dump for debugging (captures request context)
	dumpCtx := StartDump(p.name, p.model, p.baseURL, openaiMessages, openaiTools, systemPrompt, 1)

	// Stream the response
	stream, err := p.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		// Log the full request details on error for debugging
		L_error("stream creation failed - request details",
			"provider", p.name,
			"baseURL", p.baseURL,
			"model", p.model,
			"maxTokens", p.maxTokens,
			"messageCount", len(openaiMessages),
			"toolCount", len(openaiTools),
			"requestSizeKB", reqSizeKB,
			"stream", req.Stream,
		)

		// Log message roles summary
		roleCounts := make(map[string]int)
		for _, msg := range openaiMessages {
			roleCounts[string(msg.Role)]++
		}
		L_error("stream creation failed - message roles", "roles", roleCounts)

		// Try to extract detailed error information
		var apiErr *openai.APIError
		var reqErr *openai.RequestError
		if errors.As(err, &apiErr) {
			L_error("stream creation failed (APIError)",
				"provider", p.name,
				"model", p.model,
				"statusCode", apiErr.HTTPStatusCode,
				"status", apiErr.HTTPStatus,
				"code", apiErr.Code,
				"message", apiErr.Message,
				"type", apiErr.Type,
				"param", apiErr.Param,
			)
		} else if errors.As(err, &reqErr) {
			L_error("stream creation failed (RequestError)",
				"provider", p.name,
				"model", p.model,
				"statusCode", reqErr.HTTPStatusCode,
				"status", reqErr.HTTPStatus,
				"error", reqErr.Error(),
			)
		} else {
			L_error("stream creation failed", "provider", p.name, "model", p.model, "error", err)
		}
		// Dump full request/response to file for debugging
		FinishDumpError(dumpCtx, err, p.transport)

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
	chunkNum := 0

	// Hybrid logging state tracking
	const emptyChunkThreshold = 50 // Warn after this many consecutive empty chunks
	emptyChunkCount := 0
	firstContentLogged := false
	toolCallsStarted := make(map[int]bool) // Track which tool call indices we've logged start for

	for {
		chunk, err := stream.Recv()
		if err != nil {
			if err.Error() == "EOF" {
				// Stream complete - log summary
				p.trace("openai: stream complete",
					"provider", p.name,
					"totalChunks", chunkNum,
					"duration", time.Since(startTime).Round(time.Millisecond),
					"textLen", len(response.Text),
					"toolCallsCount", len(toolCalls),
				)
				break
			}
			// Try to extract detailed error information (same as stream creation errors)
			var apiErr *openai.APIError
			var reqErr *openai.RequestError
			if errors.As(err, &apiErr) {
				L_error("stream recv failed (APIError)",
					"provider", p.name,
					"model", p.model,
					"statusCode", apiErr.HTTPStatusCode,
					"code", apiErr.Code,
					"message", apiErr.Message,
					"type", apiErr.Type,
				)
			} else if errors.As(err, &reqErr) {
				L_error("stream recv failed (RequestError)",
					"provider", p.name,
					"model", p.model,
					"statusCode", reqErr.HTTPStatusCode,
					"error", reqErr.Error(),
				)
			} else {
				// For other errors (like JSON parse failures), log the raw error
				// This catches things like "unexpected end of JSON input" from providers
				// that return non-JSON error responses (e.g., LM Studio context overflow)
				L_error("stream recv failed",
					"provider", p.name,
					"model", p.model,
					"error", err,
					"errorType", fmt.Sprintf("%T", err),
				)
			}
			// Dump full request/response to file for debugging
			FinishDumpError(dumpCtx, err, p.transport)

			// Record metrics for failed request
			if p.metricPrefix != "" {
				MetricDuration(p.metricPrefix, "request", time.Since(startTime))
				MetricFailWithReason(p.metricPrefix, "request_status", "stream_error")
			}
			return nil, fmt.Errorf("stream error: %w", err)
		}

		chunkNum++

		if len(chunk.Choices) == 0 {
			// Empty chunk (no choices) - count it
			emptyChunkCount++
			if emptyChunkCount == emptyChunkThreshold {
				L_warn("openai: potential hang - many consecutive empty chunks",
					"provider", p.name,
					"emptyChunks", emptyChunkCount,
					"chunk", chunkNum,
				)
			} else if emptyChunkCount > 0 && emptyChunkCount%100 == 0 {
				// Periodic update for very long empty stretches
				L_warn("openai: still waiting - empty chunks continuing",
					"provider", p.name,
					"emptyChunks", emptyChunkCount,
					"chunk", chunkNum,
					"elapsed", time.Since(startTime).Round(time.Millisecond),
				)
			}
			continue
		}

		choice := chunk.Choices[0]

		// Determine if this chunk is "empty" (no meaningful content)
		hasContent := choice.Delta.Content != ""
		hasReasoning := choice.Delta.ReasoningContent != ""
		hasToolCalls := len(choice.Delta.ToolCalls) > 0
		hasFinishReason := choice.FinishReason != ""
		isEmptyChunk := !hasContent && !hasReasoning && !hasToolCalls && !hasFinishReason

		if isEmptyChunk {
			emptyChunkCount++
			if emptyChunkCount == emptyChunkThreshold {
				L_warn("openai: potential hang - many consecutive empty chunks",
					"provider", p.name,
					"emptyChunks", emptyChunkCount,
					"chunk", chunkNum,
				)
			} else if emptyChunkCount > 0 && emptyChunkCount%100 == 0 {
				L_warn("openai: still waiting - empty chunks continuing",
					"provider", p.name,
					"emptyChunks", emptyChunkCount,
					"chunk", chunkNum,
					"elapsed", time.Since(startTime).Round(time.Millisecond),
				)
			}
			continue
		}

		// Non-empty chunk - log transition from empty if applicable
		if emptyChunkCount > 0 {
			p.trace("openai: content after empty chunks",
				"provider", p.name,
				"emptyChunks", emptyChunkCount,
				"chunk", chunkNum,
			)
			emptyChunkCount = 0
		}

		// Handle reasoning/thinking content (Kimi, Deepseek, etc.)
		if hasReasoning {
			reasoningContent += choice.Delta.ReasoningContent
			// Don't log every reasoning delta - too verbose
		}

		// Handle text content
		if hasContent {
			// Log first content received
			if !firstContentLogged {
				preview := choice.Delta.Content
				if len(preview) > 50 {
					preview = preview[:50] + "..."
				}
				p.trace("openai: first content received",
					"provider", p.name,
					"chunk", chunkNum,
					"preview", preview,
				)
				firstContentLogged = true
			}
			response.Text += choice.Delta.Content
			if onDelta != nil {
				onDelta(choice.Delta.Content)
			}
		}

		// Handle tool calls
		for _, tc := range choice.Delta.ToolCalls {
			// Determine tool call index
			idx := 0
			if tc.Index != nil {
				idx = *tc.Index
			}

			// Ensure toolCalls slice is large enough
			for len(toolCalls) <= idx {
				toolCalls = append(toolCalls, openai.ToolCall{})
			}

			// Log tool call start (first time we see this index with an ID or name)
			if !toolCallsStarted[idx] && (tc.ID != "" || tc.Function.Name != "") {
				toolCallsStarted[idx] = true
				p.trace("openai: tool call started",
					"provider", p.name,
					"chunk", chunkNum,
					"idx", idx,
					"id", tc.ID,
					"name", tc.Function.Name,
				)
			}

			// Accumulate tool call data silently
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
		if hasFinishReason {
			response.StopReason = string(choice.FinishReason)
			p.trace("openai: finish_reason received",
				"provider", p.name,
				"chunk", chunkNum,
				"finishReason", choice.FinishReason,
				"toolCallsCount", len(toolCalls),
				"textLen", len(response.Text),
			)
		}

		// Capture usage from stream (comes with include_usage option)
		if chunk.Usage != nil {
			response.InputTokens = chunk.Usage.PromptTokens
			response.OutputTokens = chunk.Usage.CompletionTokens
		}
	}

	// Log each completed tool call (significant event)
	for i, tc := range toolCalls {
		if tc.ID != "" {
			p.trace("openai: tool call complete",
				"provider", p.name,
				"idx", i,
				"id", tc.ID,
				"name", tc.Function.Name,
				"argsLen", len(tc.Function.Arguments),
			)
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
		L_info("llm: tool use detected", "provider", p.name, "tool", tc.Function.Name, "id", tc.ID)
	} else if len(toolCalls) > 0 {
		// Tool calls exist but first one has empty ID - log this edge case
		L_warn("openai: tool_calls present but first ID empty",
			"provider", p.name,
			"count", len(toolCalls),
			"firstID", toolCalls[0].ID,
			"firstName", toolCalls[0].Function.Name,
			"firstArgs", toolCalls[0].Function.Arguments,
		)
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
	usagePercent := 0.0
	if contextWindow > 0 {
		usagePercent = float64(response.InputTokens) / float64(contextWindow) * 100.0
	}
	L_info("llm: request completed", "provider", p.name, "duration", elapsed.Round(time.Millisecond),
		"inputTokens", response.InputTokens, "outputTokens", response.OutputTokens)

	// Log final response summary (not full text - too verbose)
	p.trace("openai: response summary",
		"provider", p.name,
		"textLen", len(response.Text),
		"stopReason", response.StopReason,
		"toolName", response.ToolName,
		"thinkingLen", len(response.Thinking),
		"hasToolUse", response.HasToolUse(),
	)

	// Record metrics
	if p.metricPrefix != "" {
		MetricDuration(p.metricPrefix, "request", elapsed)
		MetricAdd(p.metricPrefix, "input_tokens", int64(response.InputTokens))
		MetricAdd(p.metricPrefix, "output_tokens", int64(response.OutputTokens))
		MetricOutcome(p.metricPrefix, "stop_reason", response.StopReason)
		MetricSuccess(p.metricPrefix, "request_status")

		// Context window metrics (contextWindow calculated at request start)
		if contextWindow > 0 {
			MetricSet(p.metricPrefix, "context_window", int64(contextWindow))
			MetricSet(p.metricPrefix, "context_used", int64(response.InputTokens))
			MetricThreshold(p.metricPrefix, "context_usage_percent", usagePercent, 100.0)
		}
	}

	// Finalize dump (delete on success unless dumpOnSuccess is enabled)
	FinishDumpSuccess(dumpCtx, p.dumpOnSuccess)

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
				if content == "" {
					content = "(no output)"
				}
				toolName := msg.ToolName
				if toolName == "" {
					toolName = "unknown"
				}
				text := fmt.Sprintf("[Tool result for %s]\n%s", toolName, content)
				result = append(result, openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleUser,
					Content: text,
				})
				continue
			}

			// OpenAI uses "tool" role for tool results
			// Content is required - use placeholder if empty (some providers reject empty content)
			toolContent := msg.Content
			if toolContent == "" {
				toolContent = "(no output)"
			}
			result = append(result, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    toolContent,
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
