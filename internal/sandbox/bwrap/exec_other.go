//go:build !linux

package bwrap

// ExecSandbox is not available on non-Linux platforms.
// Returns nil with an error already set.
func ExecSandbox(workspace, home string, allowNetwork, clearEnv bool) *Builder {
	b := New()
	b.err = ErrNotLinux
	return b
}
