//go:build linux

package bwrap

// BrowserSandbox creates a pre-configured builder for browser (Chromium) sandboxing.
// Sets up system binds, GPU access, shared memory, and display access.
//
// Parameters:
//   - workspace: the workspace directory (for media downloads, writable)
//   - browserProfile: the browser profile directory (writable)
//   - home: the home directory to expose inside sandbox
//   - gpu: whether to enable GPU acceleration (/dev/dri)
func BrowserSandbox(workspace, browserProfile, home string, gpu bool) *Builder {
	b := New()

	// Core system binds
	b.SystemBinds()
	b.EtcBinds()
	b.SSLCerts()
	b.Fonts()

	// Workspace writable (for screenshot/media downloads)
	b.Bind(workspace)

	// Browser profile writable (cookies, cache, etc)
	b.Bind(browserProfile)

	// Isolated /tmp
	b.Tmpfs("/tmp")

	// Process info
	b.Proc()
	b.Dev()

	// Shared memory required for Chromium IPC
	b.SharedMem()

	// GPU acceleration
	if gpu {
		b.GPU()
	}

	// Network required for browser
	b.ShareNet()

	// Environment - clearenv MUST come before any setenv calls
	b.ClearEnv()
	b.DefaultEnv(home)

	// Display access (X11 or Wayland) - after clearenv so DISPLAY is preserved
	b.Display()
	b.Wayland()

	// D-Bus for Chromium (needed for some features in headed mode)
	b.Dbus()

	// Set working directory
	b.Chdir(workspace)

	// Kill sandbox if GoClaw dies
	b.DieWithParent()

	return b
}
