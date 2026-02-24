package stt

import (
	"fmt"
	"path/filepath"

	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/paths"
)

// Config holds STT configuration.
type Config struct {
	Provider   string           `json:"provider"`   // "whispercpp", "openai", "groq", "google"
	WhisperCpp WhisperCppConfig `json:"whispercpp"` // Local whisper.cpp
	OpenAI     OpenAIConfig     `json:"openai"`     // OpenAI Whisper API
	Groq       GroqConfig       `json:"groq"`       // Groq Whisper API
	Google     GoogleConfig     `json:"google"`     // Google Cloud STT
}

// OpenAIConfig holds OpenAI Whisper configuration.
type OpenAIConfig struct {
	APIKey string `json:"apiKey"`
	Model  string `json:"model"` // "whisper-1"
}

// GroqConfig holds Groq Whisper configuration.
type GroqConfig struct {
	APIKey string `json:"apiKey"`
	Model  string `json:"model"` // "whisper-large-v3", "whisper-large-v3-turbo", "distil-whisper-large-v3-en"
}

// GoogleConfig holds Google Cloud STT configuration.
type GoogleConfig struct {
	APIKey       string `json:"apiKey"`       // Simple API key
	LanguageCode string `json:"languageCode"` // e.g., "en-US", "en-ZA"
}

// providerInstance holds the singleton STT provider.
var providerInstance Provider

// GetProvider returns the current STT provider (may be nil if not configured).
func GetProvider() Provider {
	return providerInstance
}

// ApplyConfig initializes the STT provider based on configuration.
// Returns nil if no provider is configured.
func ApplyConfig(cfg Config) error {
	// Close existing provider if any
	if providerInstance != nil {
		if err := providerInstance.Close(); err != nil {
			L_warn("stt: failed to close existing provider", "error", err)
		}
		providerInstance = nil
	}

	if cfg.Provider == "" {
		L_debug("stt: no provider configured")
		return nil
	}

	switch cfg.Provider {
	case "whispercpp":
		return applyWhisperCppConfig(cfg.WhisperCpp)
	case "openai":
		return applyOpenAIConfig(cfg.OpenAI)
	case "groq":
		return applyGroqConfig(cfg.Groq)
	case "google":
		return applyGoogleConfig(cfg.Google)
	default:
		return fmt.Errorf("stt: unknown provider: %s", cfg.Provider)
	}
}

// applyWhisperCppConfig initializes the Whisper.cpp provider.
func applyWhisperCppConfig(cfg WhisperCppConfig) error {
	if cfg.ModelsDir == "" || cfg.Model == "" {
		L_warn("stt: whispercpp not fully configured", "modelsDir", cfg.ModelsDir, "model", cfg.Model)
		return nil
	}

	// Expand ~ in modelsDir
	modelsDir, err := paths.ExpandTilde(cfg.ModelsDir)
	if err != nil {
		return fmt.Errorf("stt: failed to expand models dir: %w", err)
	}

	// Validate model exists
	modelPath := filepath.Join(modelsDir, cfg.Model)
	if !IsModelDownloaded(modelsDir, cfg.Model) {
		return fmt.Errorf("stt: model not found at %s - use 'Download Model' button to download", modelPath)
	}

	provider, err := NewWhisperCppProvider(WhisperCppConfig{
		ModelsDir: modelsDir,
		Model:     cfg.Model,
		Language:  cfg.Language,
		Threads:   cfg.Threads,
	})
	if err != nil {
		return fmt.Errorf("stt: failed to initialize whispercpp: %w", err)
	}

	providerInstance = provider
	L_info("stt: whispercpp provider initialized", "model", cfg.Model)
	return nil
}

// applyOpenAIConfig initializes the OpenAI Whisper provider.
func applyOpenAIConfig(cfg OpenAIConfig) error {
	if cfg.APIKey == "" {
		L_warn("stt: openai API key not configured")
		return nil
	}

	provider, err := NewOpenAIProvider(cfg)
	if err != nil {
		return fmt.Errorf("stt: failed to initialize openai: %w", err)
	}

	providerInstance = provider
	L_info("stt: openai provider initialized")
	return nil
}

// applyGroqConfig initializes the Groq Whisper provider.
func applyGroqConfig(cfg GroqConfig) error {
	if cfg.APIKey == "" {
		L_warn("stt: groq API key not configured")
		return nil
	}

	provider, err := NewGroqProvider(cfg)
	if err != nil {
		return fmt.Errorf("stt: failed to initialize groq: %w", err)
	}

	providerInstance = provider
	L_info("stt: groq provider initialized")
	return nil
}

// applyGoogleConfig initializes the Google Cloud STT provider.
func applyGoogleConfig(cfg GoogleConfig) error {
	if cfg.APIKey == "" {
		L_warn("stt: google API key not configured")
		return nil
	}

	provider, err := NewGoogleProvider(cfg)
	if err != nil {
		return fmt.Errorf("stt: failed to initialize google: %w", err)
	}

	providerInstance = provider
	L_info("stt: google provider initialized")
	return nil
}

// Close shuts down the STT provider.
func Close() {
	if providerInstance != nil {
		if err := providerInstance.Close(); err != nil {
			L_warn("stt: failed to close provider", "error", err)
		}
		providerInstance = nil
	}
}

// ConfigFormDef returns the form definition for STT configuration.
func ConfigFormDef(currentModelsDir string) forms.FormDef {
	// Get all available whisper models from catalog
	expandedDir, _ := paths.ExpandTilde(currentModelsDir)
	modelOptions := GetModelOptions(expandedDir)

	// Convert to forms.Option
	whisperModelOptions := make([]forms.Option, len(modelOptions))
	for i, opt := range modelOptions {
		whisperModelOptions[i] = forms.Option{
			Label: opt.Label,
			Value: opt.Value,
		}
	}

	return forms.FormDef{
		Title:       "Speech-to-Text (STT)",
		Description: "Configure voice transcription for audio messages",
		Actions: []forms.ActionDef{
			{
				Name:  "download",
				Label: "Download Model",
				Desc:  "Download the selected whisper model",
			},
		},
		Sections: []forms.Section{
			{
				Title: "Provider",
				Fields: []forms.Field{
					{
						Name:  "provider",
						Title: "STT Provider",
						Desc:  "Speech-to-text engine",
						Type:  forms.Select,
						Options: []forms.Option{
							{Label: "Disabled", Value: ""},
							{Label: "Whisper.cpp (Local/Offline)", Value: "whispercpp"},
							{Label: "OpenAI Whisper (Cloud)", Value: "openai"},
							{Label: "Groq Whisper (Cloud - Fast)", Value: "groq"},
							{Label: "Google Cloud STT (Features)", Value: "google"},
						},
					},
				},
			},
			{
				Title:    "Whisper.cpp Settings",
				ShowWhen: "provider=whispercpp",
				Fields: []forms.Field{
					{
						Name:    "whispercpp.modelsDir",
						Title:   "Models Directory",
						Desc:    "Directory containing whisper model files (ggml-*.bin)",
						Type:    forms.Text,
						Default: "~/.goclaw/stt/whisper",
					},
					{
						Name:    "whispercpp.model",
						Title:   "Model",
						Desc:    "Select a model (tiny=fastest, large=best quality)",
						Type:    forms.Select,
						Options: whisperModelOptions,
					},
					{
						Name:    "whispercpp.language",
						Title:   "Language",
						Desc:    "Language code (e.g., 'en', 'auto' for detection)",
						Type:    forms.Text,
						Default: "en",
					},
				},
			},
			{
				Title:    "OpenAI Settings",
				ShowWhen: "provider=openai",
				Fields: []forms.Field{
					{
						Name:  "openai.apiKey",
						Title: "API Key",
						Desc:  "OpenAI API key",
						Type:  forms.Secret,
					},
				},
			},
			{
				Title:    "Groq Settings",
				ShowWhen: "provider=groq",
				Fields: []forms.Field{
					{
						Name:  "groq.apiKey",
						Title: "API Key",
						Desc:  "Groq API key",
						Type:  forms.Secret,
					},
					{
						Name:  "groq.model",
						Title: "Model",
						Desc:  "Whisper model variant",
						Type:  forms.Select,
						Options: []forms.Option{
							{Label: "Whisper Large V3", Value: "whisper-large-v3"},
							{Label: "Whisper Large V3 Turbo (faster)", Value: "whisper-large-v3-turbo"},
							{Label: "Distil Whisper Large V3 EN (English only)", Value: "distil-whisper-large-v3-en"},
						},
					},
				},
			},
			{
				Title:    "Google Cloud Settings",
				ShowWhen: "provider=google",
				Fields: []forms.Field{
					{
						Name:  "google.apiKey",
						Title: "API Key",
						Desc:  "Google Cloud API key (enable Speech-to-Text API)",
						Type:  forms.Secret,
					},
					{
						Name:    "google.languageCode",
						Title:   "Language Code",
						Desc:    "BCP-47 language code (e.g., en-US, en-ZA)",
						Type:    forms.Text,
						Default: "en-US",
					},
				},
			},
		},
	}
}
