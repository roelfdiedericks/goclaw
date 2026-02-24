package setup

import (
	"github.com/roelfdiedericks/goclaw/internal/metadata"
)

// ProviderPreset defines a predefined LLM provider option
type ProviderPreset struct {
	Name               string
	Key                string
	Type               string // Driver: "anthropic", "openai", "ollama", "xai"
	BaseURL            string
	Description        string
	IsLocal            bool
	SupportsEmbeddings bool
	KnownEmbedModels   []string
	KnownChatModels    []string
}

// BuildPresets returns provider presets built from models.json metadata.
func BuildPresets() []ProviderPreset {
	meta := metadata.Get()
	providerIDs := meta.ModelProviderIDs()

	presets := make([]ProviderPreset, 0, len(providerIDs))
	for _, pid := range providerIDs {
		prov, ok := meta.GetModelProvider(pid)
		if !ok {
			continue
		}

		preset := ProviderPreset{
			Name:            prov.Name,
			Key:             pid,
			Type:            prov.Driver,
			BaseURL:         prov.APIEndpoint,
			KnownChatModels: meta.GetKnownChatModels(pid),
		}

		if prov.Driver == "ollama" || pid == "lmstudio" {
			preset.IsLocal = true
		}

		presets = append(presets, preset)
	}

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
		Type:               "openai",
		BaseURL:            baseURL,
		Description:        "Custom endpoint",
		SupportsEmbeddings: true,
		KnownEmbedModels:   []string{},
		KnownChatModels:    []string{},
	}
}
