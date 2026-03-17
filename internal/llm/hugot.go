// Package llm provides LLM client implementations.
package llm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/pipelines"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/paths"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

func init() {
	RegisterDriver(DriverDescriptor{
		ID:                 "hugot",
		Label:              "Hugot (Local Embeddings)",
		Order:              50,
		IsLocal:            true,
		SupportsEmbeddings: true,
		New: func(name string, cfg LLMProviderConfig) (Provider, error) {
			return NewHugotProvider(name, cfg)
		},
	})
}

const (
	// BuiltInHugotProviderAlias is the permanent built-in local embeddings provider alias.
	BuiltInHugotProviderAlias = "hugot-local"

	// DefaultHugotEmbeddingModel is the curated default local embeddings model.
	DefaultHugotEmbeddingModel = "KnightsAnalytics/all-MiniLM-L6-v2"

	hugotDefaultEmbeddingModel = "KnightsAnalytics/all-MiniLM-L6-v2"
	hugotDefaultOnnxFilename   = "model.onnx"
)

// HugotProvider implements the Provider interface for local embeddings via Hugot.
// It is intentionally embedding-only for MVP.
type HugotProvider struct {
	name string

	model         string
	maxTokens     int
	contextTokens int

	config LLMProviderConfig

	mu                  sync.RWMutex
	session             *hugot.Session
	pipeline            *pipelines.FeatureExtractionPipeline
	embeddingDimensions int
	available           bool
	initErr             error
}

// NewHugotProvider creates a new Hugot embeddings provider.
func NewHugotProvider(name string, cfg LLMProviderConfig) (*HugotProvider, error) {
	p := &HugotProvider{
		name:          name,
		maxTokens:     cfg.MaxTokens,
		contextTokens: cfg.ContextTokens,
		config:        cfg,
	}

	L_debug("hugot provider created", "name", name)
	return p, nil
}

func (p *HugotProvider) Name() string {
	return p.name
}

func (p *HugotProvider) Type() string {
	return "hugot"
}

func (p *HugotProvider) MetadataProvider() string {
	return ""
}

func (p *HugotProvider) Model() string {
	return p.model
}

func (p *HugotProvider) WithModel(model string) Provider {
	clone := *p               //nolint:govet // copylocks: mu is reset immediately below
	clone.mu = sync.RWMutex{} // Fresh mutex - copying a used mutex is undefined behavior
	clone.model = model
	clone.session = nil
	clone.pipeline = nil
	clone.embeddingDimensions = 0
	clone.available = false
	clone.initErr = nil
	return &clone
}

func (p *HugotProvider) WithMaxTokens(max int) Provider {
	clone := *p               //nolint:govet // copylocks: mu is reset immediately below
	clone.mu = sync.RWMutex{} // Fresh mutex - copying a used mutex is undefined behavior
	clone.maxTokens = max
	return &clone
}

func (p *HugotProvider) IsAvailable() bool {
	if p == nil || p.model == "" {
		return false
	}
	return p.ensureInitialized(context.Background()) == nil
}

func (p *HugotProvider) ContextTokens() int {
	if p.contextTokens > 0 {
		return p.contextTokens
	}
	return DefaultContextTokens
}

func (p *HugotProvider) MaxTokens() int {
	if p.maxTokens > 0 {
		return p.maxTokens
	}
	return DefaultMaxOutputTokens
}

func (p *HugotProvider) SimpleMessage(_ context.Context, _, _ string) (string, error) {
	return "", ErrNotSupported{Provider: p.name, Operation: "SimpleMessage"}
}

func (p *HugotProvider) StreamMessage(
	_ context.Context,
	_ []types.Message,
	_ []types.ToolDefinition,
	_ string,
	_ func(delta string),
	_ *StreamOptions,
) (*Response, error) {
	return nil, ErrNotSupported{Provider: p.name, Operation: "StreamMessage"}
}

func (p *HugotProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	if err := p.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	result, err := p.pipeline.RunPipeline([]string{text})
	if err != nil {
		return nil, fmt.Errorf("hugot embed failed: %w", err)
	}
	if len(result.Embeddings) == 0 {
		return nil, fmt.Errorf("hugot embed returned no data")
	}
	p.cacheEmbeddingDimensions(result.Embeddings[0])
	return result.Embeddings[0], nil
}

func (p *HugotProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	if err := p.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	result, err := p.pipeline.RunPipeline(texts)
	if err != nil {
		return nil, fmt.Errorf("hugot batch embed failed: %w", err)
	}
	if len(result.Embeddings) > 0 {
		p.cacheEmbeddingDimensions(result.Embeddings[0])
	}
	return result.Embeddings, nil
}

func (p *HugotProvider) EmbeddingDimensions() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.embeddingDimensions
}

func (p *HugotProvider) SupportsEmbeddings() bool {
	return true
}

// ListModels returns the curated list of Hugot-supported embedding models.
func (p *HugotProvider) ListModels(_ context.Context) ([]ModelInfo, error) {
	modelRoot, err := paths.DataPath("hugot/models")
	if err != nil {
		return nil, fmt.Errorf("resolve hugot models path: %w", err)
	}

	displayName := "all-MiniLM-L6-v2 (recommended)"
	if hasCachedHugotModel(modelRoot, hugotDefaultEmbeddingModel) {
		displayName = "all-MiniLM-L6-v2 (recommended, cached)"
	}

	return []ModelInfo{
		{
			ID:            hugotDefaultEmbeddingModel,
			DisplayName:   displayName,
			ContextTokens: 0,
		},
	}, nil
}

// TestConnection verifies that the local Hugot runtime can initialize and that
// the GoClaw Hugot cache root can be created.
func (p *HugotProvider) TestConnection(_ context.Context) error {
	modelRoot, err := paths.DataPath("hugot/models")
	if err != nil {
		return fmt.Errorf("resolve hugot models path: %w", err)
	}
	if err := paths.EnsureDir(modelRoot); err != nil {
		return fmt.Errorf("create hugot models path: %w", err)
	}
	session, err := hugot.NewGoSession()
	if err != nil {
		return fmt.Errorf("initialize hugot go session: %w", err)
	}
	if err := session.Destroy(); err != nil {
		return fmt.Errorf("destroy hugot go session: %w", err)
	}
	return nil
}

func (p *HugotProvider) ensureInitialized(_ context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.available && p.pipeline != nil && p.session != nil {
		return nil
	}
	if p.initErr != nil {
		return p.initErr
	}
	if p.model == "" {
		p.initErr = ErrUnavailable{Provider: p.name, Reason: "no model configured"}
		return p.initErr
	}

	modelRoot, err := paths.DataPath("hugot/models")
	if err != nil {
		p.initErr = fmt.Errorf("resolve hugot models path: %w", err)
		return p.initErr
	}
	if err := paths.EnsureDir(modelRoot); err != nil {
		p.initErr = fmt.Errorf("ensure hugot models dir: %w", err)
		return p.initErr
	}

	modelPath, err := ensureHugotModelDownloaded(modelRoot, p.model)
	if err != nil {
		p.initErr = err
		return p.initErr
	}

	session, err := hugot.NewGoSession()
	if err != nil {
		p.initErr = fmt.Errorf("create hugot session: %w", err)
		return p.initErr
	}

	pipeline, err := hugot.NewPipeline(session, hugot.FeatureExtractionConfig{
		ModelPath:    modelPath,
		OnnxFilename: hugotDefaultOnnxFilename,
		Name:         "goclaw-embeddings",
		Options: []hugot.FeatureExtractionOption{
			pipelines.WithNormalization(),
		},
	})
	if err != nil {
		_ = session.Destroy()
		p.initErr = fmt.Errorf("create hugot feature extraction pipeline: %w", err)
		return p.initErr
	}

	p.session = session
	p.pipeline = pipeline
	p.available = true
	L_info("hugot: embedding provider ready", "name", p.name, "model", p.model, "path", modelPath)
	return nil
}

func (p *HugotProvider) cacheEmbeddingDimensions(embedding []float32) {
	if len(embedding) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.embeddingDimensions == 0 {
		p.embeddingDimensions = len(embedding)
		L_debug("hugot: cached embedding dimensions", "model", p.model, "dimensions", p.embeddingDimensions)
	}
}

func ensureHugotModelDownloaded(modelRoot, modelID string) (string, error) {
	modelPath := filepath.Join(modelRoot, sanitizeHugotModelID(modelID))
	onnxPath := filepath.Join(modelPath, hugotDefaultOnnxFilename)
	if _, err := os.Stat(onnxPath); err == nil {
		return modelPath, nil
	}

	L_info("hugot: downloading embedding model", "model", modelID, "target", modelRoot)
	path, err := hugot.DownloadModel(modelID, modelRoot, hugot.NewDownloadOptions())
	if err != nil {
		return "", fmt.Errorf("download hugot model %s: %w", modelID, err)
	}
	return path, nil
}

func hasCachedHugotModel(modelRoot, modelID string) bool {
	onnxPath := filepath.Join(modelRoot, sanitizeHugotModelID(modelID), hugotDefaultOnnxFilename)
	_, err := os.Stat(onnxPath)
	return err == nil
}

func sanitizeHugotModelID(modelID string) string {
	return strings.ReplaceAll(modelID, "/", "_")
}

var (
	_ Provider         = (*HugotProvider)(nil)
	_ ModelLister      = (*HugotProvider)(nil)
	_ ConnectionTester = (*HugotProvider)(nil)
	_ LLMEmbedder      = (*HugotProvider)(nil)
)
