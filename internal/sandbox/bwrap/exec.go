//go:build linux

package bwrap

import (
	"github.com/roelfdiedericks/goclaw/internal/sandbox"
)

// ExecSandbox creates a pre-configured builder for the exec tool.
// Sets up standard system binds, isolated /tmp, /proc, and safe defaults.
func ExecSandbox(workspace, home string, allowNetwork, clearEnv bool) *Builder {
	b := New()
	mgr := sandbox.GetManager()

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

	// Sandbox home isolation: mount broad home replacement first,
	// then overlay specific directories on top (bwrap last-mount-wins).
	if sandboxHome := mgr.GetHomeDir(); sandboxHome != "" {
		b.BindTo(sandboxHome, home)
	} else {
		for _, vol := range mgr.GetVolumes() {
			if pathExists(vol.Source) {
				b.BindTo(vol.Source, vol.MountPoint)
			}
		}
	}

	// Workspace is writable (overlays on top of home bind)
	b.Bind(workspace)
	b.Chdir(workspace)

	// Write-protected directories overlay on top of everything
	for _, protectedPath := range mgr.GetProtectedDirs() {
		if pathExists(protectedPath) {
			b.RoBind(protectedPath)
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
		b.DefaultEnv(home, mgr.BuildSandboxPATH(home))
	}

	// Kill sandbox if GoClaw dies
	b.DieWithParent()

	return b
}
