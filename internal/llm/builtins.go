package llm

// EnsureBuiltInEmbeddingProvider restores the permanent built-in Hugot
// embeddings provider and seeds the default embeddings chain only when empty.
func EnsureBuiltInEmbeddingProvider(cfg *LLMConfig) {
	if cfg == nil {
		return
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]LLMProviderConfig{}
	}

	cfg.Providers[BuiltInHugotProviderAlias] = builtInHugotProviderConfig(cfg.Providers[BuiltInHugotProviderAlias])

	if len(cfg.Embeddings.Models) == 0 {
		cfg.Embeddings.Models = []string{BuiltInHugotProviderAlias + "/" + DefaultHugotEmbeddingModel}
	}
}

func builtInHugotProviderConfig(existing LLMProviderConfig) LLMProviderConfig {
	existing.Driver = "hugot"
	existing.EmbeddingOnly = true
	if existing.Subtype == "" {
		existing.Subtype = "hugot"
	}
	return existing
}
