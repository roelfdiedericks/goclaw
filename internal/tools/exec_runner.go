package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// ExecRunnerConfig holds configuration for command execution
type ExecRunnerConfig struct {
	WorkingDir     string
	Timeout        time.Duration
	BubblewrapPath string
	Bubblewrap     ExecBubblewrapCfg
}

// ExecRunner handles sandboxed command execution.
// It is shared between ExecTool and JQTool to avoid duplicating sandbox logic.
type ExecRunner struct {
	config ExecRunnerConfig
}

// NewExecRunner creates a new ExecRunner with the given configuration.
func NewExecRunner(cfg ExecRunnerConfig) *ExecRunner {
	// Apply default timeout if not set
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Minute // default: 30 minutes (matches OpenClaw)
	}
	return &ExecRunner{config: cfg}
}

// ExecError represents a command execution error with exit code
type ExecError struct {
	ExitCode int
	Err      error
}

func (e *ExecError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("command exited with code %d: %v", e.ExitCode, e.Err)
	}
	return fmt.Sprintf("command exited with code %d", e.ExitCode)
}

func (e *ExecError) Unwrap() error {
	return e.Err
}

// ExecResult holds the result of command execution
type ExecResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Run executes a command and returns stdout.
// Returns error with exit code if command fails (non-zero exit).
// Stderr is discarded (not returned).
// The useSandbox parameter allows the caller to override sandbox settings (e.g., for user sandbox=false).
func (r *ExecRunner) Run(ctx context.Context, command string, useSandbox bool) ([]byte, error) {
	result, err := r.RunFull(ctx, command, "", useSandbox)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, &ExecError{ExitCode: result.ExitCode}
	}
	return result.Stdout, nil
}

// RunFull executes a command and returns full results including stdout, stderr, and exit code.
// The workDir parameter overrides the default working directory if non-empty.
// The useSandbox parameter allows the caller to override sandbox settings.
func (r *ExecRunner) RunFull(ctx context.Context, command, workDir string, useSandbox bool) (*ExecResult, error) {
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

	return &ExecResult{
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
		ExitCode: exitCode,
	}, nil
}

// Config returns the runner's configuration (read-only access for tools)
func (r *ExecRunner) Config() ExecRunnerConfig {
	return r.config
}
