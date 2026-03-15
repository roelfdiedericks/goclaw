//go:build darwin

package sandbox

import (
	"os/exec"
	"testing"

	sbruntime "github.com/roelfdiedericks/goclaw/internal/sandbox/runtime"
)

func runSandboxedCommand(t *testing.T, command string, opts sbruntime.ExecLaunchOptions) (exitCode int, output string) {
	t.Helper()

	cmd, err := sbruntime.BuildExecCommand(command, opts)
	if err != nil {
		t.Fatalf("build sandbox command: %v", err)
	}
	if cmd == nil {
		t.Skip("sandbox backend unavailable on this host")
	}

	out, runErr := cmd.CombinedOutput()
	if runErr == nil {
		return 0, string(out)
	}
	if exitErr, ok := runErr.(*exec.ExitError); ok {
		return exitErr.ExitCode(), string(out)
	}
	t.Fatalf("sandbox command failed unexpectedly: %v, out=%s", runErr, string(out))
	return -1, string(out)
}
