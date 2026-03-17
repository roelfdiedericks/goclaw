//go:build linux

package bwrap

import (
	"path/filepath"

	"github.com/roelfdiedericks/goclaw/internal/sandbox"
)

// ExecSandbox creates a pre-configured builder for the exec tool.
// Sets up standard system binds, isolated /tmp, /proc, and safe defaults.
func ExecSandbox(workspace, visibleHome, backingHome, sandboxMode string, allowNetwork, clearEnv bool) *Builder {
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

	// Home mode: mount broad home replacement, then overlay specific dirs.
	if backingHome != "" && sandboxMode == sandbox.ModeHome {
		b.BindTo(backingHome, visibleHome)
	} else if sandboxMode == sandbox.ModeAutoDocsRead || sandboxMode == sandbox.ModeAutoDocsWrite {
		// Autodocs modes should not expose hidden home paths. Create an empty
		// visible home root and overlay only explicit autodocs binds from callers.
		homeParent := filepath.Dir(visibleHome)
		if homeParent != "" && homeParent != "." && homeParent != string(filepath.Separator) {
			b.Tmpfs(homeParent)
		}
		b.Tmpfs(visibleHome)
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
		b.DefaultEnv(visibleHome, mgr.BuildSandboxPATH(visibleHome))
	}

	// Kill sandbox if GoClaw dies
	b.DieWithParent()

	return b
}
