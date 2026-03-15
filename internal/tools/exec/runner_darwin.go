//go:build darwin

package exec

import (
	"context"
	"os"
	"os/exec"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/sandbox"
	sbruntime "github.com/roelfdiedericks/goclaw/internal/sandbox/runtime"
)

// buildSandboxedCommand creates a sandboxed exec.Cmd using the current sandbox backend.
// Returns nil if sandboxing is disabled or not available.
func (r *Runner) buildSandboxedCommand(ctx context.Context, command, workDir string) (*exec.Cmd, error) {
	if !r.config.Bubblewrap.Enabled {
		return nil, nil
	}

	mgr := sandbox.GetManager()
	policy := mgr.ResolvePolicy()
	vols := runtimeVolumes(mgr.GetVolumes())
	protectedDirs := mgr.GetProtectedDirs()
	autoDocsRoots := mgr.GetAutoDocsRoots()
	pathValue := os.Getenv("PATH")
	if mgr != nil {
		pathValue = mgr.BuildSandboxPATH(policy.VisibleHomeDir)
	}
	extraBind := append([]string{}, r.config.Bubblewrap.ExtraBind...)
	extraRoBind := append([]string{}, r.config.Bubblewrap.ExtraRoBind...)
	if mgr.IsAutoDocsWriteMode() {
		extraBind = append(extraBind, autoDocsRoots...)
	} else {
		extraRoBind = append(extraRoBind, autoDocsRoots...)
	}
	L_debug("exec runner: darwin sandbox request",
		"backendPath", r.config.BubblewrapPath,
		"workspaceDir", r.config.WorkingDir,
		"workDir", workDir,
		"homeDir", policy.VisibleHomeDir,
		"pathValue", pathValue,
		"volumes", len(vols),
		"protectedDirs", len(protectedDirs),
	)
	cmd, err := sbruntime.BuildExecCommand(command, sbruntime.ExecLaunchOptions{
		BackendPath:   r.config.BubblewrapPath,
		SandboxMode:   mgr.GetMode(),
		WorkspaceDir:  r.config.WorkingDir,
		WorkDir:       workDir,
		VisibleHomeDir: policy.VisibleHomeDir,
		BackingHomeDir: policy.BackingHomeDir,
		PathValue:     pathValue,
		Volumes:       vols,
		ProtectedDirs: protectedDirs,
		ClearEnv:      r.config.Bubblewrap.ClearEnv,
		AllowNetwork:  r.config.Bubblewrap.AllowNetwork,
		ExtraEnv:      r.config.Bubblewrap.ExtraEnv,
		ExtraBind:     extraBind,
		ExtraRoBind:   extraRoBind,
	})
	if err != nil {
		L_error("exec runner: failed to build sandbox command", "error", err)
		return nil, err
	}
	if cmd == nil {
		return nil, nil
	}

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
