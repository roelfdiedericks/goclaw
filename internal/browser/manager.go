package browser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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

// createLauncher creates a configured Chrome launcher
// Always uses a wrapper script for consistent environment:
// - bubblewrap enabled: bwrap sandbox wrapper
// - bubblewrap disabled: clean-environment passthrough wrapper
func (m *Manager) createLauncher(binPath, profileDir string, headless bool) (*launcher.Launcher, error) {
	var actualBinPath string
	var err error
	
	if m.config.Bubblewrap.Enabled {
		// Create bubblewrap sandbox wrapper
		actualBinPath, err = CreateSandboxedLauncher(binPath, m.config.Workspace, profileDir, m.config.Bubblewrap)
		if err != nil {
			L_warn("browser: failed to create sandbox wrapper, falling back to passthrough", "error", err)
			actualBinPath, err = CreatePassthroughLauncher(binPath)
			if err != nil {
				return nil, fmt.Errorf("failed to create browser wrapper: %w", err)
			}
			L_info("browser: using passthrough wrapper (bwrap failed)")
		} else {
			L_info("browser: using bubblewrap sandbox", "wrapper", actualBinPath)
		}
	} else {
		// Create clean-environment passthrough wrapper (same env as bwrap would provide)
		actualBinPath, err = CreatePassthroughLauncher(binPath)
		if err != nil {
			return nil, fmt.Errorf("failed to create browser wrapper: %w", err)
		}
		L_info("browser: using passthrough wrapper (clean environment)")
	}
	
	l := launcher.New().
		Bin(actualBinPath).
		UserDataDir(profileDir).
		Headless(headless).
		Set("disable-dev-shm-usage") // For Docker/limited memory
	
	// Set window size for headed mode (otherwise Chrome uses a tiny default)
	if !headless {
		l = l.Set("window-size", "1920,1080").
			Set("start-maximized")
	}
	
	if m.config.Stealth {
		l = l.Set("disable-blink-features", "AutomationControlled")
	}
	
	// Note: We no longer add --no-sandbox. The passthrough wrapper provides
	// a clean environment, and Chrome can use its native sandbox.
	
	return l, nil
}

// launchWithRetry launches Chrome with retry on SingletonLock error
func (m *Manager) launchWithRetry(binPath, profileDir string, headless bool) (string, error) {
	l, err := m.createLauncher(binPath, profileDir, headless)
	if err != nil {
		return "", err
	}
	
	controlURL, err := l.Launch()
	if err != nil {
		// Check for SingletonLock error and retry once after cleanup
		if strings.Contains(err.Error(), "SingletonLock") || strings.Contains(err.Error(), "ProcessSingleton") {
			L_warn("browser: SingletonLock error, cleaning up and retrying", "error", err)
			cleanupStaleLocks(profileDir)
			time.Sleep(500 * time.Millisecond)
			
			l, err = m.createLauncher(binPath, profileDir, headless)
			if err != nil {
				return "", err
			}
			controlURL, err = l.Launch()
		}
	}
	
	return controlURL, err
}

// Manager is the singleton browser manager
// It handles browser download, profiles, and browser instance pooling

// browserInstance tracks a browser and its state
type browserInstance struct {
	browser   *rod.Browser
	headed    bool
	profile   string
	createdAt time.Time
}

type Manager struct {
	config     BrowserConfig
	homeDir    string
	downloader *Downloader
	profiles   *ProfileManager
	
	// Browser instances per profile
	browsers   map[string]*browserInstance
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
			browsers: make(map[string]*browserInstance),
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

// GetBrowser returns a browser instance for the given profile and mode.
// One browser per profile - mode switching rules:
//   - Headed exists + headed requested → return headed
//   - Headed exists + headless requested → return headed anyway (headed wins, never auto-close)
//   - Headless exists + headed requested → close headless, open headed (safe - headless is invisible)
//   - Headless exists + headless requested → return headless
//   - Nothing exists → create what was requested
//
// Special case: profile="chrome" connects to an existing Chrome via CDP endpoint.
func (m *Manager) GetBrowser(profile string, headed bool) (*rod.Browser, error) {
	if m == nil {
		return nil, fmt.Errorf("browser manager not initialized")
	}
	
	if profile == "" {
		profile = m.config.DefaultProfile
	}
	
	m.browsersMu.Lock()
	defer m.browsersMu.Unlock()
	
	// Check for existing browser
	if instance, ok := m.browsers[profile]; ok {
		// Headed browser always wins - never close it automatically
		if instance.headed {
			L_debug("browser: returning existing headed browser", "profile", profile, "requested_headed", headed)
			return instance.browser, nil
		}
		
		// Existing is headless
		if !headed {
			// Requested headless, have headless - return it
			L_debug("browser: returning existing headless browser", "profile", profile)
			return instance.browser, nil
		}
		
		// Requested headed, have headless - safe to close and upgrade
		L_debug("browser: upgrading headless to headed", "profile", profile)
		instance.browser.Close()
		delete(m.browsers, profile)
	}
	
	// Special case: profile="chrome" connects to existing Chrome via CDP
	if profile == "chrome" {
		browser, err := m.connectToChrome()
		if err != nil {
			return nil, err
		}
		m.browsers[profile] = &browserInstance{
			browser:   browser,
			headed:    true,
			profile:   profile,
			createdAt: time.Now(),
		}
		// Monitor for disconnect
		go m.monitorBrowser(profile, browser)
		return browser, nil
	}
	
	// Launch new browser with requested mode
	browser, err := m.launchBrowser(profile, headed)
	if err != nil {
		return nil, err
	}
	
	m.browsers[profile] = &browserInstance{
		browser:   browser,
		headed:    headed,
		profile:   profile,
		createdAt: time.Now(),
	}
	
	// Monitor for browser death - automatically clean up when WebSocket dies
	go m.monitorBrowser(profile, browser)
	
	mode := "headless"
	if headed {
		mode = "headed"
	}
	L_info("browser: launched", "profile", profile, "mode", mode)
	
	return browser, nil
}

// launchBrowser creates and connects to a new browser instance
func (m *Manager) launchBrowser(profile string, headed bool) (*rod.Browser, error) {
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
	cleanupStaleLocks(profileDir)
	
	mode := "headless"
	if headed {
		mode = "headed"
	}
	L_debug("browser: launching", "profile", profile, "mode", mode, "profileDir", profileDir)
	
	controlURL, err := m.launchWithRetry(binPath, profileDir, !headed)
	if err != nil {
		return nil, fmt.Errorf("failed to launch browser: %w", err)
	}
	
	browser := rod.New().ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		return nil, fmt.Errorf("failed to connect to browser: %w", err)
	}
	
	// Set device emulation based on config
	browser.DefaultDevice(m.config.ResolveDevice())
	
	return browser, nil
}

// monitorBrowser watches for browser disconnect and removes from pool
func (m *Manager) monitorBrowser(profile string, browser *rod.Browser) {
	<-browser.GetContext().Done()
	
	m.browsersMu.Lock()
	defer m.browsersMu.Unlock()
	
	// Only delete if this is still the same browser (not replaced)
	if instance, ok := m.browsers[profile]; ok && instance.browser == browser {
		delete(m.browsers, profile)
		L_info("browser: removed dead browser from pool", "profile", profile, "headed", instance.headed)
	}
}

// GetStealthPage creates a new stealth page for the given profile and mode.
// If stealth mode is enabled in config, uses stealth.Page for anti-detection.
func (m *Manager) GetStealthPage(profile string, headed bool) (*rod.Page, error) {
	browser, err := m.GetBrowser(profile, headed)
	if err != nil {
		return nil, err
	}
	
	if m.config.Stealth {
		return stealth.Page(browser)
	}
	
	return browser.Page(proto.TargetCreateTarget{})
}

// GetBackgroundStealthPage creates a new stealth page in the background (doesn't steal focus).
// Use this for background operations like web_fetch that shouldn't disturb the user.
func (m *Manager) GetBackgroundStealthPage(profile string, headed bool) (*rod.Page, error) {
	browser, err := m.GetBrowser(profile, headed)
	if err != nil {
		return nil, err
	}
	
	// Create page in background - won't steal focus from user's active tab
	page, err := browser.Page(proto.TargetCreateTarget{Background: true})
	if err != nil {
		return nil, err
	}
	
	// Apply stealth JS if configured (same as stealth.Page does)
	if m.config.Stealth {
		if _, err := page.EvalOnNewDocument(stealth.JS); err != nil {
			page.Close()
			return nil, err
		}
	}
	
	return page, nil
}

// GetPage creates a new page on the browser for the given profile and mode.
func (m *Manager) GetPage(profile string, headed bool) (*rod.Page, error) {
	browser, err := m.GetBrowser(profile, headed)
	if err != nil {
		return nil, err
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

// CloseBrowser explicitly closes the browser for a specific profile.
// Does nothing for external profiles like "chrome" (user's browser).
// Returns error if manager not initialized.
func (m *Manager) CloseBrowser(profile string) error {
	if m == nil {
		return fmt.Errorf("browser manager not initialized")
	}
	
	if profile == "" {
		profile = m.config.DefaultProfile
	}
	
	// Don't close external browsers (user's Chrome)
	if m.IsExternalProfile(profile) {
		L_debug("browser: skipping close for external profile", "profile", profile)
		return nil
	}
	
	m.browsersMu.Lock()
	defer m.browsersMu.Unlock()
	
	if instance, ok := m.browsers[profile]; ok {
		instance.browser.Close()
		delete(m.browsers, profile)
		L_debug("browser: closed", "profile", profile, "headed", instance.headed)
	}
	return nil
}

// CloseAll closes all browser instances (except external profiles like "chrome")
func (m *Manager) CloseAll() {
	if m == nil {
		return
	}
	
	m.browsersMu.Lock()
	defer m.browsersMu.Unlock()
	
	for profile, instance := range m.browsers {
		// Don't close external browsers (user's Chrome)
		if m.IsExternalProfile(profile) {
			L_debug("browser: skipping close for external profile", "profile", profile)
			continue
		}
		instance.browser.Close()
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
	for profile, instance := range m.browsers {
		status := BrowserStatus{
			Profile: profile,
			Running: true,
		}

		// Try to get page count
		pages, err := instance.browser.Pages()
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

// HasBrowser checks if a browser exists for the given profile.
// Returns whether a browser exists and whether it's headed.
func (m *Manager) HasBrowser(profile string) (exists bool, headed bool) {
	if m == nil {
		return false, false
	}
	
	if profile == "" {
		profile = m.config.DefaultProfile
	}
	
	m.browsersMu.Lock()
	defer m.browsersMu.Unlock()
	
	if instance, ok := m.browsers[profile]; ok {
		return true, instance.headed
	}
	return false, false
}
