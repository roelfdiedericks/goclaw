package hass

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// HomeAssistantConfig configures Home Assistant integration (REST + WebSocket)
type HomeAssistantConfig struct {
	Enabled          bool   `json:"enabled"`                    // Enable Home Assistant integration
	URL              string `json:"url"`                        // HA base URL (e.g., "https://home.example.com:8123")
	Token            string `json:"token"`                      // Long-lived access token
	Insecure         bool   `json:"insecure,omitempty"`         // Skip TLS verification for self-signed certs
	Timeout          string `json:"timeout,omitempty"`          // Request timeout (default: "10s")
	EventTimeout     string `json:"eventTimeout,omitempty"`     // Agent invocation timeout for wake events (default: "120s")
	EventPrefix      string `json:"eventPrefix,omitempty"`      // Prefix for injected events (default: "[HomeAssistant Event]")
	SubscriptionFile string `json:"subscriptionFile,omitempty"` // Subscription persistence file (default: "hass-subscriptions.json")
	ReconnectDelay   string `json:"reconnectDelay,omitempty"`   // WebSocket reconnect delay (default: "5s")
}

// HConfig is an alias for HomeAssistantConfig for convenience
// (Named HomeAssistantConfig rather than Config for clarity when embedded in main Config struct)
type HConfig = HomeAssistantConfig

const configPath = "homeassistant"

// ConfigFormDef returns the form definition for HomeAssistantConfig
func ConfigFormDef(cfg HConfig) forms.FormDef {
	return forms.FormDef{
		Title:       "Home Assistant",
		Description: "Configure Home Assistant integration",
		Sections: []forms.Section{
			{
				Title: "Connection",
				Fields: []forms.Field{
					{
						Name:  "enabled",
						Title: "Enable Integration",
						Desc:  "Enable Home Assistant connection",
						Type:  forms.Toggle,
					},
					{
						Name:  "url",
						Title: "URL",
						Desc:  "Home Assistant base URL (e.g., https://home.example.com:8123)",
						Type:  forms.Text,
					},
					{
						Name:  "token",
						Title: "Access Token",
						Desc:  "Long-lived access token from Home Assistant",
						Type:  forms.Secret,
					},
					{
						Name:  "insecure",
						Title: "Skip TLS Verification",
						Desc:  "Allow self-signed certificates (not recommended)",
						Type:  forms.Toggle,
					},
				},
			},
			{
				Title:     "Advanced",
				Collapsed: true,
				Fields: []forms.Field{
					{
						Name:    "timeout",
						Title:   "Request Timeout",
						Desc:    "HTTP request timeout (e.g., 10s)",
						Type:    forms.Text,
						Default: "10s",
					},
					{
						Name:    "eventPrefix",
						Title:   "Event Prefix",
						Desc:    "Prefix for injected event messages",
						Type:    forms.Text,
						Default: "[HomeAssistant Event]",
					},
					{
						Name:    "subscriptionFile",
						Title:   "Subscription File",
						Desc:    "File to persist event subscriptions",
						Type:    forms.Text,
						Default: "hass-subscriptions.json",
					},
					{
						Name:    "reconnectDelay",
						Title:   "Reconnect Delay",
						Desc:    "Delay before WebSocket reconnect attempts",
						Type:    forms.Text,
						Default: "5s",
					},
				},
			},
		},
		Actions: []forms.ActionDef{
			{
				Name:  "test",
				Label: "Test Connection",
				Desc:  "Verify connectivity to Home Assistant",
			},
			{
				Name:  "apply",
				Label: "Apply Now",
				Desc:  "Apply changes to running service",
			},
		},
	}
}

// ValidateConfig validates a HomeAssistantConfig
func ValidateConfig(cfg HConfig) error {
	if cfg.Enabled {
		if cfg.URL == "" {
			return fmt.Errorf("URL is required when enabled")
		}
		if cfg.Token == "" {
			return fmt.Errorf("token is required when enabled")
		}
	}
	return nil
}

// DefaultHConfig returns a HomeAssistantConfig with default values
func DefaultHConfig() HConfig {
	return HConfig{
		Enabled:          false,
		Timeout:          "10s",
		EventPrefix:      "[HomeAssistant Event]",
		SubscriptionFile: "hass-subscriptions.json",
		ReconnectDelay:   "5s",
	}
}

// RegisterCommands registers config commands for hass.
func RegisterCommands() {
	bus.RegisterCommand(configPath, "apply", handleApply)
}

// UnregisterCommands unregisters config commands.
func UnregisterCommands() {
	bus.UnregisterCommand(configPath, "apply")
}

// handleApply validates config and publishes event for manager to apply.
func handleApply(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(HomeAssistantConfig)
	if !ok {
		cfgPtr, okPtr := cmd.Payload.(*HomeAssistantConfig)
		if okPtr {
			cfg = *cfgPtr
			ok = true
		}
	}
	if !ok {
		return bus.CommandResult{
			Error:   fmt.Errorf("expected HomeAssistantConfig payload, got %T", cmd.Payload),
			Message: "invalid payload type",
		}
	}

	// Validate config before applying
	if err := ValidateConfig(cfg); err != nil {
		return bus.CommandResult{
			Error:   err,
			Message: fmt.Sprintf("config validation failed: %v", err),
		}
	}

	L_info("hass: config applied", "enabled", cfg.Enabled, "url", cfg.URL)
	bus.PublishEvent(configPath+".config.applied", cfg)

	return bus.CommandResult{
		Success: true,
		Message: "Config applied - manager will reload",
	}
}
