package voicellm

// Config is the top-level VoiceLLM configuration
type Config struct {
	Providers   map[string]ProviderConfig `json:"providers"`
	Default     string                    `json:"default"`
	ServerVAD   bool                      `json:"serverVAD" default:"true"`
	IdleTimeout int                       `json:"idleTimeout" default:"300"`
	Enabled     bool                      `json:"enabled"`
	Prompt      PromptConfig              `json:"prompt,omitempty"`
}

// ProviderConfig is the configuration for a specific VoiceLLM provider
type ProviderConfig struct {
	Driver      string `json:"driver"`                    // "xai", "openai"
	APIKey      string `json:"apiKey"`
	Voice       string `json:"voice" default:"Eve"`       // Eve, Ara, marin, etc.
	SampleRate  int    `json:"sampleRate" default:"48000"` // 48kHz matches browser native rate
	AudioFormat string `json:"audioFormat" default:"pcm"` // pcm, pcmu, pcma
	BaseURL     string `json:"baseURL,omitempty"`
}

// PromptConfig contains configurable voice prompt settings
type PromptConfig struct {
	Language               string            `json:"language,omitempty"`
	MaxSentences           int               `json:"maxSentences,omitempty" default:"3"`
	Pronunciations         map[string]string `json:"pronunciations,omitempty"`
	AdditionalInstructions string            `json:"additionalInstructions,omitempty"`
}
