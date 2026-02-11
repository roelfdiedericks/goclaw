//go:build !linux

package browser

import . "github.com/roelfdiedericks/goclaw/internal/logging"

// BrowserBubblewrapConfig holds bubblewrap settings for browser sandboxing
type BrowserBubblewrapConfig struct {
	Enabled     bool
	BwrapPath   string
	ExtraRoBind []string
	ExtraBind   []string
	GPU         bool
}

// IsSandboxAvailable returns false on non-Linux platforms.
func IsSandboxAvailable(bwrapPath string) bool {
	return false
}

// CreateSandboxedLauncher returns the original browser binary on non-Linux platforms.
func CreateSandboxedLauncher(browserBin, workspace, profileDir string, cfg BrowserBubblewrapConfig) (string, error) {
	if cfg.Enabled {
		L_warn("browser sandbox: bubblewrap only available on Linux, running unsandboxed")
	}
	return browserBin, nil
}

// CreatePassthroughLauncher returns the original browser binary on non-Linux platforms.
// The clean-environment wrapper is Linux-specific.
func CreatePassthroughLauncher(browserBin string) (string, error) {
	return browserBin, nil
}

// CleanupSandboxWrapper is a no-op on non-Linux platforms.
func CleanupSandboxWrapper() {}

// CheckBwrapForBrowser returns false on non-Linux platforms.
func CheckBwrapForBrowser(cfg BrowserBubblewrapConfig) bool {
	if cfg.Enabled {
		L_warn("browser sandbox: bubblewrap only available on Linux, running unsandboxed")
	}
	return false
}
