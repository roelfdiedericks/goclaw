//go:build linux

package tools

import (
	"context"
	"os"
	"os/exec"

	"github.com/roelfdiedericks/goclaw/internal/bwrap"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// buildSandboxedCommand creates a sandboxed exec.Cmd using bubblewrap.
// Returns nil if sandboxing is disabled or not available.
func (t *ExecTool) buildSandboxedCommand(ctx context.Context, command, workDir string) (*exec.Cmd, error) {
	if !t.bubblewrap.Enabled {
		return nil, nil // Not sandboxed
	}

	home, _ := os.UserHomeDir()

	// Build base sandbox config
	b := bwrap.ExecSandbox(t.workingDir, home, t.bubblewrap.AllowNetwork, t.bubblewrap.ClearEnv)

	// Set custom bwrap path if provided
	if t.bubblewrapPath != "" {
		b.BwrapPath(t.bubblewrapPath)
	}

	// Add extra read-only binds
	for _, path := range t.bubblewrap.ExtraRoBind {
		b.RoBind(path)
	}

	// Add extra read-write binds
	for _, path := range t.bubblewrap.ExtraBind {
		b.Bind(path)
	}

	// Add extra environment variables
	for k, v := range t.bubblewrap.ExtraEnv {
		b.SetEnv(k, v)
	}

	// Set working directory inside sandbox (if different from workspace root)
	if workDir != "" && workDir != t.workingDir {
		// Ensure the working directory is bound and accessible
		b.Bind(workDir)
		b.Chdir(workDir)
	}

	// Set the shell command to run
	b.ShellCommand(command)

	// Build the command
	cmd, err := b.BuildCommand()
	if err != nil {
		L_error("exec sandbox: failed to build command", "error", err)
		return nil, err
	}

	// Apply context for timeout handling
	cmd = exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)

	L_debug("exec sandbox: command built",
		"command", command[:min(len(command), 50)],
		"allowNetwork", t.bubblewrap.AllowNetwork,
		"clearEnv", t.bubblewrap.ClearEnv,
	)

	return cmd, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
