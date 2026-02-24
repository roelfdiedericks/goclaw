package metadata

// ModelsData is the root structure of models.json.
type ModelsData struct {
	Generated       string                    `json:"generated"`
	CatwalkCommit   string                    `json:"catwalk_commit,omitempty"`
	ModelsDevBranch string                    `json:"models_dev_branch,omitempty"`
	Providers       map[string]*ModelProvider `json:"providers"`
}

// ModelProvider contains metadata about a provider from models.json.
type ModelProvider struct {
	Name              string            `json:"name"`
	APIEndpoint       string            `json:"api_endpoint"`
	APIKeyEnv         string            `json:"api_key_env"`
	CatwalkType       string            `json:"catwalk_type"`
	Driver            string            `json:"driver"`
	DefaultLargeModel string            `json:"default_large_model"`
	DefaultSmallModel string            `json:"default_small_model"`
	Models            map[string]*Model `json:"models"`
}

// Model contains metadata about a single LLM model.
type Model struct {
	Name            string       `json:"name"`
	ContextWindow   int64        `json:"context_window"`
	MaxOutputTokens int64        `json:"max_output_tokens"`
	Cost            ModelCost    `json:"cost"`
	Capabilities    Capabilities `json:"capabilities"`
	Modalities      Modalities   `json:"modalities"`
	Metadata        ModelMeta    `json:"metadata"`
}

// ModelCost contains pricing per 1M tokens (USD).
type ModelCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
	Reasoning  float64 `json:"reasoning"`
}

// Capabilities describes what a model supports.
type Capabilities struct {
	Vision                 bool     `json:"vision"`
	ToolUse                bool     `json:"tool_use"`
	Streaming              bool     `json:"streaming"`
	Reasoning              bool     `json:"reasoning"`
	StructuredOutput       bool     `json:"structured_output"`
	ReasoningLevels        []string `json:"reasoning_levels,omitempty"`
	DefaultReasoningEffort string   `json:"default_reasoning_effort,omitempty"`
}

// Modalities describes the input/output types a model supports.
type Modalities struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

// ModelMeta contains supplementary metadata about a model.
type ModelMeta struct {
	Family          string `json:"family,omitempty"`
	KnowledgeCutoff string `json:"knowledge_cutoff,omitempty"`
	ReleaseDate     string `json:"release_date,omitempty"`
	OpenWeights     bool   `json:"open_weights"`
}
