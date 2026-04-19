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
	"github.com/roelfdiedericks/goclaw/internal/types"
	openai "github.com/sashabaranov/go-openai"
)

const (
	llamaCppPropsTTL             = 60 * time.Second
	llamaCppHealthProbeTimeout   = 2 * time.Second
	llamaCppEmbeddingProbeTimout = 30 * time.Second
	llamaCppDefaultContextTokens = 4096
)

type llamaCppPropsResponse struct {
	TotalSlots                int `json:"total_slots"`
	DefaultGenerationSettings struct {
		NCtx int `json:"n_ctx"`
	} `json:"default_generation_settings"`
}

// LlamaCppProvider implements the Provider interface for llama-server's
// OpenAI-compatible endpoints without embedding OpenAIProvider.
type LlamaCppProvider struct {
	name             string
	client           *openai.Client
	httpClient       *http.Client
	model            string
	maxTokens        int
	contextTokens    int
	apiKey           string
	baseURL          string
	serverRoot       string
	metricPrefix     string
	metadataProvider string
	config           LLMProviderConfig

	embeddingOnly       bool
	embeddingDimensions int

	traceEnabled  bool
	transport     *CapturingTransport
	chatAugment   *LlamaCppChatAugmentTransport
	dumpOnSuccess bool

	mu        sync.RWMutex
	available bool

	propsMu                sync.RWMutex
	cachedPropsNCtx        int
	cachedPropsTotalSlots  int
	cachedPropsFetchedTime time.Time

	// Session state (StatefulProvider) + live slot binding for this clone
	slotMu        sync.Mutex
	slotPersisted int // persisted lease target; -1 if none / unpinned
}

func normalizeOpenAICompatBaseURL(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return ""
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(baseURL, "/v1") {
		baseURL += "/v1"
	}
	return baseURL
}

func llamaCppServerRoot(v1BaseURL string) string {
	root := strings.TrimSpace(v1BaseURL)
	root = strings.TrimRight(root, "/")
	if strings.HasSuffix(root, "/v1") {
		root = strings.TrimSuffix(root, "/v1")
	}
	return root
}

func NewLlamaCppProvider(name string, cfg LLMProviderConfig) (*LlamaCppProvider, error) {
	baseURL := normalizeOpenAICompatBaseURL(cfg.BaseURL)
	serverRoot := llamaCppServerRoot(baseURL)

	clientAPIKey := cfg.APIKey
	if clientAPIKey == "" {
		clientAPIKey = "not-needed"
	}

	clientConfig := openai.DefaultConfig(clientAPIKey)
	if baseURL != "" {
		clientConfig.BaseURL = baseURL
	}

	capturing := &CapturingTransport{Base: http.DefaultTransport}
	chatAugment := &LlamaCppChatAugmentTransport{Next: capturing}
	httpClient := &http.Client{Transport: chatAugment}
	if cfg.TimeoutSeconds > 0 {
		httpClient.Timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
	}
	clientConfig.HTTPClient = httpClient

	traceEnabled := true
	if cfg.Trace != nil && !*cfg.Trace {
		traceEnabled = false
	}

	p := &LlamaCppProvider{
		name:             name,
		client:           openai.NewClientWithConfig(clientConfig),
		httpClient:       httpClient,
		maxTokens:        cfg.MaxTokens,
		contextTokens:    cfg.ContextTokens,
		apiKey:           cfg.APIKey,
		baseURL:          baseURL,
		serverRoot:       serverRoot,
		metadataProvider: "llamacpp",
		config:           cfg,
		traceEnabled:     traceEnabled,
		transport:        capturing,
		chatAugment:      chatAugment,
		dumpOnSuccess:    cfg.DumpOnSuccess,
		slotPersisted:    -1,
	}

	L_debug("llamacpp provider created",
		"name", name,
		"baseURL", baseURL,
		"serverRoot", serverRoot,
		"maxTokens", cfg.MaxTokens,
		"contextTokens", cfg.ContextTokens,
		"timeoutSeconds", cfg.TimeoutSeconds,
		"trace", traceEnabled,
	)

	return p, nil
}

func (p *LlamaCppProvider) trace(msg string, args ...any) {
	if p.traceEnabled {
		L_trace(msg, args...)
	}
}

func (p *LlamaCppProvider) Name() string {
	return p.name
}

func (p *LlamaCppProvider) Type() string {
	return "llamacpp"
}

func (p *LlamaCppProvider) MetadataProvider() string {
	return "llamacpp"
}

func (p *LlamaCppProvider) Model() string {
	return p.model
}

func (p *LlamaCppProvider) clone() *LlamaCppProvider {
	return &LlamaCppProvider{
		name:                   p.name,
		client:                 p.client,
		httpClient:             p.httpClient,
		model:                  p.model,
		maxTokens:              p.maxTokens,
		contextTokens:          p.contextTokens,
		apiKey:                 p.apiKey,
		baseURL:                p.baseURL,
		serverRoot:             p.serverRoot,
		metricPrefix:           p.metricPrefix,
		metadataProvider:       p.metadataProvider,
		config:                 p.config,
		embeddingOnly:          p.embeddingOnly,
		embeddingDimensions:    p.embeddingDimensions,
		traceEnabled:           p.traceEnabled,
		transport:              p.transport,
		chatAugment:            p.chatAugment,
		dumpOnSuccess:          p.dumpOnSuccess,
		available:              p.available,
		cachedPropsNCtx:        p.cachedPropsNCtx,
		cachedPropsTotalSlots:  p.cachedPropsTotalSlots,
		cachedPropsFetchedTime: p.cachedPropsFetchedTime,
		slotPersisted:          p.slotPersisted,
	}
}

func (p *LlamaCppProvider) WithModel(model string) Provider {
	clone := p.clone()
	clone.available = false
	clone.embeddingDimensions = 0
	clone.embeddingOnly = false
	clone.model = model
	clone.metricPrefix = fmt.Sprintf("llm/%s/%s/%s", p.Type(), p.Name(), model)
	return clone
}

func (p *LlamaCppProvider) WithMaxTokens(max int) Provider {
	clone := p.clone()
	clone.maxTokens = max
	return clone
}

func (p *LlamaCppProvider) WithEmbeddingModel(model string) Provider {
	clone := p.clone()
	clone.available = false
	clone.embeddingDimensions = 0
	clone.model = model
	clone.embeddingOnly = true
	clone.metricPrefix = fmt.Sprintf("llm/%s/%s/%s", p.Type(), p.Name(), model)
	clone.checkEmbeddingAvailability()
	return clone
}

func (p *LlamaCppProvider) healthProbe(ctx context.Context) bool {
	if p.serverRoot == "" || p.httpClient == nil {
		return false
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, llamaCppHealthProbeTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.serverRoot+"/health", nil)
	if err != nil {
		L_debug("llamacpp: failed to create health request", "error", err)
		return false
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		L_debug("llamacpp: health request failed", "provider", p.name, "error", err)
		return false
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return true
	case http.StatusServiceUnavailable:
		return false
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		L_debug("llamacpp: unexpected health response",
			"provider", p.name,
			"status", resp.StatusCode,
			"body", string(body),
		)
		return false
	}
}

func (p *LlamaCppProvider) IsAvailable() bool {
	if p == nil || p.client == nil || p.httpClient == nil || p.model == "" {
		return false
	}
	if !p.healthProbe(context.Background()) {
		return false
	}
	if p.embeddingOnly {
		p.mu.RLock()
		defer p.mu.RUnlock()
		return p.available
	}
	return true
}

// fetchCachedProps returns n_ctx and total_slots from GET /props with TTL caching.
// When the server omits total_slots, totalSlots defaults to 1.
func (p *LlamaCppProvider) fetchCachedProps(ctx context.Context) (nCtx, totalSlots int) {
	p.propsMu.RLock()
	if p.cachedPropsNCtx > 0 && time.Since(p.cachedPropsFetchedTime) < llamaCppPropsTTL {
		nCtx = p.cachedPropsNCtx
		totalSlots = p.cachedPropsTotalSlots
		p.propsMu.RUnlock()
		return nCtx, totalSlots
	}
	p.propsMu.RUnlock()

	if p.serverRoot == "" || p.httpClient == nil {
		return 0, 0
	}

	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, llamaCppHealthProbeTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.serverRoot+"/props", nil)
	if err != nil {
		L_debug("llamacpp: failed to create props request", "error", err)
		return 0, 0
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		L_debug("llamacpp: props request failed", "provider", p.name, "error", err)
		return 0, 0
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		L_debug("llamacpp: props returned non-200",
			"provider", p.name,
			"status", resp.StatusCode,
			"body", string(body),
		)
		return 0, 0
	}

	var payload llamaCppPropsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		L_debug("llamacpp: failed to decode props response", "provider", p.name, "error", err)
		return 0, 0
	}

	if payload.DefaultGenerationSettings.NCtx <= 0 {
		return 0, 0
	}

	slots := payload.TotalSlots
	if slots < 1 {
		slots = 1
	}

	p.propsMu.Lock()
	p.cachedPropsNCtx = payload.DefaultGenerationSettings.NCtx
	p.cachedPropsTotalSlots = slots
	p.cachedPropsFetchedTime = time.Now()
	p.propsMu.Unlock()

	L_debug("llamacpp: cached props",
		"provider", p.name,
		"contextTokens", payload.DefaultGenerationSettings.NCtx,
		"totalSlots", slots,
	)

	return payload.DefaultGenerationSettings.NCtx, slots
}

func (p *LlamaCppProvider) ContextTokens() int {
	if p.contextTokens > 0 {
		return p.contextTokens
	}
	if nctx, _ := p.fetchCachedProps(context.Background()); nctx > 0 {
		return nctx
	}
	return llamaCppDefaultContextTokens
}

func (p *LlamaCppProvider) MaxTokens() int {
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

func (p *LlamaCppProvider) checkEmbeddingAvailability() {
	ctx, cancel := context.WithTimeout(context.Background(), llamaCppEmbeddingProbeTimout)
	defer cancel()

	L_info("llamacpp: checking embedding availability",
		"name", p.name,
		"model", p.model,
		"baseURL", p.baseURL,
	)

	req := openai.EmbeddingRequest{
		Model: openai.EmbeddingModel(p.model),
		Input: []string{"test"},
	}

	resp, err := p.client.CreateEmbeddings(ctx, req)
	if err != nil {
		L_warn("llamacpp: embedding not available", "error", err, "name", p.name, "model", p.model)
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
		L_info("llamacpp: embedding ready", "name", p.name, "model", p.model, "dimensions", len(resp.Data[0].Embedding))
		return
	}

	L_warn("llamacpp: embedding returned empty data", "name", p.name, "model", p.model)
	p.mu.Lock()
	p.available = false
	p.mu.Unlock()
}

func (p *LlamaCppProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	if !p.embeddingOnly {
		return nil, ErrNotSupported{Provider: p.name, Operation: "embeddings (not configured as embedding provider)"}
	}

	req := openai.EmbeddingRequest{
		Model: openai.EmbeddingModel(p.model),
		Input: []string{text},
	}

	resp, err := p.client.CreateEmbeddings(ctx, req)
	if err != nil {
		L_error("llamacpp: embedding failed", "error", err, "model", p.model)
		return nil, fmt.Errorf("embedding failed: %w", err)
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("embedding returned no data")
	}

	embedding := make([]float32, len(resp.Data[0].Embedding))
	for i, v := range resp.Data[0].Embedding {
		embedding[i] = float32(v)
	}

	p.mu.Lock()
	p.available = true
	if p.embeddingDimensions == 0 && len(embedding) > 0 {
		p.embeddingDimensions = len(embedding)
	}
	p.mu.Unlock()
	if len(embedding) > 0 {
		L_debug("llamacpp: cached embedding dimensions", "dimensions", len(embedding), "model", p.model)
	}

	return embedding, nil
}

func (p *LlamaCppProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
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
		L_error("llamacpp: batch embedding failed", "error", err, "model", p.model, "count", len(texts))
		return nil, fmt.Errorf("batch embedding failed: %w", err)
	}

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

	p.mu.Lock()
	p.available = true
	if p.embeddingDimensions == 0 && len(result) > 0 && len(result[0]) > 0 {
		p.embeddingDimensions = len(result[0])
	}
	p.mu.Unlock()
	if len(result) > 0 && len(result[0]) > 0 {
		L_debug("llamacpp: cached embedding dimensions from batch", "dimensions", len(result[0]), "model", p.model)
	}

	return result, nil
}

func (p *LlamaCppProvider) EmbeddingDimensions() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.embeddingDimensions
}

func (p *LlamaCppProvider) SupportsEmbeddings() bool {
	return p.embeddingOnly
}

func (p *LlamaCppProvider) SimpleMessage(ctx context.Context, userMessage, systemPrompt string) (string, error) {
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

func (p *LlamaCppProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	modelsURL := strings.TrimSuffix(p.baseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if p.apiKey != "" && p.apiKey != "not-needed" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
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
			ID               string `json:"id"`
			ContextLength    *int   `json:"context_length"`
			MaxContextLength *int   `json:"max_context_length"`
			ContextWindow    *int   `json:"context_window"`
			NCtx             *int   `json:"n_ctx"`
			MaxModelLen      *int   `json:"max_model_len"`
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
		info := ModelInfo{ID: m.ID, DisplayName: m.ID}
		switch {
		case m.ContextLength != nil && *m.ContextLength > 0:
			info.ContextTokens = *m.ContextLength
		case m.MaxContextLength != nil && *m.MaxContextLength > 0:
			info.ContextTokens = *m.MaxContextLength
		case m.ContextWindow != nil && *m.ContextWindow > 0:
			info.ContextTokens = *m.ContextWindow
		case m.NCtx != nil && *m.NCtx > 0:
			info.ContextTokens = *m.NCtx
		case m.MaxModelLen != nil && *m.MaxModelLen > 0:
			info.ContextTokens = *m.MaxModelLen
		}
		models = append(models, info)
	}

	return models, nil
}

func (p *LlamaCppProvider) TestConnection(ctx context.Context) error {
	_, err := p.ListModels(ctx)
	if err != nil {
		return fmt.Errorf("connection test failed: %w", err)
	}
	return nil
}

// LoadSessionState implements StatefulProvider.
func (p *LlamaCppProvider) LoadSessionState(state map[string]any) {
	p.slotMu.Lock()
	defer p.slotMu.Unlock()
	p.slotPersisted = -1
	if state == nil {
		return
	}
	if v, ok := state["slot_id"]; ok {
		switch n := v.(type) {
		case float64:
			p.slotPersisted = int(n)
		case int:
			p.slotPersisted = n
		case int64:
			p.slotPersisted = int(n)
		default:
			p.slotPersisted = -1
		}
	}
	L_trace("llamacpp: loaded session state", "slotPersisted", p.slotPersisted)
}

// SaveSessionState implements StatefulProvider.
func (p *LlamaCppProvider) SaveSessionState() map[string]any {
	p.slotMu.Lock()
	defer p.slotMu.Unlock()
	if p.slotPersisted < 0 {
		return nil
	}
	return map[string]any{"slot_id": p.slotPersisted}
}

var _ StatefulProvider = (*LlamaCppProvider)(nil)
