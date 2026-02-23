package exec

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Runner handles sandboxed command execution.
// It is shared between ExecTool and JQTool to avoid duplicating sandbox logic.
type Runner struct {
	config RunnerConfig
}

// NewRunner creates a new Runner with the given configuration.
func NewRunner(cfg RunnerConfig) *Runner {
	// Apply default timeout if not set
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Minute
	}
	return &Runner{config: cfg}
}

// Run executes a command and returns stdout.
// Returns error with exit code if command fails (non-zero exit).
// Stderr is discarded (not returned).
// The useSandbox parameter allows the caller to override sandbox settings (e.g., for user sandbox=false).
func (r *Runner) Run(ctx context.Context, command string, useSandbox bool) ([]byte, error) {
	result, err := r.RunFull(ctx, command, "", useSandbox)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, &Error{ExitCode: result.ExitCode}
	}
	return result.Stdout, nil
}

// RunFull executes a command and returns full results including stdout, stderr, and exit code.
// The workDir parameter overrides the default working directory if non-empty.
// The useSandbox parameter allows the caller to override sandbox settings.
func (r *Runner) RunFull(ctx context.Context, command, workDir string, useSandbox bool) (*Result, error) {
	// Use default working directory if not specified
	if workDir == "" {
		workDir = r.config.WorkingDir
	}

	// Create command preview for logging
	cmdPreview := strings.ReplaceAll(command, "\n", " ")
	cmdPreview = strings.ReplaceAll(cmdPreview, "\r", "")
	if len(cmdPreview) > 50 {
		cmdPreview = cmdPreview[:50] + "..."
	}

	L_debug("exec runner: running", "cmd", cmdPreview, "workDir", workDir, "sandboxed", useSandbox)
	L_trace("exec runner: full command", "cmd", command, "timeout", r.config.Timeout)

	// Create context with timeout
	execCtx, cancel := context.WithTimeout(ctx, r.config.Timeout)
	defer cancel()

	// Build command - sandboxed or unsandboxed
	var cmd *exec.Cmd
	if useSandbox && r.config.Bubblewrap.Enabled {
		sandboxedCmd, err := r.buildSandboxedCommand(execCtx, command, workDir)
		if err != nil {
			L_error("exec runner: sandbox failed", "error", err)
			return nil, fmt.Errorf("sandbox error: %w", err)
		}
		if sandboxedCmd != nil {
			cmd = sandboxedCmd
		}
	}

	// Fall back to unsandboxed execution if no sandbox command was built
	if cmd == nil {
		cmd = exec.CommandContext(execCtx, "bash", "-c", command)
		cmd.Dir = workDir
	}

	// Capture output
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Run command
	startTime := time.Now()
	err := cmd.Run()
	elapsed := time.Since(startTime)

	// Get exit code
	exitCode := 0
	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			L_warn("exec runner: timed out", "cmd", cmdPreview, "timeout", r.config.Timeout)
			return nil, fmt.Errorf("command timed out after %v", r.config.Timeout)
		}
		// Extract exit code from error
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			// Other error (e.g., command not found)
			return nil, fmt.Errorf("exec failed: %w", err)
		}
		L_debug("exec runner: non-zero exit", "cmd", cmdPreview, "exitCode", exitCode, "elapsed", elapsed)
	} else {
		L_debug("exec runner: completed", "cmd", cmdPreview, "elapsed", elapsed, "stdoutLen", stdout.Len(), "stderrLen", stderr.Len())
	}

	return &Result{
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
		ExitCode: exitCode,
	}, nil
}

// Config returns the runner's configuration (read-only access for tools)
func (r *Runner) Config() RunnerConfig {
	return r.config
}
