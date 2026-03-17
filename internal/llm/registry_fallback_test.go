package llm

import (
	"context"
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/types"
)

type mockProvider struct {
	name  string
	typ   string
	model string
}

func (m *mockProvider) Name() string                        { return m.name }
func (m *mockProvider) Type() string                        { return m.typ }
func (m *mockProvider) Model() string                       { return m.model }
func (m *mockProvider) MetadataProvider() string            { return "" }
func (m *mockProvider) WithModel(model string) Provider     { cp := *m; cp.model = model; return &cp }
func (m *mockProvider) WithMaxTokens(_ int) Provider        { cp := *m; return &cp }
func (m *mockProvider) IsAvailable() bool                   { return true }
func (m *mockProvider) ContextTokens() int                  { return 128000 }
func (m *mockProvider) MaxTokens() int                      { return 8192 }
func (m *mockProvider) SimpleMessage(context.Context, string, string) (string, error) {
	return "ok", nil
}
func (m *mockProvider) StreamMessage(context.Context, []types.Message, []types.ToolDefinition, string, func(string), *StreamOptions) (*Response, error) {
	return &Response{Text: "ok"}, nil
}
func (m *mockProvider) Embed(context.Context, string) ([]float32, error)         { return []float32{0.1}, nil }
func (m *mockProvider) EmbedBatch(context.Context, []string) ([][]float32, error) { return [][]float32{{0.1}}, nil }
func (m *mockProvider) EmbeddingDimensions() int                                  { return 1 }
func (m *mockProvider) SupportsEmbeddings() bool                                  { return true }

func TestAllowsAgentFallbackPurposeRules(t *testing.T) {
	if allowsAgentFallback("agent") {
		t.Fatalf("agent should not fallback to agent chain")
	}
	if allowsAgentFallback("embeddings") {
		t.Fatalf("embeddings should not fallback to agent chain")
	}
	for _, purpose := range []string{"summarization", "heartbeat", "cron", "hass", "memory_extraction"} {
		if !allowsAgentFallback(purpose) {
			t.Fatalf("%s should fallback to agent chain", purpose)
		}
	}
}

func TestGetProviderUsesAgentFallbackForSummarization(t *testing.T) {
	r := &Registry{
		providers: map[string]providerInstance{
			"p1": {provider: &mockProvider{name: "p1", typ: "mock", model: "base"}},
		},
		purposes: map[string]LLMPurposeConfig{
			"agent":         {Models: []string{"p1/agent-model"}},
			"summarization": {Models: nil},
			"embeddings":    {Models: nil},
		},
	}

	p, err := r.GetProvider("summarization")
	if err != nil {
		t.Fatalf("expected summarization provider via agent fallback, got error: %v", err)
	}
	if p.Model() != "agent-model" {
		t.Fatalf("expected fallback model agent-model, got %q", p.Model())
	}
}

func TestGetProviderDoesNotFallbackForEmbeddings(t *testing.T) {
	r := &Registry{
		providers: map[string]providerInstance{
			"p1": {provider: &mockProvider{name: "p1", typ: "mock", model: "base"}},
		},
		purposes: map[string]LLMPurposeConfig{
			"agent":      {Models: []string{"p1/agent-model"}},
			"embeddings": {Models: nil},
		},
	}

	_, err := r.GetProvider("embeddings")
	if err == nil {
		t.Fatalf("expected error when embeddings chain is empty")
	}
}
