# LLM Provider Unification Specification

## Overview

Unify all LLM provider access behind a common interface. Currently GoClaw has:
- `anthropic.go` - Full-featured client (streaming, tools, caching)
- `ollama.go` - Simple client (summarization, embeddings)

This spec defines a unified provider system that:
- Supports multiple providers (Anthropic, OpenAI-compatible, Ollama)
- Allows multiple instances of same provider type (e.g., two Ollama endpoints)
- Centralizes config (DRY - define once, reference everywhere)
- Abstracts tool calling (native vs prompt-injection)
- Enables fallback chains

## Current Pain Points

### Config Duplication

Ollama URL repeated THREE times in `goclaw.json`:
```json
{
  "session": {
    "summarization": {
      "ollama": { "url": "http://192.168.1.100:11434", "model": "llama3.2:3b" }
    }
  },
  "memorySearch": {
    "ollama": { "url": "http://192.168.1.100:11434", "model": "nomic-embed-text" }
  },
  "transcript": {
    "ollama": { "url": "http://192.168.1.100:11434", "model": "nomic-embed-text" }
  }
}
```

Change the server? Edit three places. Add a new model? Copy-paste config.

### No Common Interface

```go
// anthropic.go
func (c *Client) StreamMessage(...) (*Response, error)

// ollama.go  
func (c *OllamaClient) SimpleMessage(...) (string, error)
```

Different method signatures, different types. Can't swap providers without code changes.

### No Fallback Support

When Anthropic returns "Overloaded", we're stuck. No automatic fallback to Kimi or local Ollama.

## New Config Structure

```json
{
  "llm": {
    "providers": {
      "anthropic": {
        "type": "anthropic",
        "apiKey": "sk-ant-..."
      },
      "kimi": {
        "type": "openai",
        "apiKey": "...",
        "baseURL": "https://api.moonshot.ai/v1"
      },
      "openai": {
        "type": "openai",
        "apiKey": "sk-...",
        "baseURL": "https://api.openai.com/v1"
      },
      "ollama-local": {
        "type": "ollama",
        "url": "http://localhost:11434"
      },
      "ollama-server": {
        "type": "ollama",
        "url": "http://192.168.1.100:11434"
      }
    },
    
    "models": {
      "main": "anthropic/claude-opus-4-5",
      "fast": "kimi/kimi-k2.5",
      "cheap": "ollama-server/llama3.2:3b",
      "compaction": "ollama-server/llama3.2:3b",
      "embeddings": "ollama-server/nomic-embed-text",
      "fallback": "anthropic/claude-3-haiku-20240307"
    },
    
    "default": "main",
    "fallback": ["fast", "cheap"]
  }
}
```

### Provider Types

| Type | Description | Tool Support |
|------|-------------|--------------|
| `anthropic` | Anthropic Claude API | Native |
| `openai` | OpenAI-compatible (GPT, Kimi, Groq, etc.) | Native |
| `ollama` | Local Ollama instance | Prompt injection |

### Model References

Format: `provider-name/model-name`

```
"anthropic/claude-opus-4-5"     → anthropic provider, claude-opus-4-5 model
"kimi/kimi-k2.5"                → kimi provider (openai type), kimi-k2.5 model
"ollama-server/llama3.2:3b"     → ollama-server provider, llama3.2:3b model
```

### Usage Throughout Config

Other sections reference models by alias:

```json
{
  "session": {
    "summarization": {
      "model": "compaction",
      "fallbackModel": "fallback",
      "timeoutSeconds": 120
    }
  },
  
  "memorySearch": {
    "enabled": true,
    "model": "embeddings"
  },
  
  "transcript": {
    "enabled": true,
    "model": "embeddings"
  }
}
```

Change Ollama server? ONE edit in `providers`. Everything else follows.

## Unified Provider Interface

```go
// llm/provider.go

package llm

import (
    "context"
    "encoding/json"
)

// Provider is the unified interface for all LLM backends
type Provider interface {
    // Identity
    Name() string        // Provider instance name ("anthropic", "ollama-local")
    Type() string        // Provider type ("anthropic", "openai", "ollama")
    Model() string       // Current model name
    IsAvailable() bool   // Ready to accept requests
    ContextTokens() int  // Model's context window size
    
    // Capabilities
    ToolStrategy() ToolStrategy  // How this provider handles tools
    
    // Simple completion - no tools, no streaming
    // Used for: compaction, summarization, simple queries
    SimpleMessage(ctx context.Context, userMessage, systemPrompt string) (string, error)
    
    // Full chat with streaming and tools
    // Used for: main agent loop
    StreamMessage(
        ctx context.Context,
        messages []Message,
        tools []ToolDefinition,
        systemPrompt string,
        callbacks StreamCallbacks,
    ) (*Response, error)
}

// StreamCallbacks handles streaming events
type StreamCallbacks struct {
    OnDelta   func(delta string)       // Text chunk received
    OnToolUse func(tool ToolUse)       // Tool call requested
    OnError   func(err error)          // Error during stream
}

// EmbeddingProvider is optional capability for providers that support embeddings
type EmbeddingProvider interface {
    Provider
    Embed(ctx context.Context, text string) ([]float32, error)
    EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
    EmbeddingDimensions() int  // Vector size (e.g., 768 for nomic-embed-text)
}

// SupportsEmbeddings checks if provider can do embeddings
func SupportsEmbeddings(p Provider) (EmbeddingProvider, bool) {
    e, ok := p.(EmbeddingProvider)
    return e, ok
}
```

## Message Types

```go
// Message represents a conversation message (provider-agnostic)
type Message struct {
    Role       string          // "user", "assistant", "system", "tool_result"
    Content    string          // Text content
    ToolUseID  string          // For tool_use/tool_result pairing
    ToolName   string          // Tool name (for tool_use)
    ToolInput  json.RawMessage // Tool input (for tool_use)
    Images     []Image         // Attached images (vision models)
}

// Image represents an attached image
type Image struct {
    MimeType string // "image/jpeg", "image/png", etc.
    Data     string // Base64-encoded data
    Source   string // Original source (for logging)
}

// Response from the LLM
type Response struct {
    Text       string          // Accumulated text response
    ToolUseID  string          // If tool use requested
    ToolName   string
    ToolInput  json.RawMessage
    StopReason string          // "end_turn", "tool_use", "max_tokens"
    
    // Token usage
    InputTokens         int
    OutputTokens        int
    CacheCreationTokens int  // Anthropic prompt caching
    CacheReadTokens     int
}

// HasToolUse returns true if the response contains a tool use request
func (r *Response) HasToolUse() bool {
    return r.ToolName != ""
}
```

## Tool Abstraction

### Tool Definition (Provider-Agnostic)

```go
// ToolDefinition describes a tool the model can use
type ToolDefinition struct {
    Name        string         `json:"name"`
    Description string         `json:"description"`
    Parameters  map[string]any `json:"parameters"`  // JSON Schema
}

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
```

### Tool Strategy

Different providers handle tools differently:

```go
// ToolStrategy indicates how a provider handles tool calling
type ToolStrategy int

const (
    ToolStrategyNone   ToolStrategy = iota  // No tool support
    ToolStrategyNative                       // Native function calling (Anthropic, OpenAI)
    ToolStrategyPrompt                       // Prompt injection (Ollama, older models)
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
```

### Prompt-Injection Tools

For providers without native tool support (Ollama), we inject tool definitions into the system prompt and parse tool calls from the response:

```go
// llm/prompttools/injector.go

package prompttools

// InjectToolsIntoPrompt appends tool descriptions to the system prompt
func InjectToolsIntoPrompt(systemPrompt string, tools []ToolDefinition) string {
    if len(tools) == 0 {
        return systemPrompt
    }
    
    var sb strings.Builder
    sb.WriteString(systemPrompt)
    sb.WriteString("\n\n## Available Tools\n\n")
    sb.WriteString("You have access to the following tools. To use a tool, respond with:\n")
    sb.WriteString("<tool_call>\n{\"name\": \"tool_name\", \"input\": {...}}\n</tool_call>\n\n")
    
    for _, tool := range tools {
        sb.WriteString(fmt.Sprintf("### %s\n", tool.Name))
        sb.WriteString(fmt.Sprintf("%s\n", tool.Description))
        if params, err := json.MarshalIndent(tool.Parameters, "", "  "); err == nil {
            sb.WriteString(fmt.Sprintf("Parameters: %s\n\n", string(params)))
        }
    }
    
    return sb.String()
}

// ParseToolCallFromResponse extracts tool calls from model response
// Returns: remaining text, tool call (if any), whether a tool call was found
func ParseToolCallFromResponse(response string) (string, *ToolUse, bool) {
    // Look for <tool_call>...</tool_call> pattern
    startTag := "<tool_call>"
    endTag := "</tool_call>"
    
    startIdx := strings.Index(response, startTag)
    if startIdx == -1 {
        return response, nil, false
    }
    
    endIdx := strings.Index(response[startIdx:], endTag)
    if endIdx == -1 {
        return response, nil, false
    }
    endIdx += startIdx + len(endTag)
    
    // Extract JSON
    jsonStart := startIdx + len(startTag)
    jsonStr := strings.TrimSpace(response[jsonStart : endIdx-len(endTag)])
    
    var toolCall struct {
        Name  string          `json:"name"`
        Input json.RawMessage `json:"input"`
    }
    if err := json.Unmarshal([]byte(jsonStr), &toolCall); err != nil {
        return response, nil, false
    }
    
    // Generate ID for pairing
    toolUse := &ToolUse{
        ID:    fmt.Sprintf("tool_%d", time.Now().UnixNano()),
        Name:  toolCall.Name,
        Input: toolCall.Input,
    }
    
    // Remove tool call from response text
    remainingText := response[:startIdx] + response[endIdx:]
    
    return strings.TrimSpace(remainingText), toolUse, true
}
```

## Provider Implementations

### Provider Factory

```go
// llm/factory.go

package llm

import "fmt"

// ProviderConfig is the config for a single provider instance
type ProviderConfig struct {
    Type    string `json:"type"`    // "anthropic", "openai", "ollama"
    APIKey  string `json:"apiKey"`  // For cloud providers
    BaseURL string `json:"baseURL"` // For OpenAI-compatible
    URL     string `json:"url"`     // For Ollama
    
    // Common options
    MaxTokens      int  `json:"maxTokens"`
    TimeoutSeconds int  `json:"timeoutSeconds"`
    PromptCaching  bool `json:"promptCaching"`  // Anthropic-specific
}

// NewProvider creates a provider instance from config
func NewProvider(name string, cfg ProviderConfig) (Provider, error) {
    switch cfg.Type {
    case "anthropic":
        return NewAnthropicProvider(name, cfg)
    case "openai":
        return NewOpenAIProvider(name, cfg)
    case "ollama":
        return NewOllamaProvider(name, cfg)
    default:
        return nil, fmt.Errorf("unknown provider type: %s", cfg.Type)
    }
}
```

### Anthropic Provider

```go
// llm/anthropic.go

type AnthropicProvider struct {
    name          string
    client        *anthropic.Client
    model         string
    maxTokens     int
    promptCaching bool
}

func (p *AnthropicProvider) Name() string           { return p.name }
func (p *AnthropicProvider) Type() string           { return "anthropic" }
func (p *AnthropicProvider) Model() string          { return p.model }
func (p *AnthropicProvider) ToolStrategy() ToolStrategy { return ToolStrategyNative }

func (p *AnthropicProvider) StreamMessage(
    ctx context.Context,
    messages []Message,
    tools []ToolDefinition,
    systemPrompt string,
    callbacks StreamCallbacks,
) (*Response, error) {
    // Convert to Anthropic format
    // Use native tool calling
    // Stream response
    // ... (existing implementation)
}

func (p *AnthropicProvider) SimpleMessage(ctx context.Context, userMessage, systemPrompt string) (string, error) {
    // Use StreamMessage with no tools, accumulate result
    var result string
    _, err := p.StreamMessage(ctx, 
        []Message{{Role: "user", Content: userMessage}},
        nil,
        systemPrompt,
        StreamCallbacks{OnDelta: func(d string) { result += d }},
    )
    return result, err
}
```

### OpenAI-Compatible Provider

```go
// llm/openai.go

type OpenAIProvider struct {
    name      string
    baseURL   string
    apiKey    string
    model     string
    maxTokens int
    client    *http.Client
}

func (p *OpenAIProvider) Name() string           { return p.name }
func (p *OpenAIProvider) Type() string           { return "openai" }
func (p *OpenAIProvider) Model() string          { return p.model }
func (p *OpenAIProvider) ToolStrategy() ToolStrategy { return ToolStrategyNative }

// Works with:
// - OpenAI (api.openai.com)
// - Kimi (api.moonshot.ai)
// - Groq (api.groq.com)
// - Azure OpenAI
// - Any OpenAI-compatible endpoint

func (p *OpenAIProvider) StreamMessage(...) (*Response, error) {
    // OpenAI chat completions API with streaming
    // Native function calling support
}
```

### Ollama Provider

```go
// llm/ollama.go

type OllamaProvider struct {
    name          string
    url           string
    model         string
    contextTokens int
    client        *http.Client
    available     bool
}

func (p *OllamaProvider) Name() string           { return p.name }
func (p *OllamaProvider) Type() string           { return "ollama" }
func (p *OllamaProvider) Model() string          { return p.model }
func (p *OllamaProvider) ToolStrategy() ToolStrategy { return ToolStrategyPrompt }

func (p *OllamaProvider) StreamMessage(
    ctx context.Context,
    messages []Message,
    tools []ToolDefinition,
    systemPrompt string,
    callbacks StreamCallbacks,
) (*Response, error) {
    // Inject tools into system prompt if any
    if len(tools) > 0 {
        systemPrompt = prompttools.InjectToolsIntoPrompt(systemPrompt, tools)
    }
    
    // Stream from Ollama /api/chat
    // Parse tool calls from response text
    // ...
}

// Embeddings support
func (p *OllamaProvider) Embed(ctx context.Context, text string) ([]float32, error) {
    // POST /api/embeddings
}

func (p *OllamaProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
    // Batch embedding calls
}
```

## Provider Registry

```go
// llm/registry.go

package llm

import (
    "fmt"
    "sync"
)

// Registry holds all configured providers
type Registry struct {
    providers map[string]Provider
    models    map[string]string  // alias -> "provider/model"
    fallback  []string           // fallback chain
    mu        sync.RWMutex
}

// NewRegistry creates a registry from config
func NewRegistry(cfg LLMConfig) (*Registry, error) {
    r := &Registry{
        providers: make(map[string]Provider),
        models:    cfg.Models,
        fallback:  cfg.Fallback,
    }
    
    // Initialize all providers
    for name, provCfg := range cfg.Providers {
        p, err := NewProvider(name, provCfg)
        if err != nil {
            return nil, fmt.Errorf("provider %s: %w", name, err)
        }
        r.providers[name] = p
    }
    
    return r, nil
}

// Get returns a provider by name
func (r *Registry) Get(name string) (Provider, bool) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    p, ok := r.providers[name]
    return p, ok
}

// GetModel returns a provider configured for a specific model alias
func (r *Registry) GetModel(alias string) (Provider, error) {
    r.mu.RLock()
    ref, ok := r.models[alias]
    r.mu.RUnlock()
    
    if !ok {
        return nil, fmt.Errorf("unknown model alias: %s", alias)
    }
    
    // Parse "provider/model" format
    parts := strings.SplitN(ref, "/", 2)
    if len(parts) != 2 {
        return nil, fmt.Errorf("invalid model reference: %s (expected provider/model)", ref)
    }
    
    providerName := parts[0]
    modelName := parts[1]
    
    provider, ok := r.Get(providerName)
    if !ok {
        return nil, fmt.Errorf("unknown provider: %s", providerName)
    }
    
    // Clone provider with specific model? Or providers are model-specific?
    // For now, assume provider is configured with the model
    return provider, nil
}

// GetWithFallback tries providers in order until one succeeds
func (r *Registry) GetWithFallback(primary string) Provider {
    // Try primary
    if p, ok := r.Get(primary); ok && p.IsAvailable() {
        return p
    }
    
    // Try fallback chain
    for _, name := range r.fallback {
        if p, ok := r.Get(name); ok && p.IsAvailable() {
            return p
        }
    }
    
    return nil
}
```

## Fallback Behavior

When a provider fails (overloaded, timeout, error), try the next in the fallback chain:

```go
// Example: Main request with fallback
func (g *Gateway) chat(ctx context.Context, messages []Message, ...) (*Response, error) {
    providers := []string{g.config.LLM.Default}
    providers = append(providers, g.config.LLM.Fallback...)
    
    var lastErr error
    for _, name := range providers {
        provider, err := g.registry.GetModel(name)
        if err != nil {
            continue
        }
        
        if !provider.IsAvailable() {
            L_debug("provider unavailable, trying next", "provider", name)
            continue
        }
        
        resp, err := provider.StreamMessage(ctx, messages, tools, systemPrompt, callbacks)
        if err != nil {
            lastErr = err
            L_warn("provider failed, trying fallback", "provider", name, "error", err)
            continue
        }
        
        return resp, nil
    }
    
    return nil, fmt.Errorf("all providers failed, last error: %w", lastErr)
}
```

## Provider Capabilities Matrix

| Provider | Stream | Tools | Embeddings | Caching | Vision |
|----------|--------|-------|------------|---------|--------|
| Anthropic | ✅ | Native | ❌ | ✅ | ✅ |
| OpenAI | ✅ | Native | ✅ | ❌ | ✅ |
| Kimi (OpenAI) | ✅ | Native | ? | ❌ | ✅ |
| Ollama | ✅ | Prompt | ✅ | ❌ | ✅* |

*Depends on model

## Pricing Reference

| Provider | Model | Input | Output |
|----------|-------|-------|--------|
| Anthropic | Claude Opus | $15/M | $75/M |
| Anthropic | Claude Sonnet | $3/M | $15/M |
| Anthropic | Claude Haiku | $0.25/M | $1.25/M |
| Kimi | K2.5 | $0.60/M | $2.50/M |
| OpenAI | GPT-4o | $2.50/M | $10/M |
| Ollama | Any | Free | Free |

## Migration Path

### Phase 1: Interface Definition
- [ ] Define `Provider` interface in `llm/provider.go`
- [ ] Define `Message`, `Response`, `ToolDefinition` types
- [ ] Define `ToolStrategy` and prompt injection helpers

### Phase 2: Refactor Existing
- [ ] Rename `Client` → `AnthropicProvider`, implement interface
- [ ] Rename `OllamaClient` → `OllamaProvider`, implement interface
- [ ] Add `SimpleMessage` to Anthropic (via StreamMessage)
- [ ] Add `StreamMessage` to Ollama (with prompt-injection tools)

### Phase 3: Add OpenAI Provider
- [ ] Implement `OpenAIProvider`
- [ ] Test with OpenAI, Kimi, Groq endpoints
- [ ] Native function calling support

### Phase 4: Registry & Config
- [ ] Implement `Registry`
- [ ] Update config structure
- [ ] Migrate existing goclaw.json configs
- [ ] Update all references (session, memorySearch, transcript)

### Phase 5: Fallback & Resilience
- [ ] Implement fallback chain in gateway
- [ ] Add circuit breaker for failing providers
- [ ] Add provider health checks

## Example Final Config

```json
{
  "agent": {
    "name": "Ratpup",
    "emoji": "🐀"
  },
  
  "llm": {
    "providers": {
      "anthropic": {
        "type": "anthropic",
        "apiKey": "sk-ant-...",
        "promptCaching": true
      },
      "kimi": {
        "type": "openai",
        "apiKey": "...",
        "baseURL": "https://api.moonshot.ai/v1"
      },
      "ollama": {
        "type": "ollama",
        "url": "http://192.168.1.100:11434"
      }
    },
    
    "models": {
      "main": "anthropic/claude-opus-4-5",
      "fast": "kimi/kimi-k2.5", 
      "compaction": "ollama/llama3.2:3b",
      "embeddings": "ollama/nomic-embed-text",
      "fallback": "anthropic/claude-3-haiku-20240307"
    },
    
    "default": "main",
    "fallback": ["fast", "fallback"]
  },
  
  "session": {
    "store": "sqlite",
    "storePath": "~/.openclaw/goclaw/sessions.db",
    "summarization": {
      "model": "compaction",
      "fallbackModel": "fallback",
      "timeoutSeconds": 120,
      "checkpoint": {
        "enabled": true,
        "thresholds": [25, 50, 75]
      }
    }
  },
  
  "memorySearch": {
    "enabled": true,
    "model": "embeddings"
  },
  
  "transcript": {
    "enabled": true,
    "model": "embeddings"
  },
  
  "tools": {
    "web": { "braveApiKey": "..." },
    "browser": { "enabled": true, "headless": false }
  },
  
  "telegram": { "enabled": true, "botToken": "..." },
  "http": { "listen": ":1337" },
  "cron": { "enabled": true }
}
```

## Summary

| Before | After |
|--------|-------|
| Ollama URL in 3 places | Define once in `providers` |
| Different client interfaces | Unified `Provider` interface |
| No fallback on failure | Automatic fallback chain |
| Anthropic-only for main | Any provider for any task |
| Hardcoded tool handling | Strategy-based (native vs prompt) |

This unification makes GoClaw provider-agnostic while keeping config DRY and enabling resilience through fallbacks.
