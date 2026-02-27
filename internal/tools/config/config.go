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
	UseBrowser  string `json:"useBrowser"` // Browser fallback: "auto" (on 403/bot), "always", "never" (default: "auto")
	Profile     string `json:"profile"`    // Browser profile for web_fetch (default: "default")
	Headless    *bool  `json:"headless"`   // Run browser headless (default: true, set false for debugging)
}

// BrowserToolsConfig contains browser tool settings
type BrowserToolsConfig struct {
	Enabled        bool                    `json:"enabled"`        // Enable headless browser tool (requires Chrome/Chromium)
	Dir            string                  `json:"dir"`            // Browser data directory (empty = ~/.goclaw/browser)
	AutoDownload   bool                    `json:"autoDownload"`   // Download Chromium if missing (default: true)
	Revision       string                  `json:"revision"`       // Chromium revision (empty = latest)
	Headless       bool                    `json:"headless"`       // Run browser in headless mode (default: true)
	NoSandbox      bool                    `json:"noSandbox"`      // Disable Chrome sandbox (needed for Docker/root)
	DefaultProfile string                  `json:"defaultProfile"` // Default profile name (default: "default")
	Timeout        string                  `json:"timeout"`        // Default action timeout (default: "30s")
	Stealth        bool                    `json:"stealth"`        // Enable stealth mode (default: true)
	Device         string                  `json:"device"`         // Device emulation: "clear", "laptop", "iphone-x", etc. (default: "clear")
	ProfileDomains map[string]string       `json:"profileDomains"` // Domain → profile mapping for auto-selection
	Bubblewrap     BrowserBubblewrapConfig `json:"bubblewrap"`     // Sandbox settings
}

// BrowserBubblewrapConfig contains bubblewrap settings for browser tool
type BrowserBubblewrapConfig struct {
	Enabled     bool     `json:"enabled"`     // Enable sandboxing (default: false)
	ExtraRoBind []string `json:"extraRoBind"` // Additional read-only bind mounts
	ExtraBind   []string `json:"extraBind"`   // Additional read-write bind mounts
	GPU         bool     `json:"gpu"`         // Enable GPU acceleration (default: true)
}

// ExecToolsConfig contains exec tool settings
type ExecToolsConfig struct {
	Timeout    int                  `json:"timeout"`    // Timeout in seconds (default: 1800 = 30 min, 0 = no timeout)
	Bubblewrap ExecBubblewrapConfig `json:"bubblewrap"` // Sandbox settings
}

// ExecBubblewrapConfig contains bubblewrap settings for exec tool
type ExecBubblewrapConfig struct {
	Enabled      bool              `json:"enabled"`      // Enable sandboxing (default: false)
	ExtraRoBind  []string          `json:"extraRoBind"`  // Additional read-only bind mounts
	ExtraBind    []string          `json:"extraBind"`    // Additional read-write bind mounts
	ExtraEnv     map[string]string `json:"extraEnv"`     // Additional environment variables
	AllowNetwork bool              `json:"allowNetwork"` // Allow network access (default: true)
	ClearEnv     bool              `json:"clearEnv"`     // Clear environment before setting defaults (default: true)
}

// XAIImagineConfig contains xAI image generation tool settings
type XAIImagineConfig struct {
	Enabled     bool   `json:"enabled"`               // Enable the tool (default: false)
	APIKey      string `json:"apiKey,omitempty"`      // xAI API key (falls back to provider config)
	Model       string `json:"model,omitempty"`       // Model to use (default: grok-2-image)
	Resolution  string `json:"resolution,omitempty"`  // Default resolution: "1K" (~1024px) or "2K" (~2048px)
	SaveToMedia bool   `json:"saveToMedia,omitempty"` // Save generated images to media store (default: true)
}
