package memory

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
)

// OllamaProvider generates embeddings using Ollama's API
type OllamaProvider struct {
	url        string
	model      string
	client     *http.Client
	dimensions int
	available  bool
	mu         sync.RWMutex
	onReady    func() // Callback when provider becomes available
}

// ollamaEmbedRequest is the request body for Ollama embeddings
type ollamaEmbedRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

// ollamaEmbedResponse is the response from Ollama embeddings
type ollamaEmbedResponse struct {
	Embedding []float64 `json:"embedding"`
}

// NewOllamaProvider creates a new Ollama embedding provider
func NewOllamaProvider(url, model string) *OllamaProvider {
	// Normalize URL
	url = strings.TrimSuffix(url, "/")

	p := &OllamaProvider{
		url:   url,
		model: model,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
		available: false,
	}

	L_info("memory: ollama provider created", "url", url, "model", model)

	// Test availability in background
	go p.checkAvailability()

	return p
}

// checkAvailability tests if Ollama is reachable and the model is available
func (p *OllamaProvider) checkAvailability() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	L_debug("memory: checking ollama availability", "url", p.url, "model", p.model)

	// Try to generate a test embedding
	embedding, err := p.embedSingle(ctx, "test")
	if err != nil {
		L_warn("memory: ollama not available", "error", err, "url", p.url)
		p.mu.Lock()
		p.available = false
		p.mu.Unlock()
		return
	}

	p.mu.Lock()
	p.dimensions = len(embedding)
	p.available = true
	p.mu.Unlock()

	L_info("memory: ollama provider ready", "url", p.url, "model", p.model, "dimensions", len(embedding))

	// Notify listener if set (used to trigger re-index after provider becomes available)
	p.mu.RLock()
	cb := p.onReady
	p.mu.RUnlock()
	if cb != nil {
		cb()
	}
}

// OnReady sets a callback to be invoked when the provider becomes available
func (p *OllamaProvider) OnReady(cb func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onReady = cb
	// If already available, call immediately
	if p.available && cb != nil {
		go cb()
	}
}

func (p *OllamaProvider) ID() string {
	return "ollama"
}

func (p *OllamaProvider) Model() string {
	return p.model
}

func (p *OllamaProvider) Dimensions() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.dimensions
}

func (p *OllamaProvider) Available() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.available
}

// EmbedQuery generates an embedding for a search query
func (p *OllamaProvider) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	if !p.Available() {
		L_debug("memory: ollama not available for query embedding")
		return nil, nil
	}

	L_trace("memory: embedding query", "textLength", len(text))

	embedding, err := p.embedSingle(ctx, text)
	if err != nil {
		L_error("memory: failed to embed query", "error", err)
		return nil, err
	}

	L_trace("memory: query embedded", "dimensions", len(embedding))
	return embedding, nil
}

// EmbedBatch generates embeddings for multiple texts
func (p *OllamaProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if !p.Available() {
		L_debug("memory: ollama not available for batch embedding", "count", len(texts))
		return make([][]float32, len(texts)), nil
	}

	L_debug("memory: embedding batch", "count", len(texts))

	embeddings := make([][]float32, len(texts))
	for i, text := range texts {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		embedding, err := p.embedSingle(ctx, text)
		if err != nil {
			L_warn("memory: failed to embed text in batch", "index", i, "error", err)
			// Continue with nil embedding for this text
			continue
		}
		embeddings[i] = embedding
	}

	L_debug("memory: batch embedded", "count", len(texts), "successful", countNonNil(embeddings))
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

	L_trace("memory: sending ollama request", "url", url, "model", p.model)

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

	return embedding, nil
}

// countNonNil counts non-nil embeddings
func countNonNil(embeddings [][]float32) int {
	count := 0
	for _, e := range embeddings {
		if e != nil {
			count++
		}
	}
	return count
}

// Ensure OllamaProvider implements EmbeddingProvider
var _ EmbeddingProvider = (*OllamaProvider)(nil)
