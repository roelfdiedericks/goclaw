//go:build linux

package bwrap

import (
	"github.com/roelfdiedericks/goclaw/internal/sandbox"
)

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

	// Write-protected directories (from centralized registry)
	// Later ro-bind overrides earlier bind for the same path in bubblewrap
	for _, protectedPath := range sandbox.GetProtectedDirs() {
		if pathExists(protectedPath) {
			b.RoBind(protectedPath)
		}
	}

	// Sandbox volumes: isolated writable mounts that replace real host directories.
	// Each volume's Source (under ~/.goclaw/sandbox/) is mounted at MountPoint (e.g., ~/.local).
	for _, vol := range sandbox.GetVolumes() {
		if pathExists(vol.Source) {
			b.BindTo(vol.Source, vol.MountPoint)
		}
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
