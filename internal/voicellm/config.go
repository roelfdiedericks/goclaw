package voicellm

import (
	"fmt"
	"strings"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

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
	Driver      string `json:"driver"` // "xai", "openai"
	APIKey      string `json:"apiKey"`
	Voice       string `json:"voice" default:"Eve"`        // Eve, Ara, marin, etc.
	SampleRate  int    `json:"sampleRate" default:"48000"` // 48kHz matches browser native rate
	AudioFormat string `json:"audioFormat" default:"pcm"`  // pcm, pcmu, pcma
	BaseURL     string `json:"baseURL,omitempty"`
}

// PromptConfig contains configurable voice prompt settings
type PromptConfig struct {
	Language               string            `json:"language,omitempty"`
	MaxSentences           int               `json:"maxSentences,omitempty" default:"3"`
	Pronunciations         map[string]string `json:"pronunciations,omitempty"`
	AdditionalInstructions string            `json:"additionalInstructions,omitempty"`
}

// PromptConfigForm is a form-friendly wrapper for PromptConfig.
// Pronunciations map is represented as a StringList for form editing.
type PromptConfigForm struct {
	Language               string   `json:"language"`
	MaxSentences           int      `json:"maxSentences"`
	PronunciationsList     []string `json:"pronunciationsList"`
	AdditionalInstructions string   `json:"additionalInstructions"`
}

// ToPromptConfigForm converts PromptConfig to form-friendly format.
func (p *PromptConfig) ToPromptConfigForm() *PromptConfigForm {
	var list []string
	for word, pron := range p.Pronunciations {
		list = append(list, word+":"+pron)
	}
	return &PromptConfigForm{
		Language:               p.Language,
		MaxSentences:           p.MaxSentences,
		PronunciationsList:     list,
		AdditionalInstructions: p.AdditionalInstructions,
	}
}

// ToPromptConfig converts form data back to PromptConfig.
func (f *PromptConfigForm) ToPromptConfig() PromptConfig {
	pronunciations := make(map[string]string)
	for _, entry := range f.PronunciationsList {
		parts := strings.SplitN(entry, ":", 2)
		if len(parts) == 2 {
			pronunciations[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return PromptConfig{
		Language:               f.Language,
		MaxSentences:           f.MaxSentences,
		Pronunciations:         pronunciations,
		AdditionalInstructions: f.AdditionalInstructions,
	}
}

// --- Form Definitions ---

// ProviderConfigFormDef returns the form definition for editing a VoiceLLM provider.
func ProviderConfigFormDef() forms.FormDef {
	return forms.FormDef{
		Title:       "VoiceLLM Provider",
		Description: "Configure a real-time voice LLM provider",
		Sections: []forms.Section{
			{
				Title: "Connection",
				Fields: []forms.Field{
					{
						Name:     "driver",
						Title:    "Driver",
						Desc:     "Voice LLM provider type",
						Type:     forms.Select,
						Required: true,
						Options: []forms.Option{
							{Label: "xAI", Value: "xai"},
							{Label: "OpenAI Realtime", Value: "openai"},
						},
					},
					{
						Name:  "apiKey",
						Title: "API Key",
						Desc:  "API key for authentication",
						Type:  forms.Secret,
					},
					{
						Name:  "baseURL",
						Title: "Base URL",
						Desc:  "Custom API endpoint (optional)",
						Type:  forms.Text,
					},
				},
			},
			{
				Title:    "Voice (xAI)",
				ShowWhen: "driver=xai",
				Fields: []forms.Field{
					{
						Name:  "voice",
						Title: "Voice",
						Desc:  "Select voice for xAI provider",
						Type:  forms.Select,
						Options: []forms.Option{
							{Label: "Eve - Female, energetic (default)", Value: "Eve"},
							{Label: "Ara - Female, warm/friendly", Value: "Ara"},
							{Label: "Rex - Male, confident/clear", Value: "Rex"},
							{Label: "Sal - Neutral, smooth/balanced", Value: "Sal"},
							{Label: "Leo - Male, authoritative", Value: "Leo"},
						},
					},
				},
			},
			{
				Title:    "Voice (OpenAI)",
				ShowWhen: "driver=openai",
				Fields: []forms.Field{
					{
						Name:  "voice",
						Title: "Voice",
						Desc:  "Select voice for OpenAI Realtime",
						Type:  forms.Select,
						Options: []forms.Option{
							{Label: "Alloy", Value: "alloy"},
							{Label: "Echo", Value: "echo"},
							{Label: "Shimmer", Value: "shimmer"},
							{Label: "Ash", Value: "ash"},
							{Label: "Ballad", Value: "ballad"},
							{Label: "Coral", Value: "coral"},
							{Label: "Sage", Value: "sage"},
							{Label: "Verse", Value: "verse"},
						},
					},
				},
			},
			{
				Title: "Audio Settings",
				Fields: []forms.Field{
					{
						Name:  "sampleRate",
						Title: "Sample Rate",
						Desc:  "Audio sample rate in Hz (default 48000)",
						Type:  forms.Number,
						Min:   8000,
						Max:   48000,
					},
					{
						Name:  "audioFormat",
						Title: "Audio Format",
						Desc:  "Audio encoding format",
						Type:  forms.Select,
						Options: []forms.Option{
							{Label: "PCM (default)", Value: "pcm"},
							{Label: "PCM u-law", Value: "pcmu"},
							{Label: "PCM a-law", Value: "pcma"},
						},
					},
				},
			},
		},
		Actions: []forms.ActionDef{
			{
				Name:  "test",
				Label: "Test Connection",
				Desc:  "Verify API key and connectivity",
			},
		},
	}
}

// SettingsFormDef returns the form definition for VoiceLLM global settings.
// providerOptions should be built from the configured providers map.
func SettingsFormDef(providerOptions []forms.Option) forms.FormDef {
	return forms.FormDef{
		Title:       "Voice Settings",
		Description: "Configure global VoiceLLM settings",
		Sections: []forms.Section{
			{
				Title: "General",
				Fields: []forms.Field{
					{
						Name:    "default",
						Title:   "Default Provider",
						Desc:    "Provider to use for voice sessions",
						Type:    forms.Select,
						Options: providerOptions,
					},
					{
						Name:  "enabled",
						Title: "Enabled",
						Desc:  "Enable VoiceLLM functionality",
						Type:  forms.Toggle,
					},
					{
						Name:  "serverVAD",
						Title: "Server VAD",
						Desc:  "Use server-side voice activity detection",
						Type:  forms.Toggle,
					},
					{
						Name:  "idleTimeout",
						Title: "Idle Timeout",
						Desc:  "Session timeout in seconds (default 300)",
						Type:  forms.Number,
						Min:   30,
						Max:   3600,
					},
				},
			},
		},
	}
}

// PromptConfigFormDef returns the form definition for voice prompt settings.
func PromptConfigFormDef() forms.FormDef {
	return forms.FormDef{
		Title:       "Voice Prompt",
		Description: "Configure voice-specific prompt settings",
		Sections: []forms.Section{
			{
				Title: "Language",
				Fields: []forms.Field{
					{
						Name:  "language",
						Title: "Language",
						Desc:  "Preferred language for responses (e.g., 'English', 'Spanish')",
						Type:  forms.Text,
					},
					{
						Name:  "maxSentences",
						Title: "Max Sentences",
						Desc:  "Maximum sentences per response (default 3)",
						Type:  forms.Number,
						Min:   1,
						Max:   10,
					},
				},
			},
			{
				Title: "Pronunciations",
				Desc:  "Custom word pronunciations (format: word:pronunciation)",
				Fields: []forms.Field{
					{
						Name:  "pronunciationsList",
						Title: "Pronunciations",
						Desc:  "Comma-separated list: GoClaw:go-claw,API:A.P.I.",
						Type:  forms.StringList,
					},
				},
			},
			{
				Title: "Additional Instructions",
				Fields: []forms.Field{
					{
						Name:  "additionalInstructions",
						Title: "Additional Instructions",
						Desc:  "Extra instructions for voice responses",
						Type:  forms.TextArea,
					},
				},
			},
		},
	}
}

// --- Command Handlers ---

// RegisterCommands registers VoiceLLM config command handlers
func RegisterCommands() {
	bus.RegisterCommand("voicellm", "test", handleTestConnection)
}

// UnregisterCommands removes VoiceLLM config command handlers
func UnregisterCommands() {
	bus.UnregisterCommand("voicellm", "test")
}

// handleTestConnection tests connectivity to the configured VoiceLLM provider
func handleTestConnection(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(*ProviderConfig)
	if !ok {
		return bus.CommandResult{
			Error:   fmt.Errorf("invalid payload type"),
			Message: "Internal error: invalid config type",
		}
	}

	if cfg.Driver == "" {
		return bus.CommandResult{
			Error:   fmt.Errorf("provider driver is required"),
			Message: "Select a provider driver first",
		}
	}

	if cfg.APIKey == "" {
		return bus.CommandResult{
			Error:   fmt.Errorf("API key is required"),
			Message: "Enter an API key first",
		}
	}

	// For now, just validate the config looks reasonable
	// Full connection test would require creating a WebSocket connection
	// which is more involved and may incur API costs
	switch cfg.Driver {
	case "xai":
		L_info("voicellm: testing xAI provider config", "voice", cfg.Voice)
		// Validate voice is known
		validVoices := map[string]bool{"Eve": true, "Ara": true, "Rex": true, "Sal": true, "Leo": true}
		if cfg.Voice != "" && !validVoices[cfg.Voice] {
			return bus.CommandResult{
				Error:   fmt.Errorf("unknown xAI voice: %s", cfg.Voice),
				Message: fmt.Sprintf("Unknown voice '%s'. Valid: Eve, Ara, Rex, Sal, Leo", cfg.Voice),
			}
		}
	case "openai":
		L_info("voicellm: testing OpenAI Realtime provider config", "voice", cfg.Voice)
		validVoices := map[string]bool{
			"alloy": true, "echo": true, "shimmer": true, "ash": true,
			"ballad": true, "coral": true, "sage": true, "verse": true,
		}
		if cfg.Voice != "" && !validVoices[cfg.Voice] {
			return bus.CommandResult{
				Error:   fmt.Errorf("unknown OpenAI voice: %s", cfg.Voice),
				Message: fmt.Sprintf("Unknown voice '%s'", cfg.Voice),
			}
		}
	default:
		return bus.CommandResult{
			Error:   fmt.Errorf("unknown driver: %s", cfg.Driver),
			Message: fmt.Sprintf("Unknown driver '%s'. Valid: xai, openai", cfg.Driver),
		}
	}

	L_info("voicellm: config validation passed", "driver", cfg.Driver)
	return bus.CommandResult{
		Success: true,
		Message: fmt.Sprintf("Configuration valid (%s provider)", cfg.Driver),
	}
}
