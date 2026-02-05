//go:build linux

package bwrap

import "path/filepath"

// ExecSandbox creates a pre-configured builder for the exec tool.
// Sets up standard system binds, isolated /tmp, /proc, and safe defaults.
//
// Parameters:
//   - workspace: the workspace directory (writable)
//   - home: the home directory to expose inside sandbox
//   - allowNetwork: whether to share network with host
//   - clearEnv: whether to clear environment variables
func ExecSandbox(workspace, home string, allowNetwork, clearEnv bool) *Builder {
	b := New()

	// Core system binds
	b.SystemBinds()
	b.EtcBinds()
	b.SSLCerts()

	// Isolated temporary directory
	b.Tmpfs("/tmp")

	// Process info
	b.Proc()
	b.Dev()
	b.UnsharePID()

	// Workspace is writable
	b.Bind(workspace)
	b.Chdir(workspace)

	// User's ~/.local is writable (for pip install --user, etc.)
	localDir := filepath.Join(home, ".local")
	if pathExists(localDir) {
		b.Bind(localDir)
	}

	// Network
	if allowNetwork {
		b.ShareNet()
	} else {
		b.UnshareNet()
	}

	// Environment
	if clearEnv {
		b.ClearEnv()
		b.DefaultEnv(home)
	}

	// Kill sandbox if GoClaw dies
	b.DieWithParent()

	return b
}
