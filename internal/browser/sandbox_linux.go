//go:build linux

package browser

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/roelfdiedericks/goclaw/internal/bwrap"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
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
	wrapperDir := filepath.Join(home, ".openclaw", "goclaw", "browser-sandbox")
	if err := os.MkdirAll(wrapperDir, 0755); err != nil {
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

	if err := os.WriteFile(wrapperPath, []byte(script), 0755); err != nil {
		return "", err
	}

	L_debug("browser sandbox: created wrapper script",
		"wrapper", wrapperPath,
		"browser", browserBin,
		"gpu", cfg.GPU,
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
	home, _ := os.UserHomeDir()
	wrapperPath := filepath.Join(home, ".openclaw", "goclaw", "browser-sandbox", "chromium-wrapper.sh")
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

// buildSandboxedCommand is a helper that builds the bwrap command for debugging.
// Not used directly - see CreateSandboxedLauncher for the actual implementation.
func buildSandboxedCommand(browserBin, workspace, profileDir string, cfg BrowserBubblewrapConfig) (*exec.Cmd, error) {
	home, _ := os.UserHomeDir()

	b := bwrap.BrowserSandbox(workspace, profileDir, home, cfg.GPU)

	if cfg.BwrapPath != "" {
		b.BwrapPath(cfg.BwrapPath)
	}

	for _, path := range cfg.ExtraRoBind {
		b.RoBind(path)
	}

	for _, path := range cfg.ExtraBind {
		b.Bind(path)
	}

	b.Command(browserBin)

	return b.BuildCommand()
}
