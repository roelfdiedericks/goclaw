//go:build linux

package bwrap

// BrowserSandbox creates a pre-configured builder for browser (Chromium) sandboxing.
// Sets up system binds, GPU access, shared memory, and display access.
//
// Parameters:
//   - workspace: the workspace directory (for media downloads, writable)
//   - browserProfile: the browser profile directory (writable)
//   - visibleHome: the home directory to expose inside sandbox
//   - backingHome: the backing directory for sandbox-managed home content
//   - gpu: whether to enable GPU acceleration (/dev/dri)
func BrowserSandbox(workspace, browserProfile, visibleHome, backingHome string, gpu bool) *Builder {
	b := New()

	// Core system binds
	b.SystemBinds()
	b.EtcBinds()
	b.SSLCerts()
	b.Fonts()

	if backingHome != "" {
		b.BindTo(backingHome, visibleHome)
	}

	// Workspace writable (for screenshot/media downloads)
	b.Bind(workspace)

	// Browser profile writable (cookies, cache, etc)
	b.Bind(browserProfile)

	// Chromium needs writable ~/.cache and ~/.config for various temp files
	// Use tmpfs so it can write but nothing persists outside profile
	b.Tmpfs(visibleHome + "/.cache")
	b.Tmpfs(visibleHome + "/.config")

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
	b.DefaultEnv(visibleHome, "")

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
