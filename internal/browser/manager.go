package browser

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// cleanupStaleLocks removes Chrome lock files left behind by crashed sessions
// Chrome refuses to start if SingletonLock or other lock files exist
func cleanupStaleLocks(profileDir string) {
	lockFiles := []string{
		"SingletonLock",
		"SingletonCookie",
		"SingletonSocket",
	}
	
	for _, lockFile := range lockFiles {
		lockPath := filepath.Join(profileDir, lockFile)
		if _, err := os.Stat(lockPath); err == nil {
			if err := os.Remove(lockPath); err != nil {
				L_warn("browser: failed to remove stale lock file", "file", lockPath, "error", err)
			} else {
				L_info("browser: removed stale lock file", "file", lockPath)
			}
		}
	}
}

// Manager is the singleton browser manager
// It handles browser download, profiles, and browser instance pooling
type Manager struct {
	config     BrowserConfig
	homeDir    string
	downloader *Downloader
	profiles   *ProfileManager
	
	// Browser instances per profile
	browsers   map[string]*rod.Browser
	browsersMu sync.Mutex
	
	// Initialization state
	initialized bool
	initOnce    sync.Once
	initErr     error
}

var (
	globalManager *Manager
	managerOnce   sync.Once
)

// GetManager returns the global browser manager singleton.
// Must call InitManager first.
func GetManager() *Manager {
	return globalManager
}

// InitManager initializes the global browser manager with the given config.
// This should be called once during application startup.
func InitManager(cfg BrowserConfig) (*Manager, error) {
	var err error
	managerOnce.Do(func() {
		homeDir, e := os.UserHomeDir()
		if e != nil {
			err = fmt.Errorf("failed to get home directory: %w", e)
			return
		}
		
		globalManager = &Manager{
			config:   cfg,
			homeDir:  homeDir,
			browsers: make(map[string]*rod.Browser),
		}
		
		// Initialize downloader and profile manager
		binDir := cfg.ResolveBinDir(homeDir)
		profilesDir := cfg.ResolveProfilesDir(homeDir)
		
		globalManager.downloader = NewDownloader(binDir, cfg.Revision)
		globalManager.profiles = NewProfileManager(profilesDir)
		
		L_debug("browser: manager initialized",
			"binDir", binDir,
			"profilesDir", profilesDir,
			"autoDownload", cfg.AutoDownload,
			"stealth", cfg.Stealth,
		)
	})
	
	if err != nil {
		return nil, err
	}
	
	return globalManager, nil
}

// IsInitialized returns true if the manager has been initialized
func (m *Manager) IsInitialized() bool {
	return m != nil && m.downloader != nil
}

// IsReady returns true if the browser is downloaded and ready to use
func (m *Manager) IsReady() bool {
	if m == nil || m.downloader == nil {
		return false
	}
	return m.downloader.IsDownloaded()
}

// EnsureReady ensures the browser is downloaded and ready.
// If autoDownload is enabled, this will download the browser if needed.
func (m *Manager) EnsureReady() error {
	if m == nil {
		return fmt.Errorf("browser manager not initialized")
	}
	
	if !m.config.AutoDownload {
		// Check if browser exists
		if _, err := m.downloader.FindExistingBrowser(); err != nil {
			return fmt.Errorf("browser not available and autoDownload is disabled: %w", err)
		}
		return nil
	}
	
	// Download if needed
	_, err := m.downloader.EnsureBrowser()
	return err
}

// EnsureBrowser downloads the browser if needed and returns the path
func (m *Manager) EnsureBrowser() (string, error) {
	if m == nil {
		return "", fmt.Errorf("browser manager not initialized")
	}
	return m.downloader.EnsureBrowser()
}

// ForceDownload forces a browser download/update
func (m *Manager) ForceDownload() (string, error) {
	if m == nil {
		return "", fmt.Errorf("browser manager not initialized")
	}
	return m.downloader.ForceDownload()
}

// GetBrowser returns a browser instance for the given profile.
// The browser is created lazily and reused for subsequent calls with the same profile.
// Special case: profile="chrome" connects to an existing Chrome via CDP endpoint.
func (m *Manager) GetBrowser(profile string) (*rod.Browser, error) {
	if m == nil {
		return nil, fmt.Errorf("browser manager not initialized")
	}
	
	if profile == "" {
		profile = m.config.DefaultProfile
	}
	
	m.browsersMu.Lock()
	defer m.browsersMu.Unlock()
	
	// Return existing browser if available
	if browser, ok := m.browsers[profile]; ok {
		// Check if browser is still connected
		// rod doesn't have a direct "IsConnected" method, so we try a simple operation
		// Wrap in func to recover from panic if CDP client is nil
		connected := func() (ok bool) {
			defer func() {
				if r := recover(); r != nil {
					L_debug("browser: connection check panicked, browser is dead", "profile", profile, "panic", r)
					ok = false
				}
			}()
			_, err := browser.Call(nil, "", "Browser.getVersion", nil)
			return err == nil
		}()
		
		if connected {
			return browser, nil
		}
		// Browser disconnected, remove and recreate
		L_debug("browser: existing browser disconnected, recreating", "profile", profile)
		delete(m.browsers, profile)
	}
	
	// Special case: profile="chrome" connects to existing Chrome via CDP
	if profile == "chrome" {
		browser, err := m.connectToChrome()
		if err != nil {
			return nil, err
		}
		m.browsers[profile] = browser
		return browser, nil
	}
	
	// Ensure browser is downloaded
	binPath, err := m.downloader.EnsureBrowser()
	if err != nil {
		return nil, fmt.Errorf("failed to ensure browser: %w", err)
	}
	
	// Ensure profile directory exists
	profileDir, err := m.profiles.EnsureProfile(profile)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure profile: %w", err)
	}
	
	// Clean up stale Chrome lock files from crashed sessions
	// Chrome refuses to start if these exist from a previous crash
	cleanupStaleLocks(profileDir)
	
	L_debug("browser: launching browser", "profile", profile, "profileDir", profileDir, "headless", m.config.Headless)
	
	// Create launcher
	l := launcher.New().
		Bin(binPath).
		UserDataDir(profileDir).
		Headless(m.config.Headless).
		Set("disable-dev-shm-usage") // For Docker/limited memory
	
	// Set window size for headed mode (otherwise Chrome uses a tiny default)
	// Use 1920x1080 to ensure sites show full desktop layout, not responsive/mobile
	if !m.config.Headless {
		l = l.Set("window-size", "1920,1080").
			Set("start-maximized")
	}
	
	// Stealth options
	if m.config.Stealth {
		l = l.Set("disable-blink-features", "AutomationControlled")
	}
	
	// No sandbox if configured (needed for Docker/root)
	if m.config.NoSandbox {
		l = l.Set("no-sandbox")
	}
	
	// Launch browser
	controlURL, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("failed to launch browser: %w", err)
	}
	
	browser := rod.New().ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		return nil, fmt.Errorf("failed to connect to browser: %w", err)
	}
	
	// Set device emulation based on config
	// Rod defaults to LaptopWithMDPIScreen which constrains the viewport
	browser.DefaultDevice(m.config.ResolveDevice())
	
	m.browsers[profile] = browser
	L_info("browser: launched", "profile", profile, "controlURL", controlURL)
	
	return browser, nil
}

// GetStealthPage creates a new stealth page for the given profile
func (m *Manager) GetStealthPage(profile string) (*rod.Page, error) {
	browser, err := m.GetBrowser(profile)
	if err != nil {
		return nil, err
	}
	
	if m.config.Stealth {
		return stealth.Page(browser)
	}
	
	return browser.Page(proto.TargetCreateTarget{})
}

// connectToChrome connects to an existing Chrome browser via CDP endpoint.
// This is used when profile="chrome" to connect to the user's native Chrome
// (typically with OpenClaw's browser extension installed).
func (m *Manager) connectToChrome() (*rod.Browser, error) {
	endpoint := m.config.ChromeCDP
	if endpoint == "" {
		endpoint = "ws://localhost:9222"
	}
	
	L_info("browser: connecting to Chrome", "endpoint", endpoint)
	
	browser := rod.New().ControlURL(endpoint)
	if err := browser.Connect(); err != nil {
		return nil, fmt.Errorf("failed to connect to Chrome at %s (is Chrome running with extension?): %w", endpoint, err)
	}
	
	L_info("browser: connected to Chrome", "endpoint", endpoint)
	return browser, nil
}

// IsExternalProfile returns true if the profile connects to an external browser
// that should not be closed by GoClaw (e.g., profile="chrome").
func (m *Manager) IsExternalProfile(profile string) bool {
	return profile == "chrome"
}

// CloseBrowser closes the browser for a specific profile.
// Does nothing for external profiles like "chrome" (user's browser).
func (m *Manager) CloseBrowser(profile string) {
	if m == nil {
		return
	}
	
	if profile == "" {
		profile = m.config.DefaultProfile
	}
	
	// Don't close external browsers (user's Chrome)
	if m.IsExternalProfile(profile) {
		L_debug("browser: skipping close for external profile", "profile", profile)
		return
	}
	
	m.browsersMu.Lock()
	defer m.browsersMu.Unlock()
	
	if browser, ok := m.browsers[profile]; ok {
		browser.Close()
		delete(m.browsers, profile)
		L_debug("browser: closed", "profile", profile)
	}
}

// CloseAll closes all browser instances (except external profiles like "chrome")
func (m *Manager) CloseAll() {
	if m == nil {
		return
	}
	
	m.browsersMu.Lock()
	defer m.browsersMu.Unlock()
	
	for profile, browser := range m.browsers {
		// Don't close external browsers (user's Chrome)
		if m.IsExternalProfile(profile) {
			L_debug("browser: skipping close for external profile", "profile", profile)
			continue
		}
		browser.Close()
		L_debug("browser: closed", "profile", profile)
		delete(m.browsers, profile)
	}
	L_info("browser: closed all managed instances")
}

// Profiles returns the profile manager
func (m *Manager) Profiles() *ProfileManager {
	if m == nil {
		return nil
	}
	return m.profiles
}

// Downloader returns the downloader
func (m *Manager) Downloader() *Downloader {
	if m == nil {
		return nil
	}
	return m.downloader
}

// Config returns the current configuration
func (m *Manager) Config() BrowserConfig {
	if m == nil {
		return DefaultBrowserConfig()
	}
	return m.config
}

// BrowserStatus contains status information for a running browser
type BrowserStatus struct {
	Profile     string `json:"profile"`
	Running     bool   `json:"running"`
	PageCount   int    `json:"pageCount"`
	ControlURL  string `json:"controlURL,omitempty"`
}

// Status returns the status of all browser instances
func (m *Manager) Status() []BrowserStatus {
	if m == nil {
		return nil
	}

	m.browsersMu.Lock()
	defer m.browsersMu.Unlock()

	var statuses []BrowserStatus
	for profile, browser := range m.browsers {
		status := BrowserStatus{
			Profile: profile,
			Running: true,
		}

		// Try to get page count
		pages, err := browser.Pages()
		if err == nil {
			status.PageCount = len(pages)
		}

		statuses = append(statuses, status)
	}

	return statuses
}

// RunningProfiles returns a list of profiles with running browsers
func (m *Manager) RunningProfiles() []string {
	if m == nil {
		return nil
	}

	m.browsersMu.Lock()
	defer m.browsersMu.Unlock()

	profiles := make([]string, 0, len(m.browsers))
	for profile := range m.browsers {
		profiles = append(profiles, profile)
	}
	return profiles
}

// HasRunningBrowsers returns true if any browsers are running
func (m *Manager) HasRunningBrowsers() bool {
	if m == nil {
		return false
	}

	m.browsersMu.Lock()
	defer m.browsersMu.Unlock()

	return len(m.browsers) > 0
}

// ProfileForDomain returns the profile to use for a given domain
func (m *Manager) ProfileForDomain(domain string) string {
	if m == nil {
		return "default"
	}
	return m.config.ProfileForDomain(domain)
}

// LaunchHeaded launches a headed (non-headless) browser for interactive setup.
// This is used by the CLI for profile setup.
func (m *Manager) LaunchHeaded(profile string, startURL string) (*rod.Browser, *rod.Page, error) {
	if m == nil {
		return nil, nil, fmt.Errorf("browser manager not initialized")
	}
	
	if profile == "" {
		profile = m.config.DefaultProfile
	}
	
	// Ensure browser is downloaded
	binPath, err := m.downloader.EnsureBrowser()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to ensure browser: %w", err)
	}
	
	// Ensure profile directory exists
	profileDir, err := m.profiles.EnsureProfile(profile)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to ensure profile: %w", err)
	}
	
	// Clean up stale Chrome lock files from crashed sessions
	cleanupStaleLocks(profileDir)
	
	L_info("browser: launching headed browser for setup", "profile", profile, "startURL", startURL)
	
	// Create launcher - NOT headless
	l := launcher.New().
		Bin(binPath).
		UserDataDir(profileDir).
		Headless(false). // Headed mode for interactive setup
		Set("disable-dev-shm-usage").
		Set("window-size", "1920,1080").
		Set("start-maximized")
	
	if m.config.Stealth {
		l = l.Set("disable-blink-features", "AutomationControlled")
	}
	
	if m.config.NoSandbox {
		l = l.Set("no-sandbox")
	}
	
	// Launch browser
	controlURL, err := l.Launch()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to launch headed browser: %w", err)
	}
	
	browser := rod.New().ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		return nil, nil, fmt.Errorf("failed to connect to headed browser: %w", err)
	}
	
	// Set device emulation based on config (default "clear" fills window)
	// Rod defaults to LaptopWithMDPIScreen which constrains the viewport
	browser.DefaultDevice(m.config.ResolveDevice())
	
	// Create a page
	var page *rod.Page
	if m.config.Stealth {
		page, err = stealth.Page(browser)
	} else {
		page, err = browser.Page(proto.TargetCreateTarget{})
	}
	if err != nil {
		browser.Close()
		return nil, nil, fmt.Errorf("failed to create page: %w", err)
	}
	
	// Navigate to start URL if provided
	if startURL != "" {
		if err := page.Navigate(startURL); err != nil {
			L_warn("browser: failed to navigate to start URL", "url", startURL, "error", err)
			// Continue anyway - user can navigate manually
		}
	}
	
	return browser, page, nil
}
