// Package config defines tool-specific configuration types.
// These types are defined here to avoid import cycles between config and tools packages.
package config

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// ToolsConfig contains tool-specific settings
type ToolsConfig struct {
	Web        WebToolsConfig     `json:"web"`
	Browser    BrowserToolsConfig `json:"browser"`
	Exec       ExecToolsConfig    `json:"exec"`
	XAIImagine XAIImagineConfig   `json:"xaiImagine"`
	XAIVideo   XAIVideoConfig     `json:"xaiVideo"`
}

// WebToolsConfig contains web tool settings
type WebToolsConfig struct {
	BraveAPIKey string `json:"braveApiKey"`
	UseBrowser  string `json:"useBrowser" default:"auto"` // Browser fallback: "auto" (on 403/bot), "always", "never"
	Profile     string `json:"profile" default:"default"` // Browser profile for web_fetch
	Headless    bool   `json:"headless" default:"true"`   // Run browser headless
}

// BrowserToolsConfig contains browser tool settings
type BrowserToolsConfig struct {
	Enabled        bool                    `json:"enabled" default:"true"`           // Enable headless browser tool
	Dir            string                  `json:"dir"`                              // Browser data directory (empty = ~/.goclaw/browser)
	AutoDownload   bool                    `json:"autoDownload" default:"true"`      // Download Chromium if missing
	Revision       string                  `json:"revision"`                         // Chromium revision (empty = latest)
	Headless       bool                    `json:"headless" default:"true"`          // Run browser in headless mode
	NoSandbox      bool                    `json:"noSandbox"`                        // Disable Chrome sandbox (needed for Docker/root)
	DefaultProfile string                  `json:"defaultProfile" default:"default"` // Default profile name
	Timeout        string                  `json:"timeout" default:"30s"`            // Default action timeout
	Stealth        bool                    `json:"stealth" default:"true"`           // Enable stealth mode
	Device         string                  `json:"device" default:"clear"`           // Device emulation
	ProfileDomains map[string]string       `json:"profileDomains"`                   // Domain → profile mapping (runtime default)
	Bubblewrap     BrowserBubblewrapConfig `json:"bubblewrap"`                       // Sandbox settings
}

// BrowserBubblewrapConfig contains bubblewrap settings for browser tool
type BrowserBubblewrapConfig struct {
	Enabled     bool     `json:"enabled" default:"true"` // Enable sandboxing
	ExtraRoBind []string `json:"extraRoBind"`            // Additional read-only bind mounts
	ExtraBind   []string `json:"extraBind"`              // Additional read-write bind mounts
	GPU         bool     `json:"gpu" default:"true"`     // Enable GPU acceleration
}

// ExecToolsConfig contains exec tool settings
type ExecToolsConfig struct {
	Timeout    int                  `json:"timeout" default:"1800"` // Timeout in seconds (0 = no timeout)
	Bubblewrap ExecBubblewrapConfig `json:"bubblewrap"`             // Sandbox settings
}

// ExecBubblewrapConfig contains bubblewrap settings for exec tool
type ExecBubblewrapConfig struct {
	Enabled      bool              `json:"enabled" default:"true"`      // Enable sandboxing
	ExtraRoBind  []string          `json:"extraRoBind"`                 // Additional read-only bind mounts
	ExtraBind    []string          `json:"extraBind"`                   // Additional read-write bind mounts
	ExtraEnv     map[string]string `json:"extraEnv"`                    // Additional environment variables
	AllowNetwork bool              `json:"allowNetwork" default:"true"` // Allow network access
	ClearEnv     bool              `json:"clearEnv" default:"true"`     // Clear environment before setting defaults
}

// XAIImagineConfig contains xAI image generation tool settings
type XAIImagineConfig struct {
	Enabled     bool   `json:"enabled"`                                // Enable the tool
	APIKey      string `json:"apiKey,omitempty"`                       // xAI API key (falls back to provider config)
	Model       string `json:"model,omitempty" default:"grok-2-image"` // Model to use
	Resolution  string `json:"resolution,omitempty" default:"1K"`      // Default resolution: "1K" or "2K"
	SaveToMedia bool   `json:"saveToMedia,omitempty" default:"true"`   // Save generated images to media store
}

// XAIVideoConfig contains xAI video generation tool settings
type XAIVideoConfig struct {
	Enabled      bool   `json:"enabled"`                                      // Enable the tool
	APIKey       string `json:"apiKey,omitempty"`                             // xAI API key
	Model        string `json:"model,omitempty" default:"grok-imagine-video"` // Model to use
	Resolution   string `json:"resolution,omitempty" default:"480p"`          // Default resolution: "480p" or "720p"
	Duration     int    `json:"duration,omitempty" default:"5"`               // Default video duration in seconds (1-15)
	SaveToMedia  bool   `json:"saveToMedia,omitempty" default:"true"`         // Save generated videos to media store
	PollInterval int    `json:"pollInterval,omitempty" default:"5"`           // Seconds between status polls
	Timeout      int    `json:"timeout,omitempty" default:"600"`              // Max wait time in seconds (10 min)
}

const configPath = "tools"

// ConfigFormDef returns the combined form definition for tool configurations.
func ConfigFormDef() forms.FormDef {
	return forms.FormDef{
		Title:       "Tools",
		Description: "Configure AI-powered tools for image and video generation",
		Sections: []forms.Section{
			{
				Title: "xAI Image Generation",
				Fields: []forms.Field{
					{Name: "xaiImagine.enabled", Title: "Enable Tool", Type: forms.Toggle},
					{Name: "xaiImagine.apiKey", Title: "xAI API Key", Type: forms.Secret},
					{Name: "xaiImagine.model", Title: "Model", Type: forms.Text, Default: "grok-2-image", Desc: "Default model for image generation"},
					{Name: "xaiImagine.resolution", Title: "Resolution", Type: forms.Select, Default: "1K", Options: []forms.Option{{Label: "1K", Value: "1K"}, {Label: "2K", Value: "2K"}}, Desc: "Default resolution"},
					{Name: "xaiImagine.saveToMedia", Title: "Save to Media", Type: forms.Toggle, Default: true, Desc: "Save generated images to media store"},
				},
			},
			{
				Title: "xAI Video Generation",
				Fields: []forms.Field{
					{Name: "xaiVideo.enabled", Title: "Enable Tool", Type: forms.Toggle},
					{Name: "xaiVideo.apiKey", Title: "xAI API Key", Type: forms.Secret},
					{Name: "xaiVideo.model", Title: "Model", Type: forms.Text, Default: "grok-imagine-video", Desc: "Default model for video generation"},
					{Name: "xaiVideo.resolution", Title: "Resolution", Type: forms.Select, Default: "480p", Options: []forms.Option{{Label: "480p", Value: "480p"}, {Label: "720p", Value: "720p"}}, Desc: "Default resolution"},
					{Name: "xaiVideo.duration", Title: "Duration (seconds)", Type: forms.Number, Default: 5, Desc: "Default video duration (1-15 seconds)"},
					{Name: "xaiVideo.saveToMedia", Title: "Save to Media", Type: forms.Toggle, Default: true, Desc: "Save generated videos to media store"},
					{Name: "xaiVideo.pollInterval", Title: "Poll Interval (seconds)", Type: forms.Number, Default: 5, Desc: "Seconds between status checks"},
					{Name: "xaiVideo.timeout", Title: "Timeout (seconds)", Type: forms.Number, Default: 600, Desc: "Maximum wait time for generation"},
				},
			},
		},
		Actions: []forms.ActionDef{
			{Name: "apply", Label: "Apply"},
		},
	}
}

// RegisterCommands registers config commands for tools.
func RegisterCommands() {
	bus.RegisterCommand(configPath, "apply", handleApply)
}

// UnregisterCommands unregisters config commands.
func UnregisterCommands() {
	bus.UnregisterCommand(configPath, "apply")
}

// handleApply publishes the config.applied event for listeners to react
func handleApply(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(ToolsConfig)
	if !ok {
		cfgPtr, okPtr := cmd.Payload.(*ToolsConfig)
		if okPtr {
			cfg = *cfgPtr
			ok = true
		}
	}
	if !ok {
		return bus.CommandResult{
			Error:   fmt.Errorf("expected ToolsConfig, got %T", cmd.Payload),
			Message: "invalid payload type",
		}
	}

	L_info("tools: config applied",
		"xaiImagine.enabled", cfg.XAIImagine.Enabled,
		"xaiVideo.enabled", cfg.XAIVideo.Enabled,
	)
	bus.PublishEvent(configPath+".config.applied", cfg)

	return bus.CommandResult{
		Success: true,
		Message: "Config applied",
	}
}
