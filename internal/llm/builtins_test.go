package llm

import "testing"

func TestEnsureBuiltInEmbeddingProviderSeedsDefaultChain(t *testing.T) {
	cfg := &LLMConfig{}

	EnsureBuiltInEmbeddingProvider(cfg)

	prov, ok := cfg.Providers[BuiltInHugotProviderAlias]
	if !ok {
		t.Fatalf("expected built-in provider %q to be seeded", BuiltInHugotProviderAlias)
	}
	if prov.Driver != "hugot" {
		t.Fatalf("expected built-in provider driver hugot, got %q", prov.Driver)
	}
	if !prov.EmbeddingOnly {
		t.Fatalf("expected built-in provider to be embedding-only")
	}

	if len(cfg.Embeddings.Models) != 1 {
		t.Fatalf("expected exactly one default embeddings model, got %d", len(cfg.Embeddings.Models))
	}
	want := BuiltInHugotProviderAlias + "/" + DefaultHugotEmbeddingModel
	if cfg.Embeddings.Models[0] != want {
		t.Fatalf("expected default embeddings model %q, got %q", want, cfg.Embeddings.Models[0])
	}
}

func TestEnsureBuiltInEmbeddingProviderPreservesExistingChain(t *testing.T) {
	cfg := &LLMConfig{
		Providers: map[string]LLMProviderConfig{
			"anthropic": {Driver: "anthropic"},
		},
		Embeddings: LLMPurposeConfig{
			Models: []string{"custom/local-model"},
		},
	}

	EnsureBuiltInEmbeddingProvider(cfg)

	if _, ok := cfg.Providers[BuiltInHugotProviderAlias]; !ok {
		t.Fatalf("expected built-in provider %q to be restored", BuiltInHugotProviderAlias)
	}
	if len(cfg.Embeddings.Models) != 1 || cfg.Embeddings.Models[0] != "custom/local-model" {
		t.Fatalf("expected existing embeddings chain to be preserved, got %#v", cfg.Embeddings.Models)
	}
}
