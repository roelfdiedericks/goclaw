package main

// driverMap maps Catwalk provider ID to the GoClaw runtime driver.
// Driver corresponds to internal/llm/*.go implementation:
//
//	anthropic -> anthropic.go
//	openai    -> openai.go
//	xai       -> xai.go
//	ollama    -> ollama.go
var driverMap = map[string]string{
	"anthropic":   "anthropic",
	"openai":      "openai",
	"xai":         "xai",
	"deepseek":    "openai",
	"groq":        "openai",
	"openrouter":  "openai",
	"gemini":      "openai",
	"azure":       "openai",
	"kimi-coding": "anthropic",
	"kimi":        "openai",
	"zai":         "openai",
	"ollama":      "ollama",
	"lmstudio":    "openai",
}

// modelsDevProviderMap maps Catwalk provider ID to models.dev provider
// directory name. Empty string means no models.dev lookup for that provider.
var modelsDevProviderMap = map[string]string{
	"anthropic":   "anthropic",
	"openai":      "openai",
	"xai":         "xai",
	"deepseek":    "deepseek",
	"groq":        "groq",
	"gemini":      "google",
	"azure":       "openai",
	"kimi-coding": "",
	"kimi":        "",
	"zai":         "",
	"openrouter":  "",
	"ollama":      "",
	"lmstudio":    "",
}

// defaultEndpoints provides well-known API endpoints for providers where
// Catwalk uses env var references instead of hardcoded URLs.
var defaultEndpoints = map[string]string{
	"anthropic": "https://api.anthropic.com",
	"openai":    "https://api.openai.com/v1",
	"gemini":    "https://generativelanguage.googleapis.com/v1beta/openai",
}

// catwalkProviders is the ordered list of Catwalk provider config filenames to fetch.
// The key is the filename (without .json), the value is the expected provider ID
// in the parsed JSON (they differ for kimi).
var catwalkProviders = []struct {
	Filename   string
	ExpectedID string
}{
	{"anthropic", "anthropic"},
	{"openai", "openai"},
	{"xai", "xai"},
	{"deepseek", "deepseek"},
	{"groq", "groq"},
	{"openrouter", "openrouter"},
	{"gemini", "gemini"},
	{"azure", "azure"},
	{"kimi", "kimi-coding"},
	{"zai", "zai"},
}
