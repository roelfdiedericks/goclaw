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
	Web             WebToolsConfig        `json:"web"`
	Browser         BrowserToolsConfig    `json:"browser"`
	Exec            ExecToolsConfig       `json:"exec"`
	Subagent        SubagentToolsConfig   `json:"subagent"`
	XAIImagine      XAIImagineConfig      `json:"xaiImagine"`
	XAIVideo        XAIVideoConfig        `json:"xaiVideo"`
	DocumentExtract DocumentExtractConfig `json:"documentExtract"`
}

// DocumentExtractConfig contains document_extract tool settings.
type DocumentExtractConfig struct {
	Enabled bool `json:"enabled" default:"true"` // Enable the document_extract tool
}

// SubagentToolsConfig contains delegated subagent tool settings.
type SubagentToolsConfig struct {
	Enabled bool `json:"enabled" default:"true"` // Enable subagent_spawn/status/cancel/fanout tools
}

// WebToolsConfig contains web tool settings
type WebToolsConfig struct {
	BraveAPIKey string          `json:"braveApiKey"`
	Search      WebSearchConfig `json:"search"`
	UseBrowser  string          `json:"useBrowser" default:"auto"` // Browser fallback: "auto" (on 403/bot), "always", "never"
	Profile     string          `json:"profile" default:"default"` // Browser profile for web_fetch
	Headless    bool            `json:"headless" default:"true"`   // Run browser headless
}

// WebSearchConfig contains web_search provider and fallback settings.
type WebSearchConfig struct {
	Enabled             bool                     `json:"enabled" default:"true"`
	Provider            string                   `json:"provider" default:"auto"` // auto|grok|brave|perplexity|gemini
	FallbackProviders   []string                 `json:"fallbackProviders"`       // ordered provider fallback chain override
	MaxFallbackAttempts int                      `json:"maxFallbackAttempts" default:"3"`
	Retry               WebSearchRetryConfig     `json:"retry"`
	Providers           WebSearchProvidersConfig `json:"providers"`
}

// WebSearchRetryConfig controls retry behavior before falling back providers.
type WebSearchRetryConfig struct {
	Enabled                bool `json:"enabled" default:"true"`
	MaxAttemptsPerProvider int  `json:"maxAttemptsPerProvider" default:"2"`
	BaseBackoffMs          int  `json:"baseBackoffMs" default:"500"`
	MaxBackoffMs           int  `json:"maxBackoffMs" default:"5000"`
}

// WebSearchProvidersConfig holds provider-specific credentials and options.
type WebSearchProvidersConfig struct {
	Brave      WebSearchProviderConfig `json:"brave"`
	Grok       WebSearchProviderConfig `json:"grok"`
	Perplexity WebSearchProviderConfig `json:"perplexity"`
	Gemini     WebSearchProviderConfig `json:"gemini"`
}

// WebSearchProviderConfig holds per-provider config values.
type WebSearchProviderConfig struct {
	APIKey  string `json:"apiKey,omitempty"`
	BaseURL string `json:"baseUrl,omitempty"`
	Model   string `json:"model,omitempty"`
}

// BrowserToolsConfig contains browser tool settings
type BrowserToolsConfig struct {
	Enabled            bool                    `json:"enabled" default:"true"`                  // Enable headless browser tool
	Dir                string                  `json:"dir"`                                     // Browser data directory (empty = ~/.goclaw/browser)
	AutoDownload       bool                    `json:"autoDownload" default:"true"`             // Download Chromium if missing
	Revision           string                  `json:"revision"`                                // Chromium revision (empty = latest)
	Headless           bool                    `json:"headless" default:"true"`                 // Run browser in headless mode
	NoSandbox          bool                    `json:"noSandbox"`                               // Disable Chrome sandbox (needed for Docker/root)
	DefaultProfile     string                  `json:"defaultProfile" default:"default"`        // Default profile name
	Timeout            string                  `json:"timeout" default:"30s"`                   // Default action timeout
	Stealth            bool                    `json:"stealth" default:"true"`                  // Enable stealth mode
	Device             string                  `json:"device" default:"clear"`                  // Device emulation
	ProfileDomains     map[string]string       `json:"profileDomains"`                          // Domain → profile mapping (runtime default)
	ChromeCDP          string                  `json:"chromeCDP" default:"ws://localhost:9222"` // CDP endpoint for local chrome relay profile
	AllowAgentProfiles bool                    `json:"allowAgentProfiles"`                      // Allow explicit profile names in tool calls
	Remote             RemoteBrowserConfig     `json:"remote"`                                  // Remote browser access settings
	Advanced           AdvancedCDPConfig       `json:"advanced"`                                // Advanced CDP settings for later phases
	Bubblewrap         BrowserBubblewrapConfig `json:"bubblewrap"`                              // Sandbox settings
}

// RemoteBrowserConfig contains phase-1 remote browser access settings.
type RemoteBrowserConfig struct {
	Enabled              bool     `json:"enabled" default:"true"`            // Enable named remote browser profiles
	ProfilesText         string   `json:"profilesText"`                      // One entry per line: name=endpoint
	AllowedHosts         []string `json:"allowedHosts"`                      // Optional allowlist for remote hosts or CIDRs
	AllowDirectEndpoints bool     `json:"allowDirectEndpoints"`              // Allow raw endpoint strings in future phases
	AllowHTTPDiscovery   bool     `json:"allowHTTPDiscovery" default:"true"` // Allow http(s) discovery endpoints that resolve to websocket CDP
	ConnectionTimeout    string   `json:"connectionTimeout" default:"10s"`   // Timeout for remote endpoint connection/discovery
}

// AdvancedCDPConfig contains minimum CDP settings needed by later phases.
type AdvancedCDPConfig struct {
	NetworkCaptureEnabled bool   `json:"networkCaptureEnabled" default:"true"` // Enable network capture when implemented
	NetworkCaptureMax     int    `json:"networkCaptureMax" default:"200"`      // Max network entries to retain
	ConsoleCaptureEnabled bool   `json:"consoleCaptureEnabled" default:"true"` // Enable console capture when implemented
	ConsoleCaptureMax     int    `json:"consoleCaptureMax" default:"200"`      // Max console entries to retain
	TraceDir              string `json:"traceDir"`                             // Optional trace artifact directory override
	TraceRetention        int    `json:"traceRetention" default:"20"`          // Max trace artifacts to keep
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
				Title:     "Web Search",
				Collapsed: true,
				Fields: []forms.Field{
					{Name: "web.search.enabled", Title: "Enable web_search", Type: forms.Toggle, Default: true},
					{Name: "web.search.provider", Title: "Search Provider", Type: forms.Select, Default: "auto", Options: []forms.Option{
						{Label: "Auto", Value: "auto"},
						{Label: "Grok (xAI)", Value: "grok"},
						{Label: "Brave", Value: "brave"},
						{Label: "Perplexity", Value: "perplexity"},
						{Label: "Gemini", Value: "gemini"},
					}},
					{Name: "web.search.fallbackProviders", Title: "Fallback Providers", Type: forms.StringList, Placeholder: "grok, brave, perplexity, gemini"},
					{Name: "web.search.maxFallbackAttempts", Title: "Max Fallback Attempts", Type: forms.Number, Default: 3},
					{Name: "web.search.retry.enabled", Title: "Enable Retries", Type: forms.Toggle, Default: true},
					{Name: "web.search.retry.maxAttemptsPerProvider", Title: "Retries per Provider", Type: forms.Number, Default: 2},
					{Name: "web.search.retry.baseBackoffMs", Title: "Retry Base Backoff (ms)", Type: forms.Number, Default: 500},
					{Name: "web.search.retry.maxBackoffMs", Title: "Retry Max Backoff (ms)", Type: forms.Number, Default: 5000},
					{Name: "web.search.providers.brave.apiKey", Title: "Brave API Key", Type: forms.Secret},
					{Name: "web.search.providers.grok.apiKey", Title: "Grok API Key", Type: forms.Secret},
					{Name: "web.search.providers.grok.model", Title: "Grok Model", Type: forms.Text, Default: "grok-4-1-fast-reasoning"},
					{Name: "web.search.providers.perplexity.apiKey", Title: "Perplexity API Key", Type: forms.Secret},
					{Name: "web.search.providers.perplexity.baseUrl", Title: "Perplexity Base URL", Type: forms.Text},
					{Name: "web.search.providers.perplexity.model", Title: "Perplexity Model", Type: forms.Text},
					{Name: "web.search.providers.gemini.apiKey", Title: "Gemini API Key", Type: forms.Secret},
					{Name: "web.search.providers.gemini.model", Title: "Gemini Model", Type: forms.Text},
				},
			},
			{
				Title:     "Browser",
				Collapsed: true,
				Desc:      "General managed-browser settings for local GoClaw browser profiles.",
				Fields: []forms.Field{
					{Name: "browser.enabled", Title: "Enable Browser Tool", Type: forms.Toggle, Default: true},
					{Name: "browser.dir", Title: "Browser Data Directory", Type: forms.Text, Desc: "Empty uses ~/.goclaw/browser"},
					{Name: "browser.autoDownload", Title: "Auto-Download Chromium", Type: forms.Toggle, Default: true},
					{Name: "browser.revision", Title: "Chromium Revision", Type: forms.Text, Desc: "Leave empty for latest available revision"},
					{Name: "browser.headless", Title: "Headless by Default", Type: forms.Toggle, Default: true},
					{Name: "browser.noSandbox", Title: "Disable Chrome Sandbox", Type: forms.Toggle, Desc: "Only needed in restricted environments such as some containers"},
					{Name: "browser.defaultProfile", Title: "Default Local Profile", Type: forms.Text, Default: "default"},
					{Name: "browser.timeout", Title: "Default Action Timeout", Type: forms.Text, Default: "30s"},
					{Name: "browser.stealth", Title: "Enable Stealth Mode", Type: forms.Toggle, Default: true},
					{Name: "browser.device", Title: "Default Device Emulation", Type: forms.Text, Default: "clear", Desc: "Examples: clear, laptop, iphone-x"},
				},
			},
			{
				Title:     "Browser Relay",
				Collapsed: true,
				Desc:      "Settings for the local Chrome attach path used by profile='chrome'. This is your own local browser, not a remote machine.",
				Fields: []forms.Field{
					{Name: "browser.chromeCDP", Title: "Local Chrome Relay CDP Endpoint", Type: forms.Text, Default: "ws://localhost:9222", Desc: "Only used for profile='chrome'. Point this at the local Chrome relay/attach workflow on this machine."},
					{Name: "browser.allowAgentProfiles", Title: "Allow Agent To Choose Named Profiles", Type: forms.Toggle, Desc: "When enabled, tool calls can explicitly request named profiles such as work, twitter, or remote:workstation instead of relying only on default/domain-based profile selection."},
				},
			},
			{
				Title:     "Remote Browsers",
				Collapsed: true,
				Desc:      "Named Chrome/CDP endpoints running on other machines. These are separate from the local profile='chrome' relay path.",
				Fields: []forms.Field{
					{Name: "browser.remote.enabled", Title: "Enable Remote Browsers", Type: forms.Toggle, Default: true, Desc: "Enable support for named remote browser profiles. This does nothing by itself until you define remote profiles below."},
					{Name: "browser.remote.profilesText", Title: "Named Remote Browser Profiles", Type: forms.TextArea, Desc: "One per line: name=endpoint. Example: workstation=ws://192.168.1.100:9222/devtools/browser/abc123 or staging=http://192.168.1.1:9222"},
					{Name: "browser.remote.allowedHosts", Title: "Allowed Remote Hosts", Type: forms.StringList, Desc: "Optional safety allowlist for remote browser hosts or CIDRs. If set, only these hosts may be used for remote browser connections."},
					{Name: "browser.remote.allowDirectEndpoints", Title: "Allow Raw CDP URLs", Type: forms.Toggle, Desc: "Future-facing option. If enabled in later workflows, raw ws:// or http:// CDP endpoints could be used directly instead of only named remote profiles."},
					{Name: "browser.remote.allowHTTPDiscovery", Title: "Allow Simple http://host:port Browser Addresses", Type: forms.Toggle, Default: true, Desc: "If enabled, a remote profile can use a simple address like http://host:9222 and GoClaw will automatically discover the real websocket CDP URL via /json/version."},
					{Name: "browser.remote.connectionTimeout", Title: "Remote Browser Connection Timeout", Type: forms.Text, Default: "10s", Desc: "Maximum time to spend connecting to or discovering a configured remote browser endpoint."},
				},
			},
			{
				Title:     "Browser Diagnostics",
				Collapsed: true,
				Desc:      "Capture limits and artifact settings for console, network, and performance tooling.",
				Fields: []forms.Field{
					{Name: "browser.advanced.networkCaptureEnabled", Title: "Enable Network Capture", Type: forms.Toggle, Default: true, Desc: "Capture request, response, and failure events for observability actions"},
					{Name: "browser.advanced.networkCaptureMax", Title: "Network Capture Limit", Type: forms.Number, Default: 200, Min: 1, Max: 5000},
					{Name: "browser.advanced.consoleCaptureEnabled", Title: "Enable Console Capture", Type: forms.Toggle, Default: true, Desc: "Capture native console and exception events for observability actions"},
					{Name: "browser.advanced.consoleCaptureMax", Title: "Console Capture Limit", Type: forms.Number, Default: 200, Min: 1, Max: 5000},
					{Name: "browser.advanced.traceDir", Title: "Trace Artifact Directory", Type: forms.Text, Desc: "Optional override for performance trace output"},
					{Name: "browser.advanced.traceRetention", Title: "Trace Retention", Type: forms.Number, Default: 20, Min: 1, Max: 5000},
				},
			},
			{
				Title: "Delegated Subagents",
				Fields: []forms.Field{
					{Name: "subagent.enabled", Title: "Enable Subagent Tools", Type: forms.Toggle, Default: true, Desc: "Registers subagent_spawn, subagent_status, subagent_cancel, and subagent_fanout tools (owner sessions only). Requires gateway.delegatedRuns.enabled."},
				},
			},
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
				Title: "Document Extract",
				Desc:  "Convert uploaded documents (PDF, DOCX, PPTX, XLSX, EPUB, HTML, etc.) into LLM-ready markdown. OCR and embedded image descriptions route through the configured agent vision chain.",
				Fields: []forms.Field{
					{Name: "documentExtract.enabled", Title: "Enable document_extract", Type: forms.Toggle, Default: true, Desc: "Register the `document_extract` tool so agents can read uploaded documents."},
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
		"browser.enabled", cfg.Browser.Enabled,
		"xaiImagine.enabled", cfg.XAIImagine.Enabled,
		"xaiVideo.enabled", cfg.XAIVideo.Enabled,
	)
	bus.PublishEvent(configPath+".config.applied", cfg)

	return bus.CommandResult{
		Success: true,
		Message: "Config applied",
	}
}
