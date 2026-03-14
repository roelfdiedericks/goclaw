//go:build linux || darwin

package browser

import (
	"os"
	"path/filepath"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/paths"
	"github.com/roelfdiedericks/goclaw/internal/sandbox"
	sbruntime "github.com/roelfdiedericks/goclaw/internal/sandbox/runtime"
)

// BrowserBubblewrapConfig holds bubblewrap settings for browser sandboxing
type BrowserBubblewrapConfig struct {
	Enabled     bool
	BwrapPath   string
	ExtraRoBind []string
	ExtraBind   []string
	GPU         bool
}

// IsSandboxAvailable returns true if bubblewrap sandboxing is available for the browser.
func IsSandboxAvailable(bwrapPath string) bool {
	return sbruntime.BrowserSandboxAvailable(bwrapPath)
}

// CreateSandboxedLauncher creates a wrapper script that launches the browser through bwrap.
// This is needed because go-rod's launcher expects a single executable path.
// Returns the path to the wrapper script.
//
// NOTE: This is a basic implementation. For production use, consider:
// - Using a proper temp directory management
// - Cleaning up wrapper scripts on shutdown
// - Handling signal forwarding properly
func CreateSandboxedLauncher(browserBin, workspace, profileDir string, cfg BrowserBubblewrapConfig) (string, error) {
	if !cfg.Enabled {
		return browserBin, nil
	}

	home, _ := os.UserHomeDir()
	mgr := sandbox.GetManager()
	return sbruntime.CreateBrowserLauncher(browserBin, sbruntime.BrowserLaunchOptions{
		BackendPath:   cfg.BwrapPath,
		WorkspaceDir:  workspace,
		ProfileDir:    profileDir,
		HomeDir:       preferredBrowserHome(mgr, home),
		ProtectedDirs: mgr.GetProtectedDirs(),
		AllowGPU:      cfg.GPU,
		ClearEnv:      true,
		ExtraRoBind:   cfg.ExtraRoBind,
		ExtraBind:     cfg.ExtraBind,
	})
}

// CreatePassthroughLauncher creates a wrapper script that launches the browser with a clean
// environment matching what bubblewrap would provide, but without the actual sandboxing.
// This ensures consistent behavior regardless of whether bubblewrap is enabled.
func CreatePassthroughLauncher(browserBin string) (string, error) {
	wrapperDir, err := paths.DataPath("browser-sandbox")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(wrapperDir, 0750); err != nil {
		return "", err
	}

	home, _ := os.UserHomeDir()
	wrapperPath := filepath.Join(wrapperDir, "chromium-wrapper.sh")

	// Build environment variables matching bwrap's DefaultEnv + Display + Wayland
	// See bwrap.DefaultEnv(), bwrap.Display(), bwrap.Wayland()
	envVars := []string{}

	// PATH - preserve from host
	if path := os.Getenv("PATH"); path != "" {
		envVars = append(envVars, "PATH="+sbruntime.ShellQuote(path))
	}

	// HOME
	envVars = append(envVars, "HOME="+sbruntime.ShellQuote(home))

	// TERM
	envVars = append(envVars, "TERM='xterm'")

	// LANG - preserve from host or default
	if lang := os.Getenv("LANG"); lang != "" {
		envVars = append(envVars, "LANG="+sbruntime.ShellQuote(lang))
	} else {
		envVars = append(envVars, "LANG='C.UTF-8'")
	}

	// USER - preserve from host
	if user := os.Getenv("USER"); user != "" {
		envVars = append(envVars, "USER="+sbruntime.ShellQuote(user))
	}

	// DISPLAY - for X11 headed mode
	if display := os.Getenv("DISPLAY"); display != "" {
		envVars = append(envVars, "DISPLAY="+sbruntime.ShellQuote(display))
	}

	// XAUTHORITY - for X11 authentication (WSL2, remote X, etc.)
	if xauth := os.Getenv("XAUTHORITY"); xauth != "" {
		envVars = append(envVars, "XAUTHORITY="+sbruntime.ShellQuote(xauth))
	}

	// WAYLAND_DISPLAY - for Wayland headed mode
	if waylandDisplay := os.Getenv("WAYLAND_DISPLAY"); waylandDisplay != "" {
		envVars = append(envVars, "WAYLAND_DISPLAY="+sbruntime.ShellQuote(waylandDisplay))
	}

	// XDG_RUNTIME_DIR - needed for Wayland socket access
	if xdgRuntime := os.Getenv("XDG_RUNTIME_DIR"); xdgRuntime != "" {
		envVars = append(envVars, "XDG_RUNTIME_DIR="+sbruntime.ShellQuote(xdgRuntime))
	}

	// Build the wrapper script with env -i for clean environment
	script := "#!/bin/sh\n"
	script += "# GoClaw browser wrapper (clean environment, no sandbox)\n"
	script += "# This script runs Chromium with a minimal environment matching bubblewrap\n\n"
	script += "exec env -i \\\n"
	for i, env := range envVars {
		script += "  " + env
		if i < len(envVars)-1 {
			script += " \\\n"
		}
	}
	script += " \\\n  " + sbruntime.ShellQuote(browserBin) + " \"$@\"\n"

	//nolint:gosec // G306: Executable script needs execute permission
	if err := os.WriteFile(wrapperPath, []byte(script), 0750); err != nil {
		return "", err
	}

	L_debug("browser: created passthrough wrapper",
		"wrapper", wrapperPath,
		"browser", browserBin,
	)

	return wrapperPath, nil
}

// shellQuote properly quotes a string for shell use
func preferredBrowserHome(mgr *sandbox.Manager, realHome string) string {
	if mgr != nil && mgr.GetHomeDir() != "" {
		return mgr.GetHomeDir()
	}
	return realHome
}

// CleanupSandboxWrapper removes the sandbox wrapper script
func CleanupSandboxWrapper() {
	wrapperPath, err := paths.DataPath("browser-sandbox/chromium-wrapper.sh")
	if err != nil {
		return
	}
	_ = os.Remove(wrapperPath)
}

// CheckBwrapForBrowser checks if bwrap is available and logs appropriate messages.
// Returns true if sandboxing should be enabled, false otherwise.
func CheckBwrapForBrowser(cfg BrowserBubblewrapConfig) bool {
	if !cfg.Enabled {
		return false
	}

	if !sbruntime.BrowserSandboxAvailable(cfg.BwrapPath) {
		L_warn("browser sandbox: managed sandbox backend unavailable, running unsandboxed",
			"backend", sbruntime.SandboxBackendName())
		return false
	}

	L_info("browser sandbox: backend enabled", "backend", sbruntime.SandboxBackendName(), "gpu", cfg.GPU)
	return true
}
