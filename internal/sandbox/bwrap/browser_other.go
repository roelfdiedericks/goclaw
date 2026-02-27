//go:build !linux

package bwrap

// BrowserSandbox is not available on non-Linux platforms.
// Returns nil with an error already set.
func BrowserSandbox(workspace, browserProfile, home string, gpu bool) *Builder {
	b := New()
	b.err = ErrNotLinux
	return b
}
