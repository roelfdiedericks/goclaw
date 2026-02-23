//go:build linux

package browser

import (
	"os"
	"path/filepath"

	"github.com/roelfdiedericks/goclaw/internal/bwrap"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/paths"
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
	return bwrap.IsLinux() && bwrap.IsAvailable(bwrapPath)
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

	// Build bwrap command
	b := bwrap.BrowserSandbox(workspace, profileDir, home, cfg.GPU)

	if cfg.BwrapPath != "" {
		b.BwrapPath(cfg.BwrapPath)
	}

	// Add browser binary directory (read-only) so chromium can execute
	// browserBin is something like /home/user/.goclaw/browser/bin/chromium-XXX/chrome
	// We need to bind the parent directories up to the bin/ level
	browserBinDir := filepath.Dir(browserBin)     // chromium-XXX directory
	browserBaseDir := filepath.Dir(browserBinDir) // bin directory
	b.RoBind(browserBaseDir)

	// Add extra read-only binds
	for _, path := range cfg.ExtraRoBind {
		b.RoBind(path)
	}

	// Add extra read-write binds
	for _, path := range cfg.ExtraBind {
		b.Bind(path)
	}

	// Set the browser binary as the command (without args - launcher will add them)
	b.Command(browserBin)

	// Build command to get the bwrap path and base args
	bwrapPath, args, err := b.Build()
	if err != nil {
		return "", err
	}

	// Create a wrapper script that runs bwrap with the browser
	// The wrapper passes through all arguments to the browser
	wrapperDir, err := paths.DataPath("browser-sandbox")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(wrapperDir, 0750); err != nil {
		return "", err
	}

	wrapperPath := filepath.Join(wrapperDir, "chromium-wrapper.sh")

	// Build the wrapper script
	// Remove the last arg (browser binary) since it's already in the args
	// The launcher will add Chrome-specific args after the wrapper
	baseArgs := args[:len(args)-1] // Remove trailing browser binary

	script := "#!/bin/sh\n"
	script += "# GoClaw browser sandbox wrapper\n"
	script += "# This script runs Chromium through bubblewrap for sandboxing\n\n"
	script += "exec " + shellQuote(bwrapPath)
	for _, arg := range baseArgs {
		script += " " + shellQuote(arg)
	}
	script += " " + shellQuote(browserBin) + " \"$@\"\n"

	//nolint:gosec // G306: Executable script needs execute permission
	if err := os.WriteFile(wrapperPath, []byte(script), 0750); err != nil {
		return "", err
	}

	L_debug("browser sandbox: created wrapper script",
		"wrapper", wrapperPath,
		"browser", browserBin,
		"browserBinDir", browserBaseDir,
		"gpu", cfg.GPU,
	)

	return wrapperPath, nil
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
		envVars = append(envVars, "PATH="+shellQuote(path))
	}

	// HOME
	envVars = append(envVars, "HOME="+shellQuote(home))

	// TERM
	envVars = append(envVars, "TERM='xterm'")

	// LANG - preserve from host or default
	if lang := os.Getenv("LANG"); lang != "" {
		envVars = append(envVars, "LANG="+shellQuote(lang))
	} else {
		envVars = append(envVars, "LANG='C.UTF-8'")
	}

	// USER - preserve from host
	if user := os.Getenv("USER"); user != "" {
		envVars = append(envVars, "USER="+shellQuote(user))
	}

	// DISPLAY - for X11 headed mode
	if display := os.Getenv("DISPLAY"); display != "" {
		envVars = append(envVars, "DISPLAY="+shellQuote(display))
	}

	// XAUTHORITY - for X11 authentication (WSL2, remote X, etc.)
	if xauth := os.Getenv("XAUTHORITY"); xauth != "" {
		envVars = append(envVars, "XAUTHORITY="+shellQuote(xauth))
	}

	// WAYLAND_DISPLAY - for Wayland headed mode
	if waylandDisplay := os.Getenv("WAYLAND_DISPLAY"); waylandDisplay != "" {
		envVars = append(envVars, "WAYLAND_DISPLAY="+shellQuote(waylandDisplay))
	}

	// XDG_RUNTIME_DIR - needed for Wayland socket access
	if xdgRuntime := os.Getenv("XDG_RUNTIME_DIR"); xdgRuntime != "" {
		envVars = append(envVars, "XDG_RUNTIME_DIR="+shellQuote(xdgRuntime))
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
	script += " \\\n  " + shellQuote(browserBin) + " \"$@\"\n"

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
func shellQuote(s string) string {
	// Simple quoting - wrap in single quotes, escape existing single quotes
	out := "'"
	for _, c := range s {
		if c == '\'' {
			out += "'\\''"
		} else {
			out += string(c)
		}
	}
	out += "'"
	return out
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

	if !bwrap.IsLinux() {
		L_warn("browser sandbox: bubblewrap only available on Linux, running unsandboxed")
		return false
	}

	if !bwrap.IsAvailable(cfg.BwrapPath) {
		L_warn("browser sandbox: bwrap not found, running unsandboxed")
		return false
	}

	L_info("browser sandbox: bubblewrap enabled", "gpu", cfg.GPU)
	return true
}
