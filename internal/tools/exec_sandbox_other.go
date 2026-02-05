//go:build !linux

package tools

import (
	"context"
	"os/exec"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// buildSandboxedCommand is not available on non-Linux platforms.
// Returns nil to indicate unsandboxed execution should be used.
func (t *ExecTool) buildSandboxedCommand(ctx context.Context, command, workDir string) (*exec.Cmd, error) {
	if t.bubblewrap.Enabled {
		L_warn("exec sandbox: bubblewrap not available on this platform, running unsandboxed")
	}
	return nil, nil // Not sandboxed
}
