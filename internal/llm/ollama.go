// Package llm provides LLM client implementations.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// OllamaProvider implements the Provider interface for Ollama.
// Supports chat completion, embeddings, and summarization tasks.
type OllamaProvider struct {
	name          string // Provider instance name (e.g., "ollama-local")
	url           string
	model         string
	maxTokens     int // Output limit (0 = use model default)
	contextTokens int // Model's context window in tokens (queried from Ollama)
	dimensions    int // Embedding dimensions (detected on first embed)
	embeddingOnly bool // True if this is an embedding-only model (skip chat availability check)
	client        *http.Client
	available     bool
	mu            sync.RWMutex
}

// OllamaClient is an alias for OllamaProvider for backward compatibility.
// TODO: Remove after migration to unified provider system.
type OllamaClient = OllamaProvider

// ollamaShowRequest is the request body for /api/show
type ollamaShowRequest struct {
	Model string `json:"model"`
}

// ollamaShowResponse is the response from /api/show (partial - we only need model_info)
type ollamaShowResponse struct {
	ModelInfo map[string]interface{} `json:"model_info"`
}

// ollamaChatRequest is the request body for Ollama chat API
type ollamaChatRequest struct {
	Model    string              `json:"model"`
	Messages []ollamaChatMessage `json:"messages"`
	Stream   bool                `json:"stream"`
	Options  *ollamaOptions      `json:"options,omitempty"`
}

// ollamaOptions contains model options like context size
type ollamaOptions struct {
	NumCtx int `json:"num_ctx,omitempty"` // Context window size
}

// ollamaChatMessage represents a message in Ollama chat format
type ollamaChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ollamaChatResponse is the response from Ollama chat API
type ollamaChatResponse struct {
	Message ollamaChatMessage `json:"message"`
	Done    bool              `json:"done"`
}

// NewOllamaProvider creates a new Ollama provider from ProviderConfig.
// This is the preferred constructor for the unified provider system.
func NewOllamaProvider(name string, cfg ProviderConfig) (*OllamaProvider, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("ollama URL not configured")
	}

	url := strings.TrimSuffix(cfg.URL, "/")

	timeoutSeconds := cfg.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 300 // 5 minutes default
	}

	p := &OllamaProvider{
		name:          name,
		url:           url,
		model:         "", // Model set via WithModel()
		maxTokens:     cfg.MaxTokens,
		contextTokens: 4096, // Conservative default, updated by queryModelInfo
		embeddingOnly: cfg.EmbeddingOnly,
		client: &http.Client{
			Timeout: time.Duration(timeoutSeconds) * time.Second,
		},
		available: false,
	}

	L_debug("ollama provider created", "name", name, "url", url, "timeout", timeoutSeconds, "embeddingOnly", cfg.EmbeddingOnly)

	return p, nil
}

// NewOllamaClient creates a new Ollama LLM client for chat completion (legacy constructor).
// timeoutSeconds: request timeout (0 = use default 300s)
// contextTokensOverride: explicit context window (0 = auto-detect from model)
// TODO: Remove after migration to unified provider system.
func NewOllamaClient(url, model string, timeoutSeconds, contextTokensOverride int) *OllamaClient {
	// Normalize URL
	url = strings.TrimSuffix(url, "/")

	// Apply defaults
	if timeoutSeconds <= 0 {
		timeoutSeconds = 300 // 5 minutes default
	}

	// Use override if provided, otherwise conservative default (will be updated by queryModelInfo)
	contextTokens := 4096
	if contextTokensOverride > 0 {
		contextTokens = contextTokensOverride
	}

	c := &OllamaClient{
		name:          "ollama",
		url:           url,
		model:         model,
		contextTokens: contextTokens,
		client: &http.Client{
			Timeout: time.Duration(timeoutSeconds) * time.Second,
		},
		available: false,
	}

	if contextTokensOverride > 0 {
		L_info("ollama: client initialized (context override)", "url", url, "model", model, "timeout", timeoutSeconds, "contextTokens", contextTokensOverride)
		// Skip model info query since we have an explicit override
		go c.checkAvailability()
	} else {
		L_info("ollama: client initialized", "url", url, "model", model, "timeout", timeoutSeconds)
		// Query model info and test availability in background
		go c.initializeModel()
	}

	return c
}

// initializeModel queries model info and checks availability
func (c *OllamaClient) initializeModel() {
	// Query model info to get context window (skip for embedding-only models)
	if !c.embeddingOnly {
		c.queryModelInfo()
	}

	// Then check availability
	c.checkAvailability()
}

// queryModelInfo fetches model metadata from Ollama to get context window size
func (c *OllamaClient) queryModelInfo() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reqBody := ollamaShowRequest{Model: c.model}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		L_warn("ollama: failed to marshal show request", "error", err)
		return
	}

	url := c.url + "/api/show"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonData))
	if err != nil {
		L_warn("ollama: failed to create show request", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		L_warn("ollama: show request failed", "error", err, "model", c.model)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		L_warn("ollama: show request returned error", "status", resp.StatusCode, "body", string(body))
		return
	}

	var result ollamaShowResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		L_warn("ollama: failed to decode show response", "error", err)
		return
	}

	// Look for context_length in model_info
	// Different models use different keys, try common patterns
	contextLength := 0
	L_debug("ollama: model_info keys", "count", len(result.ModelInfo))
	for key, value := range result.ModelInfo {
		// Log keys that might be context-related for debugging
		keyLower := strings.ToLower(key)
		if strings.Contains(keyLower, "context") || strings.Contains(keyLower, "ctx") {
			L_debug("ollama: found context key", "key", key, "value", value)
		}
		if strings.Contains(keyLower, "context_length") {
			if v, ok := value.(float64); ok {
				contextLength = int(v)
				break
			}
		}
	}

	if contextLength > 0 {
		c.mu.Lock()
		c.contextTokens = contextLength
		c.mu.Unlock()
		L_info("ollama: detected model context window", "model", c.model, "contextTokens", contextLength)
	} else {
		L_warn("ollama: could not detect context window, using default", "model", c.model, "default", c.contextTokens)
	}
}

// ContextTokens returns the model's context window size in tokens
func (c *OllamaClient) ContextTokens() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.contextTokens
}

// checkAvailability tests if Ollama is reachable and the model is available
func (c *OllamaClient) checkAvailability() {
	// Use client's configured timeout - large models can take minutes to load
	timeout := c.client.Timeout
	if timeout < 120*time.Second {
		timeout = 120 * time.Second // Minimum 2 minutes for model loading
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	L_info("ollama: checking availability", "url", c.url, "model", c.model, "embeddingOnly", c.embeddingOnly, "timeout", timeout)

	var err error
	if c.embeddingOnly {
		// For embedding models, test with a simple embedding request
		// Use embedSingle directly to bypass IsAvailable check (we're testing availability!)
		_, err = c.embedSingle(ctx, "test")
	} else {
		// For chat models, try a simple chat request
		// SimpleMessage doesn't check IsAvailable, safe to call here
		_, err = c.SimpleMessage(ctx, "hi", "respond with 'ok'")
	}

	if err != nil {
		L_warn("ollama: not available", "error", err, "url", c.url, "model", c.model)
		c.mu.Lock()
		c.available = false
		c.mu.Unlock()
		return
	}

	c.mu.Lock()
	c.available = true
	c.mu.Unlock()

	L_info("ollama: client ready", "url", c.url, "model", c.model)
}

// Model returns the configured model name
func (c *OllamaClient) Model() string {
	return c.model
}

// IsAvailable returns true if the client is configured and ready
func (c *OllamaClient) IsAvailable() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.available
}

// Available is an alias for IsAvailable
func (c *OllamaClient) Available() bool {
	return c.IsAvailable()
}

// SimpleMessage sends a user message with a system prompt and returns the response.
// This is the interface used by compaction/checkpoint summarization.
// If the message exceeds the model's context window, it will be truncated with a warning.
func (c *OllamaClient) SimpleMessage(ctx context.Context, userMessage, systemPrompt string) (string, error) {
	startTime := time.Now()
	
	// Estimate chars limit from tokens (rough: 1 token ≈ 3 chars for English)
	// Reserve 20% for response generation
	c.mu.RLock()
	contextTokens := c.contextTokens
	c.mu.RUnlock()

	maxInputTokens := int(float64(contextTokens) * 0.8) // 80% for input
	maxInputChars := maxInputTokens * 3                  // ~3 chars per token

	totalChars := len(userMessage) + len(systemPrompt)
	L_info("llm: request started", "provider", c.name, "model", c.model, "chars", totalChars)
	L_debug("ollama: sending request",
		"promptLength", len(userMessage),
		"model", c.model,
		"totalChars", totalChars,
		"contextTokens", contextTokens,
		"maxInputChars", maxInputChars)

	// Truncate if exceeding context limit
	if totalChars > maxInputChars {
		// Reserve space for system prompt + buffer
		maxUserChars := maxInputChars - len(systemPrompt) - 500
		if maxUserChars < 1000 {
			maxUserChars = 1000 // Minimum useful content
		}

		if len(userMessage) > maxUserChars {
			truncatedMsg := userMessage[:maxUserChars]
			// Try to truncate at a sentence boundary
			if lastPeriod := strings.LastIndex(truncatedMsg, ". "); lastPeriod > maxUserChars/2 {
				truncatedMsg = truncatedMsg[:lastPeriod+1]
			}
			truncatedMsg += "\n\n[... conversation truncated due to context limit ...]"

			L_warn("ollama: truncating input to fit context",
				"originalChars", len(userMessage),
				"truncatedChars", len(truncatedMsg),
				"contextTokens", contextTokens,
				"maxInputChars", maxInputChars,
				"model", c.model)

			userMessage = truncatedMsg
		}
	}

	messages := []ollamaChatMessage{}

	// Add system prompt if provided
	if systemPrompt != "" {
		messages = append(messages, ollamaChatMessage{
			Role:    "system",
			Content: systemPrompt,
		})
	}

	// Add user message
	messages = append(messages, ollamaChatMessage{
		Role:    "user",
		Content: userMessage,
	})

	reqBody := ollamaChatRequest{
		Model:    c.model,
		Messages: messages,
		Stream:   false, // Non-streaming for simplicity in compaction use case
		Options: &ollamaOptions{
			NumCtx: contextTokens, // Use detected context window
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		L_error("ollama: failed to marshal request", "error", err)
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := c.url + "/api/chat"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonData))
	if err != nil {
		L_error("ollama: failed to create request", "error", err)
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	L_trace("ollama: request prepared", "url", url, "model", c.model, "messageCount", len(messages))

	resp, err := c.client.Do(req)
	if err != nil {
		// Mark unavailable on connection failures so fallback kicks in
		c.mu.Lock()
		c.available = false
		c.mu.Unlock()
		L_error("ollama: request failed, marking unavailable", "error", err)
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		errMsg := fmt.Sprintf("ollama returned status %d: %s", resp.StatusCode, string(body))
		L_error("ollama: request failed", "status", resp.StatusCode, "body", string(body))
		return "", fmt.Errorf(errMsg)
	}

	var result ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		L_error("ollama: failed to decode response", "error", err)
		return "", fmt.Errorf("decode response: %w", err)
	}

	responseText := result.Message.Content
	duration := time.Since(startTime)
	L_info("llm: request completed", "provider", c.name, "duration", duration.Round(time.Millisecond), "responseChars", len(responseText))
	L_debug("ollama: request completed", "responseLength", len(responseText))

	// Update availability on successful request
	c.mu.Lock()
	c.available = true
	c.mu.Unlock()

	return responseText, nil
}

// ============================================================================
// Provider interface methods
// ============================================================================

// Name returns the provider instance name
func (p *OllamaProvider) Name() string {
	return p.name
}

// Type returns the provider type
func (p *OllamaProvider) Type() string {
	return "ollama"
}

// WithModel returns a clone of the provider configured with a specific model
func (p *OllamaProvider) WithModel(model string) Provider {
	clone := *p
	clone.model = model
	// Initialize model in background
	go clone.initializeModel()
	return &clone
}

// WithModelForEmbedding returns a clone configured for embedding-only use.
// This skips chat availability checks and uses embedding API instead.
func (p *OllamaProvider) WithModelForEmbedding(model string) *OllamaProvider {
	clone := *p
	clone.model = model
	clone.embeddingOnly = true
	// Initialize model in background (will use embedding check)
	go clone.initializeModel()
	return &clone
}

// WithMaxTokens returns a clone of the provider with a different output limit
func (p *OllamaProvider) WithMaxTokens(max int) Provider {
	clone := *p
	clone.maxTokens = max
	return &clone
}

// StreamMessage is not supported by Ollama provider (use for embeddings/summarization only)
func (p *OllamaProvider) StreamMessage(
	ctx context.Context,
	messages []types.Message,
	toolDefs []types.ToolDefinition,
	systemPrompt string,
	onDelta func(delta string),
) (*Response, error) {
	return nil, ErrNotSupported{Provider: p.name, Operation: "StreamMessage (use Anthropic or OpenAI for agent)"}
}

// MaxTokens returns the current output limit
func (p *OllamaProvider) MaxTokens() int {
	return p.maxTokens
}

// ============================================================================
// Embedding support
// ============================================================================

// ollamaEmbedRequest is the request body for Ollama embeddings
type ollamaEmbedRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

// ollamaEmbedResponse is the response from Ollama embeddings
type ollamaEmbedResponse struct {
	Embedding []float64 `json:"embedding"`
}

// SupportsEmbeddings returns true - Ollama supports embeddings with appropriate models
func (p *OllamaProvider) SupportsEmbeddings() bool {
	return true
}

// EmbeddingDimensions returns the embedding vector dimensions (detected on first embed)
func (p *OllamaProvider) EmbeddingDimensions() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.dimensions
}

// Embed generates an embedding for a single text
func (p *OllamaProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	if !p.IsAvailable() {
		return nil, ErrUnavailable{Provider: p.name, Reason: "not connected"}
	}
	return p.embedSingle(ctx, text)
}

// EmbedBatch generates embeddings for multiple texts
func (p *OllamaProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if !p.IsAvailable() {
		return nil, ErrUnavailable{Provider: p.name, Reason: "not connected"}
	}

	L_debug("ollama: embedding batch", "count", len(texts))

	embeddings := make([][]float32, len(texts))
	for i, text := range texts {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		embedding, err := p.embedSingle(ctx, text)
		if err != nil {
			L_warn("ollama: failed to embed text in batch", "index", i, "error", err, "textLength", len(text))
			// Continue with nil embedding for this text
			continue
		}
		embeddings[i] = embedding
	}

	L_debug("ollama: batch embedded", "count", len(texts))
	return embeddings, nil
}

// embedSingle sends a single embedding request to Ollama
func (p *OllamaProvider) embedSingle(ctx context.Context, text string) ([]float32, error) {
	reqBody := ollamaEmbedRequest{
		Model:  p.model,
		Prompt: text,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := p.url + "/api/embeddings"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	L_trace("ollama: sending embed request", "url", url, "model", p.model, "textLength", len(text))

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama returned status %d: %s", resp.StatusCode, string(body))
	}

	var result ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	// Convert float64 to float32
	embedding := make([]float32, len(result.Embedding))
	for i, v := range result.Embedding {
		embedding[i] = float32(v)
	}

	// Update dimensions if not set
	if len(embedding) > 0 {
		p.mu.Lock()
		if p.dimensions == 0 {
			p.dimensions = len(embedding)
			L_debug("ollama: detected embedding dimensions", "dimensions", p.dimensions)
		}
		p.mu.Unlock()
	}

	return embedding, nil
}
