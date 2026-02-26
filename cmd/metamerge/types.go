package main

import "time"

// --- Output schema (models.json) ---

type ModelsFile struct {
	Generated       string               `json:"generated"`
	CatwalkCommit   string               `json:"catwalk_commit,omitempty"`
	ModelsDevBranch string               `json:"models_dev_branch,omitempty"`
	Providers       map[string]*Provider `json:"providers"`
}

type Provider struct {
	Name              string            `json:"name"`
	APIEndpoint       string            `json:"api_endpoint"`
	APIKeyEnv         string            `json:"api_key_env"`
	CatwalkType       string            `json:"catwalk_type"`
	Driver            string            `json:"driver"`
	DefaultLargeModel string            `json:"default_large_model"`
	DefaultSmallModel string            `json:"default_small_model"`
	Models            map[string]*Model `json:"models"`
}

type Model struct {
	Name            string       `json:"name"`
	ContextWindow   int64        `json:"context_window"`
	MaxOutputTokens int64        `json:"max_output_tokens"`
	Cost            Cost         `json:"cost"`
	Capabilities    Capabilities `json:"capabilities"`
	Modalities      Modalities   `json:"modalities"`
	Metadata        Metadata     `json:"metadata"`
}

type Cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
	Reasoning  float64 `json:"reasoning"`
}

type Capabilities struct {
	Vision                 bool     `json:"vision"`
	ToolUse                bool     `json:"tool_use"`
	Streaming              bool     `json:"streaming"`
	Reasoning              bool     `json:"reasoning"`
	StructuredOutput       bool     `json:"structured_output"`
	ReasoningLevels        []string `json:"reasoning_levels,omitempty"`
	DefaultReasoningEffort string   `json:"default_reasoning_effort,omitempty"`
}

type Modalities struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

type Metadata struct {
	Family          string `json:"family,omitempty"`
	KnowledgeCutoff string `json:"knowledge_cutoff,omitempty"`
	ReleaseDate     string `json:"release_date,omitempty"`
	OpenWeights     bool   `json:"open_weights"`
}

// --- Catwalk source structs ---

type CatwalkProvider struct {
	Name                string         `json:"name"`
	ID                  string         `json:"id"`
	Type                string         `json:"type"`
	APIKey              string         `json:"api_key"`
	APIEndpoint         string         `json:"api_endpoint"`
	DefaultLargeModelID string         `json:"default_large_model_id"`
	DefaultSmallModelID string         `json:"default_small_model_id"`
	Models              []CatwalkModel `json:"models"`
}

type CatwalkModel struct {
	ID                     string   `json:"id"`
	Name                   string   `json:"name"`
	CostPer1MIn            float64  `json:"cost_per_1m_in"`
	CostPer1MOut           float64  `json:"cost_per_1m_out"`
	CostPer1MInCached      float64  `json:"cost_per_1m_in_cached"`
	CostPer1MOutCached     float64  `json:"cost_per_1m_out_cached"`
	ContextWindow          int64    `json:"context_window"`
	DefaultMaxTokens       int64    `json:"default_max_tokens"`
	CanReason              *bool    `json:"can_reason,omitempty"`
	SupportsAttachments    *bool    `json:"supports_attachments,omitempty"`
	ReasoningLevels        []string `json:"reasoning_levels,omitempty"`
	DefaultReasoningEffort string   `json:"default_reasoning_effort,omitempty"`
	// Catwalk uses both singular and plural — accept both
	DefaultReasoningEfforts string `json:"default_reasoning_efforts,omitempty"`
	HasReasoningEfforts     *bool  `json:"has_reasoning_efforts,omitempty"`
}

// CanReasonBool returns the effective can_reason value, defaulting to false.
func (m *CatwalkModel) CanReasonBool() bool {
	if m.CanReason != nil {
		return *m.CanReason
	}
	return false
}

// SupportsAttachmentsBool returns the effective supports_attachments value.
func (m *CatwalkModel) SupportsAttachmentsBool() bool {
	if m.SupportsAttachments != nil {
		return *m.SupportsAttachments
	}
	return false
}

// EffectiveReasoningEffort returns the reasoning effort, handling the
// singular/plural field name inconsistency in upstream Catwalk data.
func (m *CatwalkModel) EffectiveReasoningEffort() string {
	if m.DefaultReasoningEffort != "" {
		return m.DefaultReasoningEffort
	}
	return m.DefaultReasoningEfforts
}

// --- models.dev source structs ---

type ModelsDevModel struct {
	Name             string            `toml:"name"`
	Family           string            `toml:"family"`
	ReleaseDate      string            `toml:"release_date"`
	LastUpdated      string            `toml:"last_updated"`
	Attachment       bool              `toml:"attachment"`
	Reasoning        bool              `toml:"reasoning"`
	Temperature      bool              `toml:"temperature"`
	Knowledge        string            `toml:"knowledge"`
	ToolCall         *bool             `toml:"tool_call,omitempty"`
	StructuredOutput *bool             `toml:"structured_output,omitempty"`
	OpenWeights      bool              `toml:"open_weights"`
	Cost             ModelsDevCost     `toml:"cost"`
	Limit            ModelsDevLimit    `toml:"limit"`
	Modalities       ModelsDevModality `toml:"modalities"`
}

type ModelsDevCost struct {
	Input      float64 `toml:"input"`
	Output     float64 `toml:"output"`
	Reasoning  float64 `toml:"reasoning"`
	CacheRead  float64 `toml:"cache_read"`
	CacheWrite float64 `toml:"cache_write"`
}

type ModelsDevLimit struct {
	Context int64 `toml:"context"`
	Input   int64 `toml:"input"`
	Output  int64 `toml:"output"`
}

type ModelsDevModality struct {
	Input  []string `toml:"input"`
	Output []string `toml:"output"`
}

// --- Helpers ---

func nowISO() string {
	return time.Now().UTC().Format(time.RFC3339)
}
