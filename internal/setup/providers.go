package setup

// ProviderPreset defines a predefined LLM provider option
type ProviderPreset struct {
	Name               string   // Display name
	Key                string   // Internal key (e.g., "anthropic")
	Type               string   // Provider type ("anthropic", "openai", "ollama")
	BaseURL            string   // API base URL
	Description        string   // Brief description
	IsLocal            bool     // Local provider (no API key required by default)
	SupportsEmbeddings bool     // Whether this provider supports embeddings
	KnownEmbedModels   []string // Popular/recommended embedding models (format: "model|dims")
	KnownChatModels    []string // Popular/recommended chat models
}

// Presets contains all predefined provider options
var Presets = []ProviderPreset{
	{
		Name:               "Anthropic (Claude)",
		Key:                "anthropic",
		Type:               "anthropic",
		BaseURL:            "https://api.anthropic.com",
		Description:        "Recommended, Claude models",
		SupportsEmbeddings: false, // Anthropic has no embeddings API
		KnownChatModels: []string{
			"claude-opus-4-5",
			"claude-sonnet-4",
			"claude-3-5-sonnet-20241022",
			"claude-3-haiku-20240307",
		},
	},
	{
		Name:               "OpenAI (GPT)",
		Key:                "openai",
		Type:               "openai",
		BaseURL:            "https://api.openai.com/v1",
		Description:        "GPT-4, GPT-4o models",
		SupportsEmbeddings: true,
		KnownEmbedModels: []string{
			"text-embedding-3-small|1536",
			"text-embedding-3-large|3072",
			"text-embedding-ada-002|1536",
		},
		KnownChatModels: []string{
			"gpt-4o",
			"gpt-4-turbo",
			"gpt-4",
			"gpt-3.5-turbo",
		},
	},
	{
		Name:               "Kimi (Moonshot)",
		Key:                "kimi",
		Type:               "openai",
		BaseURL:            "https://api.moonshot.cn/v1",
		Description:        "Moonshot AI",
		SupportsEmbeddings: false, // No embeddings API
		KnownChatModels: []string{
			"moonshot-v1-128k",
			"moonshot-v1-32k",
			"moonshot-v1-8k",
		},
	},
	{
		Name:               "OpenRouter",
		Key:                "openrouter",
		Type:               "openai",
		BaseURL:            "https://openrouter.ai/api/v1",
		Description:        "400+ models, multiple providers",
		SupportsEmbeddings: true,
		KnownEmbedModels: []string{
			"openai/text-embedding-3-small|1536",
			"openai/text-embedding-3-large|3072",
		},
		KnownChatModels: []string{
			"anthropic/claude-3.5-sonnet",
			"openai/gpt-4o",
			"google/gemini-pro-1.5",
			"meta-llama/llama-3.1-405b-instruct",
		},
	},
	{
		Name:               "Ollama (local)",
		Key:                "ollama",
		Type:               "ollama",
		BaseURL:            "http://localhost:11434",
		Description:        "Run models locally",
		IsLocal:            true,
		SupportsEmbeddings: true,
		KnownEmbedModels: []string{
			"nomic-embed-text|768",
			"mxbai-embed-large|1024",
			"all-minilm|384",
		},
		KnownChatModels: []string{
			"llama3.2:latest",
			"llama3.1:70b",
			"mistral:latest",
			"codellama:latest",
		},
	},
	{
		Name:               "LM Studio (local)",
		Key:                "lmstudio",
		Type:               "openai",
		BaseURL:            "http://localhost:1234/v1",
		Description:        "Local models, nice UI",
		IsLocal:            true,
		SupportsEmbeddings: true,       // Assumes user loads embedding model
		KnownEmbedModels:   []string{}, // Dynamic - fetch from API
		KnownChatModels:    []string{}, // Dynamic - fetch from API
	},
}

// GetPreset returns a preset by key, or nil if not found
func GetPreset(key string) *ProviderPreset {
	for i := range Presets {
		if Presets[i].Key == key {
			return &Presets[i]
		}
	}
	return nil
}

// EmbeddingCapablePresets returns only presets that support embeddings
func EmbeddingCapablePresets() []ProviderPreset {
	var result []ProviderPreset
	for _, p := range Presets {
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
		SupportsEmbeddings: true, // Assume yes, let user try
		KnownEmbedModels:   []string{},
		KnownChatModels:    []string{},
	}
}
