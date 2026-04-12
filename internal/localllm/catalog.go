package localllm

import "fmt"

type ModelFamily string

const (
	ModelFamilyGemma4 ModelFamily = "gemma4"
)

type ManagedModelSpec struct {
	ID                     string
	Family                 ModelFamily
	Label                  string
	HFRepo                 string
	PreferredQuant         string
	PreferredFilename      string
	MMProjFilename         string
	ApproxDownloadBytes    uint64
	RecommendedMinRAMBytes uint64
	// FallbackContextTokens is used when GGUF metadata cannot be read or has no context_length.
	FallbackContextTokens int
}

var managedModelCatalog = []ManagedModelSpec{
	{
		ID:                     "gemma4-e2b",
		Family:                 ModelFamilyGemma4,
		Label:                  "Gemma 4 E2B",
		HFRepo:                 "ggml-org/gemma-4-E2B-it-GGUF",
		PreferredQuant:         "Q8_0",
		PreferredFilename:      "gemma-4-e2b-it-Q8_0.gguf",
		MMProjFilename:         "mmproj-gemma-4-e2b-it-f16.gguf",
		ApproxDownloadBytes:    4970000000,
		RecommendedMinRAMBytes: 8 * 1024 * 1024 * 1024,
		FallbackContextTokens:  128 * 1024,
	},
	{
		ID:                     "gemma4-e4b",
		Family:                 ModelFamilyGemma4,
		Label:                  "Gemma 4 E4B",
		HFRepo:                 "ggml-org/gemma-4-E4B-it-GGUF",
		PreferredQuant:         "Q4_K_M",
		PreferredFilename:      "gemma-4-e4b-it-Q4_K_M.gguf",
		MMProjFilename:         "mmproj-gemma-4-e4b-it-f16.gguf",
		ApproxDownloadBytes:    5340000000,
		RecommendedMinRAMBytes: 12 * 1024 * 1024 * 1024,
		FallbackContextTokens:  128 * 1024,
	},
	{
		ID:                     "gemma4-26b-a4b",
		Family:                 ModelFamilyGemma4,
		Label:                  "Gemma 4 26B A4B",
		HFRepo:                 "ggml-org/gemma-4-26B-A4B-it-GGUF",
		PreferredQuant:         "Q4_K_M",
		PreferredFilename:      "gemma-4-26B-A4B-it-Q4_K_M.gguf",
		MMProjFilename:         "mmproj-gemma-4-26B-A4B-it-f16.gguf",
		ApproxDownloadBytes:    16800000000,
		RecommendedMinRAMBytes: 24 * 1024 * 1024 * 1024,
		FallbackContextTokens:  256 * 1024,
	},
	{
		ID:                     "gemma4-31b",
		Family:                 ModelFamilyGemma4,
		Label:                  "Gemma 4 31B",
		HFRepo:                 "ggml-org/gemma-4-31B-it-GGUF",
		PreferredQuant:         "Q4_K_M",
		PreferredFilename:      "gemma-4-31B-it-Q4_K_M.gguf",
		MMProjFilename:         "mmproj-gemma-4-31B-it-f16.gguf",
		ApproxDownloadBytes:    18700000000,
		RecommendedMinRAMBytes: 32 * 1024 * 1024 * 1024,
		FallbackContextTokens:  256 * 1024,
	},
}

func ManagedModelCatalog() []ManagedModelSpec {
	out := make([]ManagedModelSpec, len(managedModelCatalog))
	copy(out, managedModelCatalog)
	return out
}

func ManagedModelByID(id string) (ManagedModelSpec, error) {
	for _, spec := range managedModelCatalog {
		if spec.ID == id {
			return spec, nil
		}
	}
	return ManagedModelSpec{}, fmt.Errorf("unknown managed model %q", id)
}

func (m ManagedModelSpec) APIModelName() string {
	if m.HFRepo == "" {
		return ""
	}
	if m.PreferredQuant == "" {
		return m.HFRepo
	}
	return m.HFRepo + ":" + m.PreferredQuant
}
