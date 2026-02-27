//go:build linux

package exec

import (
	"context"
	"os"
	"os/exec"

	"github.com/roelfdiedericks/goclaw/internal/sandbox/bwrap"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// buildSandboxedCommand creates a sandboxed exec.Cmd using bubblewrap.
// Returns nil if sandboxing is disabled or not available.
func (r *Runner) buildSandboxedCommand(ctx context.Context, command, workDir string) (*exec.Cmd, error) {
	if !r.config.Bubblewrap.Enabled {
		return nil, nil
	}

	home, _ := os.UserHomeDir()

	// Build base sandbox config
	b := bwrap.ExecSandbox(r.config.WorkingDir, home, r.config.Bubblewrap.AllowNetwork, r.config.Bubblewrap.ClearEnv)

	// Set custom bwrap path if provided
	if r.config.BubblewrapPath != "" {
		b.BwrapPath(r.config.BubblewrapPath)
	}

	// Add extra read-only binds
	for _, path := range r.config.Bubblewrap.ExtraRoBind {
		b.RoBind(path)
	}

	// Add extra read-write binds
	for _, path := range r.config.Bubblewrap.ExtraBind {
		b.Bind(path)
	}

	// Add extra environment variables
	for k, v := range r.config.Bubblewrap.ExtraEnv {
		b.SetEnv(k, v)
	}

	// Set working directory inside sandbox (if different from workspace root)
	if workDir != "" && workDir != r.config.WorkingDir {
		b.Bind(workDir)
		b.Chdir(workDir)
	}

	// Set the shell command to run
	b.ShellCommand(command)

	// Build the command
	cmd, err := b.BuildCommand()
	if err != nil {
		L_error("exec runner: failed to build sandbox command", "error", err)
		return nil, err
	}

	// Apply context for timeout handling
	cmd = exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...) //nolint:gosec // G204: cmd from bwrap builder - sandboxed execution

	L_debug("exec runner: sandbox command built",
		"command", truncate(command, 50),
		"allowNetwork", r.config.Bubblewrap.AllowNetwork,
		"clearEnv", r.config.Bubblewrap.ClearEnv,
	)

	return cmd, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
