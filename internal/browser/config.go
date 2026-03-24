package browser

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/go-rod/rod/lib/devices"
)

// ConfigFromToolsConfig creates a browser.Config from the tools config structure.
// This allows the config package to remain independent of the browser package.
type ToolsConfigAdapter struct {
	Enabled            bool
	Dir                string
	AutoDownload       bool
	Revision           string
	Headless           bool
	NoSandbox          bool
	DefaultProfile     string
	Timeout            string
	Stealth            bool
	Device             string // Device emulation profile (friendly name)
	ProfileDomains     map[string]string
	ChromeCDP          string
	AllowAgentProfiles bool

	// Remote browser access
	RemoteEnabled              bool
	RemoteProfilesText         string
	RemoteAllowedHosts         []string
	RemoteAllowDirectEndpoints bool
	RemoteAllowHTTPDiscovery   bool
	RemoteConnectionTimeout    string

	// Minimum advanced CDP configuration for later phases
	AdvancedNetworkCaptureEnabled bool
	AdvancedNetworkCaptureMax     int
	AdvancedConsoleCaptureEnabled bool
	AdvancedConsoleCaptureMax     int
	AdvancedTraceDir              string
	AdvancedTraceRetention        int

	// Bubblewrap sandboxing
	Workspace         string   // Workspace directory for sandbox
	BubblewrapEnabled bool     // Enable bubblewrap sandboxing
	BubblewrapPath    string   // Path to bwrap binary (empty = search PATH)
	BubblewrapGPU     bool     // Allow GPU access in sandbox
	ExtraRoBind       []string // Extra read-only bind mounts
	ExtraBind         []string // Extra read-write bind mounts
}

// ToConfig converts the adapter to a BrowserConfig
func (a ToolsConfigAdapter) ToConfig() BrowserConfig {
	cfg := DefaultBrowserConfig()
	cfg.Dir = a.Dir
	cfg.AutoDownload = a.AutoDownload
	cfg.Revision = a.Revision
	cfg.Headless = a.Headless
	cfg.NoSandbox = a.NoSandbox
	if a.DefaultProfile != "" {
		cfg.DefaultProfile = a.DefaultProfile
	}
	if a.Timeout != "" {
		cfg.Timeout = a.Timeout
	}
	cfg.Stealth = a.Stealth
	if a.Device != "" {
		cfg.Device = a.Device
	}
	if a.ProfileDomains != nil {
		cfg.ProfileDomains = a.ProfileDomains
	}
	if a.ChromeCDP != "" {
		cfg.ChromeCDP = a.ChromeCDP
	}
	cfg.AllowAgentProfiles = a.AllowAgentProfiles
	cfg.Remote = RemoteBrowserConfig{
		Enabled:              a.RemoteEnabled,
		Profiles:             parseRemoteProfilesText(a.RemoteProfilesText),
		AllowedHosts:         append([]string(nil), a.RemoteAllowedHosts...),
		AllowDirectEndpoints: a.RemoteAllowDirectEndpoints,
		AllowHTTPDiscovery:   a.RemoteAllowHTTPDiscovery,
		ConnectionTimeout:    a.RemoteConnectionTimeout,
	}
	cfg.Advanced = AdvancedCDPConfig{
		NetworkCaptureEnabled: a.AdvancedNetworkCaptureEnabled,
		NetworkCaptureMax:     a.AdvancedNetworkCaptureMax,
		ConsoleCaptureEnabled: a.AdvancedConsoleCaptureEnabled,
		ConsoleCaptureMax:     a.AdvancedConsoleCaptureMax,
		TraceDir:              a.AdvancedTraceDir,
		TraceRetention:        a.AdvancedTraceRetention,
	}

	// Bubblewrap sandboxing
	cfg.Workspace = a.Workspace
	cfg.Bubblewrap = BrowserBubblewrapConfig{
		Enabled:     a.BubblewrapEnabled,
		BwrapPath:   a.BubblewrapPath,
		GPU:         a.BubblewrapGPU,
		ExtraRoBind: a.ExtraRoBind,
		ExtraBind:   a.ExtraBind,
	}

	return cfg
}

// RemoteBrowserProfileConfig defines a named remote CDP browser endpoint.
type RemoteBrowserProfileConfig struct {
	Endpoint string `json:"endpoint"`
}

// RemoteBrowserConfig holds phase-1 remote browser access settings.
type RemoteBrowserConfig struct {
	Enabled              bool                                  `json:"enabled"`
	Profiles             map[string]RemoteBrowserProfileConfig `json:"profiles"`
	AllowedHosts         []string                              `json:"allowedHosts"`
	AllowDirectEndpoints bool                                  `json:"allowDirectEndpoints"`
	AllowHTTPDiscovery   bool                                  `json:"allowHTTPDiscovery"`
	ConnectionTimeout    string                                `json:"connectionTimeout"`
}

// AdvancedCDPConfig holds minimum CDP feature settings used by later phases.
type AdvancedCDPConfig struct {
	NetworkCaptureEnabled bool   `json:"networkCaptureEnabled"`
	NetworkCaptureMax     int    `json:"networkCaptureMax"`
	ConsoleCaptureEnabled bool   `json:"consoleCaptureEnabled"`
	ConsoleCaptureMax     int    `json:"consoleCaptureMax"`
	TraceDir              string `json:"traceDir"`
	TraceRetention        int    `json:"traceRetention"`
}

// BrowserConfig holds browser configuration
type BrowserConfig struct {
	Dir                string              `json:"dir"`                // Browser data directory (empty = ~/.goclaw/browser)
	AutoDownload       bool                `json:"autoDownload"`       // Download Chromium if missing
	Revision           string              `json:"revision"`           // Chromium revision (empty = latest)
	Headless           bool                `json:"headless"`           // Run in headless mode
	NoSandbox          bool                `json:"noSandbox"`          // Disable sandbox (needed for Docker/root)
	DefaultProfile     string              `json:"defaultProfile"`     // Default profile name
	Timeout            string              `json:"timeout"`            // Default action timeout (e.g., "30s")
	Stealth            bool                `json:"stealth"`            // Enable stealth mode
	Device             string              `json:"device"`             // Device emulation: "clear", "laptop", "iphone-x", etc.
	ProfileDomains     map[string]string   `json:"profileDomains"`     // Domain → profile mapping
	ChromeCDP          string              `json:"chromeCDP"`          // CDP endpoint for profile="chrome" (default: ws://localhost:9222)
	AllowAgentProfiles bool                `json:"allowAgentProfiles"` // Allow agent to specify any profile (default: false, only "chrome" honored)
	Remote             RemoteBrowserConfig `json:"remote"`
	Advanced           AdvancedCDPConfig   `json:"advanced"`

	// Bubblewrap sandboxing (set at runtime, not persisted to JSON)
	Workspace  string                  `json:"-"` // Workspace directory for sandbox
	Bubblewrap BrowserBubblewrapConfig `json:"-"` // Bubblewrap config
}

// DefaultBrowserConfig returns the default browser configuration
func DefaultBrowserConfig() BrowserConfig {
	return BrowserConfig{
		Dir:            "", // Will resolve to ~/.goclaw/browser
		AutoDownload:   true,
		Revision:       "", // Latest
		Headless:       true,
		NoSandbox:      false,
		DefaultProfile: "default",
		Timeout:        "30s",
		Stealth:        true,
		Device:         "clear", // No viewport emulation, fills window
		ProfileDomains: map[string]string{},
		ChromeCDP:      "ws://localhost:9222",
		Remote: RemoteBrowserConfig{
			Enabled:            true,
			Profiles:           map[string]RemoteBrowserProfileConfig{},
			AllowHTTPDiscovery: true,
			ConnectionTimeout:  "10s",
		},
		Advanced: AdvancedCDPConfig{
			NetworkCaptureEnabled: true,
			NetworkCaptureMax:     200,
			ConsoleCaptureEnabled: true,
			ConsoleCaptureMax:     200,
			TraceRetention:        20,
		},
	}
}

// ResolveDir returns the browser directory, defaulting to ~/.goclaw/browser
func (c *BrowserConfig) ResolveDir(homeDir string) string {
	if c.Dir != "" {
		return c.Dir
	}
	return filepath.Join(homeDir, ".goclaw", "browser")
}

// ResolveBinDir returns the chromium binary directory
func (c *BrowserConfig) ResolveBinDir(homeDir string) string {
	return filepath.Join(c.ResolveDir(homeDir), "bin")
}

// ResolveProfilesDir returns the profiles directory
func (c *BrowserConfig) ResolveProfilesDir(homeDir string) string {
	return filepath.Join(c.ResolveDir(homeDir), "profiles")
}

// ResolveProfileDir returns the directory for a specific profile
func (c *BrowserConfig) ResolveProfileDir(homeDir, profile string) string {
	if profile == "" {
		profile = c.DefaultProfile
	}
	return filepath.Join(c.ResolveProfilesDir(homeDir), profile)
}

// ResolveTimeout returns the timeout as a Duration
func (c *BrowserConfig) ResolveTimeout() time.Duration {
	if c.Timeout == "" {
		return 30 * time.Second
	}
	d, err := time.ParseDuration(c.Timeout)
	if err != nil {
		return 30 * time.Second
	}
	return d
}

// ResolveRemoteConnectionTimeout returns the remote connection timeout as a Duration.
func (c *BrowserConfig) ResolveRemoteConnectionTimeout() time.Duration {
	if c.Remote.ConnectionTimeout == "" {
		return 10 * time.Second
	}
	d, err := time.ParseDuration(c.Remote.ConnectionTimeout)
	if err != nil {
		return 10 * time.Second
	}
	return d
}

// ResolveRemoteProfile returns a configured remote browser profile by name.
func (c *BrowserConfig) ResolveRemoteProfile(name string) (RemoteBrowserProfileConfig, bool) {
	if c.Remote.Profiles == nil {
		return RemoteBrowserProfileConfig{}, false
	}
	profile, ok := c.Remote.Profiles[name]
	return profile, ok
}

// ProfileForDomain returns the profile to use for a given domain.
// Matching order:
//  1. Exact match (e.g., "github.com")
//  2. Wildcard prefix match (e.g., "*.github.com" matches "api.github.com")
//  3. Global wildcard ("*")
//  4. DefaultProfile
func (c *BrowserConfig) ProfileForDomain(domain string) string {
	// 1. Exact match
	if profile, ok := c.ProfileDomains[domain]; ok {
		return profile
	}

	// 2. Wildcard prefix match (*.example.com)
	// Check each part of the domain for wildcard matches
	parts := strings.Split(domain, ".")
	for i := 1; i < len(parts); i++ {
		// Build wildcard pattern: *.github.com, *.com, etc.
		wildcardDomain := "*." + strings.Join(parts[i:], ".")
		if profile, ok := c.ProfileDomains[wildcardDomain]; ok {
			return profile
		}
	}

	// 3. Global wildcard
	if profile, ok := c.ProfileDomains["*"]; ok {
		return profile
	}

	// 4. Default profile
	return c.DefaultProfile
}

// IsRemoteProfile returns true when the profile name refers to a named remote browser.
func IsRemoteProfile(profile string) bool {
	return strings.HasPrefix(profile, "remote:")
}

// RemoteProfileName extracts the configured remote browser name from "remote:name".
func RemoteProfileName(profile string) string {
	return strings.TrimPrefix(profile, "remote:")
}

func parseRemoteProfilesText(text string) map[string]RemoteBrowserProfileConfig {
	profiles := map[string]RemoteBrowserProfileConfig{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, endpoint, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		endpoint = strings.TrimSpace(endpoint)
		if name == "" || endpoint == "" {
			continue
		}
		profiles[name] = RemoteBrowserProfileConfig{Endpoint: endpoint}
	}
	return profiles
}

// ResolveDevice returns the devices.Device for the configured device name.
// Supported friendly names:
//   - "clear" - No emulation, browser fills window (default)
//   - "laptop" or "laptop-mdpi" - LaptopWithMDPIScreen (1280x800)
//   - "laptop-hidpi" - LaptopWithHiDPIScreen (1440x900, 2x DPI)
//   - "laptop-touch" - LaptopWithTouch (1280x950)
//   - "iphone-x" - iPhoneX
//   - "iphone-8" - iPhone6or7or8
//   - "iphone-8-plus" - iPhone6or7or8Plus
//   - "iphone-se" - iPhone5orSE
//   - "ipad" - iPad
//   - "ipad-mini" - iPadMini
//   - "ipad-pro" - iPadPro
//   - "pixel-2" - Pixel2
//   - "pixel-2-xl" - Pixel2XL
//   - "galaxy-s5" - GalaxyS5
//   - "galaxy-fold" - GalaxyFold
//   - "nexus-5" - Nexus5
//   - "nexus-7" - Nexus7 (tablet)
//   - "nexus-10" - Nexus10 (tablet)
func (c *BrowserConfig) ResolveDevice() devices.Device {
	device, ok := resolveDeviceByName(c.Device)
	if !ok {
		return devices.Clear
	}
	return device
}

// ResolveDeviceStrict resolves a device name and reports whether it is known.
func ResolveDeviceStrict(name string) (devices.Device, bool) {
	return resolveDeviceByName(name)
}

func resolveDeviceByName(name string) (devices.Device, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "clear":
		return devices.Clear, true
	case "laptop", "laptop-mdpi":
		return devices.LaptopWithMDPIScreen, true
	case "laptop-hidpi":
		return devices.LaptopWithHiDPIScreen, true
	case "laptop-touch":
		return devices.LaptopWithTouch, true
	case "iphone-x":
		return devices.IPhoneX, true
	case "iphone-8":
		return devices.IPhone6or7or8, true
	case "iphone-8-plus":
		return devices.IPhone6or7or8Plus, true
	case "iphone-se":
		return devices.IPhone5orSE, true
	case "iphone-4":
		return devices.IPhone4, true
	case "ipad":
		return devices.IPad, true
	case "ipad-mini":
		return devices.IPadMini, true
	case "ipad-pro":
		return devices.IPadPro, true
	case "pixel-2":
		return devices.Pixel2, true
	case "pixel-2-xl":
		return devices.Pixel2XL, true
	case "galaxy-s5":
		return devices.GalaxyS5, true
	case "galaxy-fold":
		return devices.GalaxyFold, true
	case "galaxy-note-3":
		return devices.GalaxyNote3, true
	case "nexus-5":
		return devices.Nexus5, true
	case "nexus-6":
		return devices.Nexus6, true
	case "nexus-7":
		return devices.Nexus7, true
	case "nexus-10":
		return devices.Nexus10, true
	case "moto-g4":
		return devices.MotoG4, true
	case "kindle-fire":
		return devices.KindleFireHDX, true
	case "surface-duo":
		return devices.SurfaceDuo, true
	default:
		return devices.Clear, false
	}
}
