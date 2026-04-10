package llm

import "testing"

func TestResolveLlamaCppProviderConfigManagedDefaults(t *testing.T) {
	cfg := LLMProviderConfig{
		Driver: "llamacpp",
		LlamaCpp: &LlamaCppProviderConfig{
			Mode:           LlamaCppModeManaged,
			ManagedModelID: "gemma4-e2b",
		},
	}

	got := resolveLlamaCppProviderConfig(cfg)
	if got.BaseURL != "http://127.0.0.1:8080" {
		t.Fatalf("expected managed baseURL default, got %q", got.BaseURL)
	}
}

func TestResolveLlamaCppModelNameManagedSentinel(t *testing.T) {
	cfg := LLMProviderConfig{
		Driver: "llamacpp",
		LlamaCpp: &LlamaCppProviderConfig{
			Mode:           LlamaCppModeManaged,
			ManagedModelID: "gemma4-e2b",
		},
	}

	got := resolveLlamaCppModelName(cfg, "managed")
	if got != "ggml-org/gemma-4-E2B-it-GGUF:Q8_0" {
		t.Fatalf("unexpected resolved model %q", got)
	}
}

func TestRegistryResolveUsesManagedLlamaCppModel(t *testing.T) {
	r, err := NewRegistry(RegistryConfig{
		Providers: map[string]LLMProviderConfig{
			"local": {
				Driver: "llamacpp",
				LlamaCpp: &LlamaCppProviderConfig{
					Mode:           LlamaCppModeManaged,
					ManagedModelID: "gemma4-e2b",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}

	resolved, err := r.Resolve("local/managed")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}

	provider, ok := resolved.(*LlamaCppProvider)
	if !ok {
		t.Fatalf("expected *LlamaCppProvider, got %T", resolved)
	}
	if provider.Model() != "ggml-org/gemma-4-E2B-it-GGUF:Q8_0" {
		t.Fatalf("unexpected managed model %q", provider.Model())
	}
}
