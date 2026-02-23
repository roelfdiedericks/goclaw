//go:build !linux

package exec

import (
	"context"
	"os/exec"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// buildSandboxedCommand is not available on non-Linux platforms.
// Returns nil to indicate unsandboxed execution should be used.
func (r *Runner) buildSandboxedCommand(ctx context.Context, command, workDir string) (*exec.Cmd, error) {
	if r.config.Bubblewrap.Enabled {
		L_warn("exec runner: bubblewrap not available on this platform, running unsandboxed")
	}
	return nil, nil
}
