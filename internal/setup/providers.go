package setup

import (
	"github.com/roelfdiedericks/goclaw/internal/llm"
	"github.com/roelfdiedericks/goclaw/internal/localllm"
	"github.com/roelfdiedericks/goclaw/internal/metadata"
)

// ProviderPreset defines a predefined LLM provider option
type ProviderPreset struct {
	Name               string
	Key                string
	Driver             string // "anthropic", "openai", "ollama", "xai"
	BaseURL            string
	Description        string
	IsLocal            bool
	Synthetic          bool
	SupportsEmbeddings bool
	KnownEmbedModels   []string
	KnownChatModels    []string
	DefaultModel       string
	LlamaCpp           *llm.LlamaCppProviderConfig
}

// BuildPresets returns provider presets built from models.json metadata.
func BuildPresets() []ProviderPreset {
	meta := metadata.Get()
	providerIDs := meta.ModelProviderIDs()

	presets := make([]ProviderPreset, 0, len(providerIDs)+1)
	for _, pid := range providerIDs {
		prov, ok := meta.GetModelProvider(pid)
		if !ok {
			continue
		}

		preset := ProviderPreset{
			Name:            prov.Name,
			Key:             pid,
			Driver:          prov.Driver,
			BaseURL:         prov.APIEndpoint,
			KnownChatModels: meta.GetKnownChatModels(pid),
			DefaultModel:    firstDefaultModel(meta, pid),
		}

		if llm.DriverOrEndpointIsLocal(preset.Driver, prov.APIEndpoint) {
			preset.IsLocal = true
		}

		presets = append(presets, preset)
	}

	presets = append(presets, LlamaCppManagedPreset())
	return presets
}

// GetPreset returns a preset by key, or nil if not found
func GetPreset(key string) *ProviderPreset {
	presets := BuildPresets()
	for i := range presets {
		if presets[i].Key == key {
			return &presets[i]
		}
	}
	return nil
}

// EmbeddingCapablePresets returns only presets that support embeddings
func EmbeddingCapablePresets() []ProviderPreset {
	var result []ProviderPreset
	for _, p := range BuildPresets() {
		if p.SupportsEmbeddings {
			result = append(result, p)
		}
	}
	return result
}

// CustomPreset returns a preset for "Other OpenAI-compatible" providers
func CustomPreset(name, baseURL string) ProviderPreset {
	return ProviderPreset{
		Name:               name,
		Key:                name,
		Driver:             "openai",
		BaseURL:            baseURL,
		Description:        "Custom endpoint",
		SupportsEmbeddings: true,
		KnownEmbedModels:   []string{},
		KnownChatModels:    []string{},
	}
}

func LlamaCppManagedPreset() ProviderPreset {
	catalog := localllm.ManagedModelCatalog()
	chatModels := make([]string, 0, len(catalog))
	defaultModel := ""
	defaultModelID := ""
	for i, spec := range catalog {
		modelName := spec.APIModelName()
		if modelName == "" {
			continue
		}
		chatModels = append(chatModels, modelName)
		if i == 0 {
			defaultModel = modelName
			defaultModelID = spec.ID
		}
	}

	return ProviderPreset{
		Name:               "Gemma Local (recommended)",
		Key:                "llamacpp-managed",
		Driver:             "llamacpp",
		BaseURL:            "http://127.0.0.1:8080",
		Description:        "GoClaw-managed llama.cpp runtime with curated local Gemma models.",
		IsLocal:            true,
		Synthetic:          true,
		SupportsEmbeddings: true,
		KnownChatModels:    chatModels,
		DefaultModel:       defaultModel,
		LlamaCpp: &llm.LlamaCppProviderConfig{
			Mode:           llm.LlamaCppModeManaged,
			Host:           "127.0.0.1",
			Port:           8080,
			ManagedModelID: defaultModelID,
		},
	}
}

func firstDefaultModel(meta *metadata.Manager, providerID string) string {
	defaultLarge, _ := meta.GetDefaultModels(providerID)
	return defaultLarge
}
