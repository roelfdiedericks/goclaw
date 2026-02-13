// Package llm provides unified LLM provider interfaces and implementations.
package llm

import (
	"context"
	"encoding/json"

	"github.com/roelfdiedericks/goclaw/internal/types"
)

// Provider is the unified interface for all LLM backends.
// Implementations: AnthropicProvider, OllamaProvider, OpenAIProvider
type Provider interface {
	// Identity
	Name() string  // Provider instance name (e.g., "anthropic", "ollama-local")
	Type() string  // Provider type (e.g., "anthropic", "openai", "ollama")
	Model() string // Current model name

	// Cloning with overrides
	WithModel(model string) Provider    // Clone with different model
	WithMaxTokens(max int) Provider     // Clone with output limit override

	// Availability
	IsAvailable() bool  // Ready to accept requests
	ContextTokens() int // Model's context window size
	MaxTokens() int     // Current output limit

	// Chat - Simple (no tools, no streaming, for summarization)
	SimpleMessage(ctx context.Context, userMessage, systemPrompt string) (string, error)

	// Chat - Full streaming with tools
	StreamMessage(
		ctx context.Context,
		messages []types.Message,
		toolDefs []types.ToolDefinition,
		systemPrompt string,
		onDelta func(delta string),
		opts *StreamOptions,
	) (*Response, error)

	// Embeddings
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	EmbeddingDimensions() int
	SupportsEmbeddings() bool
}

// Message represents a conversation message (provider-agnostic).
// Can be converted from session.Message for use with providers.
type Message struct {
	Role      string          `json:"role"`      // "user", "assistant", "system", "tool_result"
	Content   string          `json:"content"`
	ToolUseID string          `json:"toolUseId,omitempty"` // For tool_use/tool_result pairing
	ToolName  string          `json:"toolName,omitempty"`  // Tool name (for tool_use)
	ToolInput json.RawMessage `json:"toolInput,omitempty"` // Tool input (for tool_use)
	Images    []Image         `json:"images,omitempty"`    // Attached images (vision models)
}

// Image represents an attached image for multimodal models
type Image struct {
	MimeType string `json:"mimeType"` // "image/jpeg", "image/png", etc.
	Data     string `json:"data"`     // Base64-encoded data
	Source   string `json:"source"`   // Original source path (for logging)
}

// StreamOptions contains optional parameters for StreamMessage
type StreamOptions struct {
	// EnableThinking enables extended thinking for models that support it.
	// When true, the provider will try to enable thinking mode.
	// If the model doesn't support it, the request is retried without thinking.
	EnableThinking bool

	// ThinkingBudget is the token budget for thinking (default: 10000)
	ThinkingBudget int

	// OnThinkingDelta is called for each thinking content delta during streaming.
	// If nil, thinking content is still captured but not streamed.
	OnThinkingDelta func(delta string)
}

// Note: Response type is currently defined in anthropic.go
// It will be moved here when anthropic.go is refactored to implement Provider

// ToolDefinition is an alias to types.ToolDefinition for convenience within llm package.
type ToolDefinition = types.ToolDefinition

// ToolUse represents a tool call from the model
type ToolUse struct {
	ID    string          `json:"id"`    // For pairing with results
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolResult is what we send back after executing a tool
type ToolResult struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

// ToolStrategy indicates how a provider handles tool calling
type ToolStrategy int

const (
	ToolStrategyNone   ToolStrategy = iota // No tool support
	ToolStrategyNative                     // Native function calling (Anthropic, OpenAI)
	ToolStrategyPrompt                     // Prompt injection (Ollama, older models)
)

func (s ToolStrategy) String() string {
	switch s {
	case ToolStrategyNone:
		return "none"
	case ToolStrategyNative:
		return "native"
	case ToolStrategyPrompt:
		return "prompt"
	default:
		return "unknown"
	}
}

// ErrNotSupported is returned when a provider doesn't support an operation
type ErrNotSupported struct {
	Provider  string
	Operation string
}

func (e ErrNotSupported) Error() string {
	return e.Provider + " does not support " + e.Operation
}

// ErrUnavailable is returned when a provider is not available
type ErrUnavailable struct {
	Provider string
	Reason   string
}

func (e ErrUnavailable) Error() string {
	if e.Reason != "" {
		return e.Provider + " is unavailable: " + e.Reason
	}
	return e.Provider + " is unavailable"
}

// ProviderConfig is the configuration for a single provider instance
type ProviderConfig struct {
	Type           string `json:"type"`           // "anthropic", "openai", "ollama"
	APIKey         string `json:"apiKey"`         // For cloud providers
	BaseURL        string `json:"baseURL"`        // For OpenAI-compatible endpoints
	URL            string `json:"url"`            // For Ollama
	MaxTokens      int    `json:"maxTokens"`      // Output limit override
	ContextTokens  int    `json:"contextTokens"`  // Context window size override (0 = auto-detect)
	TimeoutSeconds int    `json:"timeoutSeconds"` // Request timeout
	PromptCaching  bool   `json:"promptCaching"`  // Anthropic-specific
	EmbeddingOnly  bool   `json:"embeddingOnly"`  // For embedding-only models (skip chat availability check)
	Trace          *bool  `json:"trace"`          // Per-provider trace logging (nil = default enabled when -t flag used)
}
