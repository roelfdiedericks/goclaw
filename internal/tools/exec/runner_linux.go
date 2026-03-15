//go:build linux || darwin

package exec

import (
	"context"
	"os/exec"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/sandbox"
	sbruntime "github.com/roelfdiedericks/goclaw/internal/sandbox/runtime"
)

// buildSandboxedCommand creates a sandboxed exec.Cmd using bubblewrap.
// Returns nil if sandboxing is disabled or not available.
func (r *Runner) buildSandboxedCommand(ctx context.Context, command, workDir string) (*exec.Cmd, error) {
	if !r.config.Bubblewrap.Enabled {
		return nil, nil
	}

	mgr := sandbox.GetManager()
	policy := mgr.ResolvePolicy()
	cmd, err := sbruntime.BuildExecCommand(command, sbruntime.ExecLaunchOptions{
		BackendPath:   r.config.BubblewrapPath,
		SandboxMode:   mgr.GetMode(),
		WorkspaceDir:  r.config.WorkingDir,
		WorkDir:       workDir,
		VisibleHomeDir: policy.VisibleHomeDir,
		BackingHomeDir: policy.BackingHomeDir,
		Volumes:       runtimeVolumes(mgr.GetVolumes()),
		ProtectedDirs: mgr.GetProtectedDirs(),
		ClearEnv:      r.config.Bubblewrap.ClearEnv,
		AllowNetwork:  r.config.Bubblewrap.AllowNetwork,
		ExtraEnv:      r.config.Bubblewrap.ExtraEnv,
		ExtraBind:     r.config.Bubblewrap.ExtraBind,
		ExtraRoBind:   r.config.Bubblewrap.ExtraRoBind,
	})
	if err != nil {
		L_error("exec runner: failed to build sandbox command", "error", err)
		return nil, err
	}
	if cmd == nil {
		return nil, nil
	}

	// Apply context for timeout handling
	baseCmd := cmd
	cmd = exec.CommandContext(ctx, baseCmd.Path, baseCmd.Args[1:]...) //nolint:gosec // G204: sandbox backend provides validated command
	cmd.Dir = baseCmd.Dir
	cmd.Env = baseCmd.Env

	L_debug("exec runner: sandbox command built",
		"command", truncate(command, 50),
		"allowNetwork", r.config.Bubblewrap.AllowNetwork,
		"clearEnv", r.config.Bubblewrap.ClearEnv,
	)

	return cmd, nil
}

func runtimeVolumes(vols []sandbox.SandboxVolume) []sbruntime.SandboxVolume {
	out := make([]sbruntime.SandboxVolume, 0, len(vols))
	for _, vol := range vols {
		out = append(out, sbruntime.SandboxVolume{
			MountPoint: vol.MountPoint,
			Source:     vol.Source,
		})
	}
	return out
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
