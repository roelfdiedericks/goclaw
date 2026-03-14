package voicellm

import "testing"

func TestAssessConfig(t *testing.T) {
	tests := []struct {
		name      string
		cfg       Config
		available bool
		message   string
	}{
		{
			name:      "disabled",
			cfg:       Config{},
			available: false,
			message:   "Voice chat is disabled in configuration.",
		},
		{
			name: "enabled no providers",
			cfg: Config{
				Enabled: true,
			},
			available: false,
			message:   "Voice chat is enabled, but no providers are configured.",
		},
		{
			name: "missing default",
			cfg: Config{
				Enabled: true,
				Providers: map[string]ProviderConfig{
					"xai": {Driver: "xai", APIKey: "abc"},
				},
			},
			available: false,
			message:   "Voice chat is enabled, but no default provider is selected.",
		},
		{
			name: "default not configured",
			cfg: Config{
				Enabled: true,
				Default: "openai",
				Providers: map[string]ProviderConfig{
					"xai": {Driver: "xai", APIKey: "abc"},
				},
			},
			available: false,
			message:   `Voice chat default provider "openai" is not configured.`,
		},
		{
			name: "missing driver",
			cfg: Config{
				Enabled: true,
				Default: "xai",
				Providers: map[string]ProviderConfig{
					"xai": {APIKey: "abc"},
				},
			},
			available: false,
			message:   `Voice chat provider "xai" has no driver configured.`,
		},
		{
			name: "missing api key",
			cfg: Config{
				Enabled: true,
				Default: "xai",
				Providers: map[string]ProviderConfig{
					"xai": {Driver: "xai"},
				},
			},
			available: false,
			message:   `Voice chat provider "xai" is missing an API key.`,
		},
		{
			name: "valid xai",
			cfg: Config{
				Enabled: true,
				Default: "xai",
				Providers: map[string]ProviderConfig{
					"xai": {Driver: "xai", APIKey: "abc"},
				},
			},
			available: true,
			message:   `Voice chat is ready using provider "xai".`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := AssessConfig(tt.cfg)
			if status.Available != tt.available {
				t.Fatalf("available = %v, want %v", status.Available, tt.available)
			}
			if status.Message != tt.message {
				t.Fatalf("message = %q, want %q", status.Message, tt.message)
			}
		})
	}
}
