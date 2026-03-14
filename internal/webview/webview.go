// Package webview provides unified webview/browser opening functionality
package webview

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/abemedia/go-webview"
	_ "github.com/abemedia/go-webview/embedded" // Embeds native libwebview.so
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Options configures webview/browser behavior
type Options struct {
	Title   string        // Window title (default: "GoClaw")
	Width   int           // Window width (default: 1200)
	Height  int           // Window height (default: 800)
	DevMode bool          // Enable developer tools (right-click inspect)
	OnReady chan struct{} // Optional: wait for this channel before navigating
}

// Open tries to open a URL in an embedded webview first, falling back to system browser.
// Returns (true, nil) if webview was used (blocks until window closed).
// Returns (false, nil) if fell back to browser (caller must handle waiting/cleanup).
// Returns (false, error) if neither webview nor browser could be opened.
func Open(url string, opts Options) (usedWebview bool, err error) {
	// Apply defaults
	if opts.Title == "" {
		opts.Title = "GoClaw"
	}
	if opts.Width == 0 {
		opts.Width = 1200
	}
	if opts.Height == 0 {
		opts.Height = 800
	}

	// Try webview first
	L_debug("webview: attempting to open", "url", url, "devMode", opts.DevMode)
	if tryWebview(url, opts) {
		L_info("webview: window closed")
		return true, nil
	}

	// Webview not available, try system browser
	L_debug("webview: not available, trying system browser")
	if err := openBrowser(url); err != nil {
		L_warn("webview: failed to open browser", "error", err)
		return false, fmt.Errorf("no UI available: webview unavailable and browser failed: %w", err)
	}

	return false, nil
}

// tryWebview attempts to open URL in embedded webview
// Returns true if successful (blocks until window closed), false if webview unavailable
func tryWebview(url string, opts Options) bool {
	w := webview.New(opts.DevMode)
	if w == nil {
		return false
	}
	defer w.Destroy()

	// Wait for ready signal if provided
	if opts.OnReady != nil {
		<-opts.OnReady
	}

	// Bind closeWebview function for JavaScript: window.closeWebview()
	if err := w.Bind("closeWebview", func() {
		w.Terminate()
	}); err != nil {
		L_warn("webview: failed to bind close handler", "error", err)
		return false
	}

	w.SetTitle(opts.Title)
	w.SetSize(opts.Width, opts.Height, webview.HintNone)
	w.Navigate(url)
	w.Run() // Blocks until window closed
	return true
}

// openBrowser attempts to open URL in system browser
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default: // linux, freebsd, etc.
		return exec.Command("xdg-open", url).Start()
	}
}

// CanUseWebview checks if webview (webkit2gtk) is available
func CanUseWebview() bool {
	w := webview.New(false)
	if w == nil {
		return false
	}
	w.Destroy()
	return true
}

// CanUseBrowser checks if system browser opening is likely available
func CanUseBrowser() bool {
	switch runtime.GOOS {
	case "windows", "darwin":
		return true
	default:
		_, err := exec.LookPath("xdg-open")
		return err == nil
	}
}

// IsAvailable returns true if either webview or browser can be used
func IsAvailable() bool {
	return CanUseWebview() || CanUseBrowser()
}
