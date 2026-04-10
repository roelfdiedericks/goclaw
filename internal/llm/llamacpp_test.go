package llm

import "testing"

func TestLlamaCppDriverRegistered(t *testing.T) {
	desc, ok := GetDriver("llamacpp")
	if !ok {
		t.Fatalf("expected llamacpp driver to be registered")
	}
	if !desc.IsLocal {
		t.Fatalf("expected llamacpp driver to be local")
	}
	if !desc.SupportsEmbeddings {
		t.Fatalf("expected llamacpp driver to support embeddings")
	}
}

func TestLlamaCppProviderUsesDedicatedType(t *testing.T) {
	p, err := NewLlamaCppProvider("local", LLMProviderConfig{})
	if err != nil {
		t.Fatalf("NewLlamaCppProvider returned error: %v", err)
	}
	if p.Type() != "llamacpp" {
		t.Fatalf("expected provider type llamacpp, got %q", p.Type())
	}
	if p.MetadataProvider() != "llamacpp" {
		t.Fatalf("expected metadata provider llamacpp, got %q", p.MetadataProvider())
	}

	clone := p.WithModel("ggml-org/gemma-4-E2B-it-GGUF:Q8_0")
	if clone.Type() != "llamacpp" {
		t.Fatalf("expected clone type llamacpp, got %q", clone.Type())
	}

	llamaClone, ok := clone.(*LlamaCppProvider)
	if !ok {
		t.Fatalf("expected clone type *LlamaCppProvider, got %T", clone)
	}
	if llamaClone.Model() != "ggml-org/gemma-4-E2B-it-GGUF:Q8_0" {
		t.Fatalf("unexpected model %q", llamaClone.Model())
	}
}
