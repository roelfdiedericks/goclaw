// Package config defines tool-specific configuration types.
// These types are defined here to avoid import cycles between config and tools packages.
package config

// ToolsConfig contains tool-specific settings
type ToolsConfig struct {
	Web        WebToolsConfig     `json:"web"`
	Browser    BrowserToolsConfig `json:"browser"`
	Exec       ExecToolsConfig    `json:"exec"`
	XAIImagine XAIImagineConfig   `json:"xaiImagine"`
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
