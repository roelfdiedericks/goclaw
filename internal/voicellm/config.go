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
	Effects     EffectsConfig             `json:"effects,omitempty"` // Global audio effects (provider-agnostic)
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

// EffectsConfig configures audio effects applied to voice output
type EffectsConfig struct {
	Preset   string         `json:"preset,omitempty"`    // UI helper for setup forms; runtime uses expanded values below.
	Mode     string         `json:"mode" default:"none"` // "none", "ring", "bitcrush", "both"
	Ring     RingModConfig  `json:"ring,omitempty"`
	Bitcrush BitcrushConfig `json:"bitcrush,omitempty"`
}

// RingModConfig configures ring modulation (carrier wave multiplication)
type RingModConfig struct {
	CarrierFreq float64 `json:"carrierFreq" default:"200"` // Hz: 30=Dalek, 150-200=metallic, 400+=bell
	Mix         float64 `json:"mix" default:"0.7"`         // 0.0=dry/original, 1.0=full effect
}

// BitcrushConfig configures bit depth reduction and downsampling
type BitcrushConfig struct {
	BitDepth   int `json:"bitDepth" default:"8"`   // target bits: 16=none, 8=crunchy, 4=lo-fi
	Downsample int `json:"downsample" default:"2"` // rate reduction: 1=none, 2=half, 4=quarter
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

// ConfigFormData is a web-form-friendly wrapper for VoiceLLM config.
// Effects are flattened through EffectsFormData so preset editing can round-trip.
type ConfigFormData struct {
	Providers   map[string]ProviderConfig `json:"providers"`
	Default     string                    `json:"default"`
	ServerVAD   bool                      `json:"serverVAD"`
	IdleTimeout int                       `json:"idleTimeout"`
	Enabled     bool                      `json:"enabled"`
	Prompt      PromptConfig              `json:"prompt,omitempty"`
	Effects     EffectsFormData           `json:"effects,omitempty"`
}

// ToConfigFormData converts Config to the form-friendly wrapper used by the web editor.
func (c *Config) ToConfigFormData() *ConfigFormData {
	if c == nil {
		return &ConfigFormData{}
	}
	return &ConfigFormData{
		Providers:   c.Providers,
		Default:     c.Default,
		ServerVAD:   c.ServerVAD,
		IdleTimeout: c.IdleTimeout,
		Enabled:     c.Enabled,
		Prompt:      c.Prompt,
		Effects:     *c.Effects.ToEffectsFormData(),
	}
}

// ToConfig converts the form-friendly wrapper back to runtime VoiceLLM config.
func (f *ConfigFormData) ToConfig() Config {
	if f == nil {
		return Config{}
	}
	effects := f.Effects
	if effects.Preset != "" && effects.Preset != "custom" {
		effects.ApplyPreset(effects.Preset)
	}
	return Config{
		Providers:   f.Providers,
		Default:     f.Default,
		ServerVAD:   f.ServerVAD,
		IdleTimeout: f.IdleTimeout,
		Enabled:     f.Enabled,
		Prompt:      f.Prompt,
		Effects:     effects.ToEffectsConfig(),
	}
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

// EffectsConfigFormDef returns the form definition for audio effects settings.
// Note: Preset handling requires custom logic in the editor - the form definition
// just provides the structure. The editor must handle preset changes to populate fields.
func EffectsConfigFormDef() forms.FormDef {
	// Build preset options from defined presets
	presetOptions := make([]forms.Option, len(EffectsPresets))
	for i, p := range EffectsPresets {
		presetOptions[i] = forms.Option{Label: p.Label, Value: p.Name}
	}

	return forms.FormDef{
		Title:       "Audio Effects",
		Description: "Apply real-time effects to voice output (robot voice, lo-fi, etc.)",
		Sections: []forms.Section{
			{
				Title: "Preset",
				Fields: []forms.Field{
					{
						Name:    "preset",
						Title:   "Preset",
						Desc:    "Select a preset or customize below",
						Type:    forms.Select,
						Options: presetOptions,
					},
				},
			},
			{
				Title:    "Effect Mode",
				ShowWhen: "preset=custom",
				Fields: []forms.Field{
					{
						Name:  "mode",
						Title: "Mode",
						Desc:  "Which effects to apply",
						Type:  forms.Select,
						Options: []forms.Option{
							{Label: "None (natural voice)", Value: "none"},
							{Label: "Ring Modulation (metallic/robotic)", Value: "ring"},
							{Label: "Bitcrush (lo-fi/crunchy)", Value: "bitcrush"},
							{Label: "Both (full robot)", Value: "both"},
						},
					},
				},
			},
			{
				Title:    "Ring Modulation",
				ShowWhen: "preset=custom,mode=ring,mode=both",
				Fields: []forms.Field{
					{
						Name:  "carrierFreq",
						Title: "Carrier Frequency (Hz)",
						Desc:  "30=Dalek, 150-200=metallic, 400+=bell-like",
						Type:  forms.Number,
						Min:   20,
						Max:   500,
					},
					{
						Name:  "mix",
						Title: "Mix",
						Desc:  "0.0=dry/original, 1.0=full effect (0.5-0.7 recommended)",
						Type:  forms.Number,
						Min:   0,
						Max:   1,
						Step:  0.1,
					},
				},
			},
			{
				Title:    "Bitcrush",
				ShowWhen: "preset=custom,mode=bitcrush,mode=both",
				Fields: []forms.Field{
					{
						Name:  "bitDepth",
						Title: "Bit Depth",
						Desc:  "16=clean, 8=crunchy, 4=very lo-fi",
						Type:  forms.Number,
						Min:   2,
						Max:   16,
					},
					{
						Name:  "downsample",
						Title: "Downsample Factor",
						Desc:  "1=none, 2=half rate, 4=quarter rate",
						Type:  forms.Number,
						Min:   1,
						Max:   8,
					},
				},
			},
		},
	}
}

// EffectsPreset defines a named preset configuration
type EffectsPreset struct {
	Name        string
	Label       string // Display label
	Mode        string
	CarrierFreq float64
	Mix         float64
	BitDepth    int
	Downsample  int
}

// EffectsPresets defines available effect presets
var EffectsPresets = []EffectsPreset{
	{
		Name:  "none",
		Label: "None (natural voice)",
		Mode:  "none",
	},
	{
		Name:        "battlestar",
		Label:       "Battlestar Galactica",
		Mode:        "both",
		CarrierFreq: 200,
		Mix:         0.7,
		BitDepth:    8,
		Downsample:  2,
	},
	{
		Name:        "dalek",
		Label:       "Dalek",
		Mode:        "ring",
		CarrierFreq: 30,
		Mix:         0.8,
	},
	{
		Name:        "metallic",
		Label:       "Metallic",
		Mode:        "ring",
		CarrierFreq: 180,
		Mix:         0.5,
	},
	{
		Name:       "lofi",
		Label:      "Lo-Fi Radio",
		Mode:       "bitcrush",
		BitDepth:   6,
		Downsample: 3,
	},
	{
		Name:  "custom",
		Label: "Custom",
		Mode:  "", // special marker
	},
}

// GetEffectsPreset returns a preset by name, or nil if not found
func GetEffectsPreset(name string) *EffectsPreset {
	for i := range EffectsPresets {
		if EffectsPresets[i].Name == name {
			return &EffectsPresets[i]
		}
	}
	return nil
}

// EffectsFormData is a flattened form-friendly struct for effects editing
type EffectsFormData struct {
	Preset      string  `json:"preset"` // Preset name or "custom"
	Mode        string  `json:"mode"`
	CarrierFreq float64 `json:"carrierFreq"`
	Mix         float64 `json:"mix"`
	BitDepth    int     `json:"bitDepth"`
	Downsample  int     `json:"downsample"`
}

// DetectPreset checks if current values match a known preset
func (f *EffectsFormData) DetectPreset() string {
	for _, p := range EffectsPresets {
		if p.Name == "none" && f.Mode == "none" {
			return "none"
		}
		if p.Name == "custom" {
			continue
		}
		if p.Mode != f.Mode {
			continue
		}

		// Only compare fields relevant to this mode
		switch p.Mode {
		case "ring":
			if p.CarrierFreq == f.CarrierFreq && p.Mix == f.Mix {
				return p.Name
			}
		case "bitcrush":
			if p.BitDepth == f.BitDepth && p.Downsample == f.Downsample {
				return p.Name
			}
		case "both":
			if p.CarrierFreq == f.CarrierFreq && p.Mix == f.Mix &&
				p.BitDepth == f.BitDepth && p.Downsample == f.Downsample {
				return p.Name
			}
		}
	}
	return "custom"
}

// ApplyPreset applies a preset's values to this form data
func (f *EffectsFormData) ApplyPreset(presetName string) {
	p := GetEffectsPreset(presetName)
	if p == nil || p.Name == "custom" {
		f.Preset = "custom"
		return
	}
	f.Preset = p.Name
	f.Mode = p.Mode
	if p.Mode == "ring" || p.Mode == "both" {
		f.CarrierFreq = p.CarrierFreq
		f.Mix = p.Mix
	}
	if p.Mode == "bitcrush" || p.Mode == "both" {
		f.BitDepth = p.BitDepth
		f.Downsample = p.Downsample
	}
}

// ToEffectsFormData converts EffectsConfig to form-friendly format
func (e *EffectsConfig) ToEffectsFormData() *EffectsFormData {
	mode := e.Mode
	if mode == "" {
		mode = "none"
	}
	carrierFreq := e.Ring.CarrierFreq
	if carrierFreq == 0 {
		carrierFreq = 200
	}
	mix := e.Ring.Mix
	if mix == 0 {
		mix = 0.7
	}
	bitDepth := e.Bitcrush.BitDepth
	if bitDepth == 0 {
		bitDepth = 8
	}
	downsample := e.Bitcrush.Downsample
	if downsample == 0 {
		downsample = 2
	}
	fd := &EffectsFormData{
		Mode:        mode,
		CarrierFreq: carrierFreq,
		Mix:         mix,
		BitDepth:    bitDepth,
		Downsample:  downsample,
	}
	fd.Preset = fd.DetectPreset()
	return fd
}

// ToEffectsConfig converts form data back to EffectsConfig
func (f *EffectsFormData) ToEffectsConfig() EffectsConfig {
	return EffectsConfig{
		Mode: f.Mode,
		Ring: RingModConfig{
			CarrierFreq: f.CarrierFreq,
			Mix:         f.Mix,
		},
		Bitcrush: BitcrushConfig{
			BitDepth:   f.BitDepth,
			Downsample: f.Downsample,
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

// ConfigFormDef returns the unified form definition for VoiceLLM configuration.
func ConfigFormDef() forms.FormDef {
	voiceOptions := []forms.Option{
		{Value: "Eve", Label: "Eve (Female, energetic)"},
		{Value: "Ara", Label: "Ara (Female, warm)"},
		{Value: "Rex", Label: "Rex (Male, confident)"},
		{Value: "Sal", Label: "Sal (Neutral, balanced)"},
		{Value: "Leo", Label: "Leo (Male, authoritative)"},
	}

	providerOptions := []forms.Option{
		{Value: "xai", Label: "xAI"},
	}

	driverOptions := []forms.Option{
		{Value: "xai", Label: "xAI"},
	}

	return forms.FormDef{
		Title:       "Voice LLM Configuration",
		Description: "Configure real-time voice interaction",
		Sections: []forms.Section{
			{
				Title: "General",
				Fields: []forms.Field{
					{Name: "enabled", Title: "Enabled", Type: forms.Toggle},
					{Name: "default", Title: "Default Provider", Desc: "Provider used for browser voice sessions", Type: forms.Select, Options: providerOptions, Default: "xai"},
					{Name: "serverVAD", Title: "Server VAD", Desc: "Server-side voice detection", Type: forms.Toggle},
					{Name: "idleTimeout", Title: "Idle Timeout", Desc: "Seconds before disconnect", Type: forms.Number},
				},
			},
			{
				Title:     "XAI Provider",
				FieldName: "providers.xai",
				Fields: []forms.Field{
					{Name: "driver", Title: "Driver", Type: forms.Select, Options: driverOptions, Default: "xai"},
					{Name: "apiKey", Title: "API Key", Type: forms.Secret},
					{Name: "voice", Title: "Voice", Type: forms.Select, Options: voiceOptions, Default: "Eve"},
					{Name: "sampleRate", Title: "Sample Rate", Type: forms.Number, Default: 48000},
				},
			},
			{
				Title:     "Voice Prompt",
				FieldName: "prompt",
				Collapsed: true,
				Fields: []forms.Field{
					{Name: "language", Title: "Language", Type: forms.Text, Default: "English"},
					{Name: "maxSentences", Title: "Max Sentences", Type: forms.Number, Default: 3},
				},
			},
			{
				Title:     "Audio Effects",
				FieldName: "effects",
				Collapsed: true,
				Nested:    ptrFormDef(EffectsConfigWebFormDef()),
			},
		},
	}
}

func ptrFormDef(def forms.FormDef) *forms.FormDef {
	return &def
}

// EffectsConfigWebFormDef returns the nested audio-effects form used by the web config page.
func EffectsConfigWebFormDef() forms.FormDef {
	presetOptions := make([]forms.Option, len(EffectsPresets))
	for i, p := range EffectsPresets {
		presetOptions[i] = forms.Option{Label: p.Label, Value: p.Name}
	}

	modeOptions := []forms.Option{
		{Value: "none", Label: "None (natural voice)"},
		{Value: "ring", Label: "Ring Modulation (metallic/robotic)"},
		{Value: "bitcrush", Label: "Bitcrush (lo-fi/crunchy)"},
		{Value: "both", Label: "Both (full robot)"},
	}

	return forms.FormDef{
		Sections: []forms.Section{
			{
				Title: "Preset",
				Fields: []forms.Field{
					{Name: "preset", Title: "Preset", Desc: "Choose a ready-made effect or switch to Custom to tune each value yourself.", Type: forms.Select, Options: presetOptions, Default: "none"},
				},
			},
			{
				Title:    "Effect Mode",
				ShowWhen: "effects.preset=custom",
				Fields: []forms.Field{
					{Name: "mode", Title: "Mode", Desc: "Which effects to apply when using a custom setup.", Type: forms.Select, Options: modeOptions, Default: "none"},
				},
			},
			{
				Title:     "Ring Modulation",
				ShowWhen:  "effects.preset=custom,effects.mode=ring,effects.mode=both",
				FieldName: "ring",
				Fields: []forms.Field{
					{Name: "carrierFreq", Title: "Carrier Frequency (Hz)", Desc: "30=Dalek, 150-200=metallic, 400+=bell-like", Type: forms.Number, Min: 20, Max: 500, Default: 200},
					{Name: "mix", Title: "Mix", Desc: "0.0=dry/original, 1.0=full effect (0.5-0.7 recommended)", Type: forms.Number, Min: 0, Max: 1, Step: 0.1, Default: 0.7},
				},
			},
			{
				Title:     "Bitcrush",
				ShowWhen:  "effects.preset=custom,effects.mode=bitcrush,effects.mode=both",
				FieldName: "bitcrush",
				Fields: []forms.Field{
					{Name: "bitDepth", Title: "Bit Depth", Desc: "16=clean, 8=crunchy, 4=very lo-fi", Type: forms.Number, Min: 2, Max: 16, Default: 8},
					{Name: "downsample", Title: "Downsample Factor", Desc: "1=none, 2=half rate, 4=quarter rate", Type: forms.Number, Min: 1, Max: 8, Default: 2},
				},
			},
		},
	}
}
