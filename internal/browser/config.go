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
	Enabled        bool
	Dir            string
	AutoDownload   bool
	Revision       string
	Headless       bool
	NoSandbox      bool
	DefaultProfile string
	Timeout        string
	Stealth        bool
	Device         string // Device emulation profile (friendly name)
	ProfileDomains map[string]string

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

// BrowserConfig holds browser configuration
type BrowserConfig struct {
	Dir                string            `json:"dir"`                // Browser data directory (empty = ~/.openclaw/goclaw/browser)
	AutoDownload       bool              `json:"autoDownload"`       // Download Chromium if missing
	Revision           string            `json:"revision"`           // Chromium revision (empty = latest)
	Headless           bool              `json:"headless"`           // Run in headless mode
	NoSandbox          bool              `json:"noSandbox"`          // Disable sandbox (needed for Docker/root)
	DefaultProfile     string            `json:"defaultProfile"`     // Default profile name
	Timeout            string            `json:"timeout"`            // Default action timeout (e.g., "30s")
	Stealth            bool              `json:"stealth"`            // Enable stealth mode
	Device             string            `json:"device"`             // Device emulation: "clear", "laptop", "iphone-x", etc.
	ProfileDomains     map[string]string `json:"profileDomains"`     // Domain → profile mapping
	ChromeCDP          string            `json:"chromeCDP"`          // CDP endpoint for profile="chrome" (default: ws://localhost:9222)
	AllowAgentProfiles bool              `json:"allowAgentProfiles"` // Allow agent to specify any profile (default: false, only "chrome" honored)

	// Bubblewrap sandboxing (set at runtime, not persisted to JSON)
	Workspace  string                  `json:"-"` // Workspace directory for sandbox
	Bubblewrap BrowserBubblewrapConfig `json:"-"` // Bubblewrap config
}

// DefaultBrowserConfig returns the default browser configuration
func DefaultBrowserConfig() BrowserConfig {
	return BrowserConfig{
		Dir:            "",      // Will resolve to ~/.openclaw/goclaw/browser
		AutoDownload:   true,
		Revision:       "",      // Latest
		Headless:       true,
		NoSandbox:      false,
		DefaultProfile: "default",
		Timeout:        "30s",
		Stealth:        true,
		Device:         "clear", // No viewport emulation, fills window
		ProfileDomains: map[string]string{},
	}
}

// ResolveDir returns the browser directory, defaulting to ~/.openclaw/goclaw/browser
func (c *BrowserConfig) ResolveDir(homeDir string) string {
	if c.Dir != "" {
		return c.Dir
	}
	return filepath.Join(homeDir, ".openclaw", "goclaw", "browser")
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
	switch strings.ToLower(c.Device) {
	case "", "clear":
		return devices.Clear
	case "laptop", "laptop-mdpi":
		return devices.LaptopWithMDPIScreen
	case "laptop-hidpi":
		return devices.LaptopWithHiDPIScreen
	case "laptop-touch":
		return devices.LaptopWithTouch
	case "iphone-x":
		return devices.IPhoneX
	case "iphone-8":
		return devices.IPhone6or7or8
	case "iphone-8-plus":
		return devices.IPhone6or7or8Plus
	case "iphone-se":
		return devices.IPhone5orSE
	case "iphone-4":
		return devices.IPhone4
	case "ipad":
		return devices.IPad
	case "ipad-mini":
		return devices.IPadMini
	case "ipad-pro":
		return devices.IPadPro
	case "pixel-2":
		return devices.Pixel2
	case "pixel-2-xl":
		return devices.Pixel2XL
	case "galaxy-s5":
		return devices.GalaxyS5
	case "galaxy-fold":
		return devices.GalaxyFold
	case "galaxy-note-3":
		return devices.GalaxyNote3
	case "nexus-5":
		return devices.Nexus5
	case "nexus-6":
		return devices.Nexus6
	case "nexus-7":
		return devices.Nexus7
	case "nexus-10":
		return devices.Nexus10
	case "moto-g4":
		return devices.MotoG4
	case "kindle-fire":
		return devices.KindleFireHDX
	case "surface-duo":
		return devices.SurfaceDuo
	default:
		// Unknown device, default to clear
		return devices.Clear
	}
}
