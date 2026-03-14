package voicellm

import "fmt"

// Availability describes whether VoiceLLM is usable for browser voice sessions.
type Availability struct {
	Available      bool
	Message        string
	Default        string
	Configured     []string
	ProviderDriver string
}

// AssessConfig validates whether a VoiceLLM config is usable at runtime.
// It is intentionally stricter than "enabled" because the voice web UI should
// not offer a Connect button when the backend cannot create sessions.
func AssessConfig(cfg Config) Availability {
	status := Availability{
		Default:    cfg.Default,
		Configured: configuredProviderNames(cfg.Providers),
	}

	if !cfg.Enabled {
		status.Message = "Voice chat is disabled in configuration."
		return status
	}

	if len(cfg.Providers) == 0 {
		status.Message = "Voice chat is enabled, but no providers are configured."
		return status
	}

	if cfg.Default == "" {
		status.Message = "Voice chat is enabled, but no default provider is selected."
		return status
	}

	provCfg, ok := cfg.Providers[cfg.Default]
	if !ok {
		status.Message = fmt.Sprintf("Voice chat default provider %q is not configured.", cfg.Default)
		return status
	}

	status.ProviderDriver = provCfg.Driver

	if provCfg.Driver == "" {
		status.Message = fmt.Sprintf("Voice chat provider %q has no driver configured.", cfg.Default)
		return status
	}

	switch provCfg.Driver {
	case "xai":
		// Supported below.
	default:
		status.Message = fmt.Sprintf("Voice chat provider %q uses unsupported driver %q.", cfg.Default, provCfg.Driver)
		return status
	}

	if provCfg.APIKey == "" {
		status.Message = fmt.Sprintf("Voice chat provider %q is missing an API key.", cfg.Default)
		return status
	}

	status.Available = true
	status.Message = fmt.Sprintf("Voice chat is ready using provider %q.", cfg.Default)
	return status
}

func configuredProviderNames(providers map[string]ProviderConfig) []string {
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	return names
}
