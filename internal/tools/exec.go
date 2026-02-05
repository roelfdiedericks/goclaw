package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// ExecToolConfig holds configuration for the exec tool
type ExecToolConfig struct {
	WorkingDir     string
	Timeout        int // seconds, 0 = use default (1800)
	BubblewrapPath string
	Bubblewrap     ExecBubblewrapCfg
}

// ExecTool executes shell commands
type ExecTool struct {
	workingDir     string
	timeout        time.Duration
	bubblewrapPath string
	bubblewrap     ExecBubblewrapCfg
}

// NewExecTool creates a new exec tool
func NewExecTool(cfg ExecToolConfig) *ExecTool {
	timeout := 30 * time.Minute // default: 30 minutes (matches OpenClaw)
	if cfg.Timeout > 0 {
		timeout = time.Duration(cfg.Timeout) * time.Second
	}
	return &ExecTool{
		workingDir:     cfg.WorkingDir,
		timeout:        timeout,
		bubblewrapPath: cfg.BubblewrapPath,
		bubblewrap:     cfg.Bubblewrap,
	}
}

func (t *ExecTool) Name() string {
	return "exec"
}

func (t *ExecTool) Description() string {
	return "Execute a shell command. Returns stdout and stderr. Use with caution."
}

func (t *ExecTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The shell command to execute.",
			},
			"working_dir": map[string]any{
				"type":        "string",
				"description": "Optional: Working directory for the command. Defaults to workspace root.",
			},
			"timeout_seconds": map[string]any{
				"type":        "integer",
				"description": "Optional: Timeout in seconds. Defaults to 1800 (30 minutes).",
			},
		},
		"required": []string{"command"},
	}
}

type execInput struct {
	Command        string `json:"command"`
	WorkingDir     string `json:"working_dir,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

func (t *ExecTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params execInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	// Set timeout
	timeout := t.timeout
	if params.TimeoutSeconds > 0 {
		timeout = time.Duration(params.TimeoutSeconds) * time.Second
	}

	// Set working directory
	workDir := t.workingDir
	if params.WorkingDir != "" {
		workDir = params.WorkingDir
	}

	// Check if user has sandbox disabled
	useSandbox := t.bubblewrap.Enabled
	if sessCtx := GetSessionContext(ctx); sessCtx != nil && sessCtx.User != nil {
		if !sessCtx.User.Sandbox {
			useSandbox = false
			L_debug("exec tool: sandbox disabled for user", "user", sessCtx.User.Name)
		}
	}

	// Create command preview for INFO: first linebreak or 30 chars, newlines stripped
	cmdPreview := strings.ReplaceAll(params.Command, "\n", " ")
	cmdPreview = strings.ReplaceAll(cmdPreview, "\r", "")
	if len(cmdPreview) > 30 {
		cmdPreview = cmdPreview[:30] + "..."
	}

	L_info("exec tool: running", "cmd", cmdPreview, "workDir", workDir, "sandboxed", useSandbox)
	L_trace("exec tool: full command", "cmd", params.Command, "timeout", timeout)

	// Create context with timeout
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Build command - sandboxed or unsandboxed
	var cmd *exec.Cmd
	if useSandbox {
		sandboxedCmd, err := t.buildSandboxedCommand(execCtx, params.Command, workDir)
		if err != nil {
			// Sandbox was requested but failed to build - hard error
			L_error("exec tool: sandbox failed", "error", err)
			return "", fmt.Errorf("sandbox error: %w", err)
		}
		if sandboxedCmd != nil {
			cmd = sandboxedCmd
		}
	}

	// Fall back to unsandboxed execution if no sandbox command was built
	if cmd == nil {
		cmd = exec.CommandContext(execCtx, "bash", "-c", params.Command)
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

	// Build result
	var result strings.Builder
	
	if stdout.Len() > 0 {
		result.WriteString("STDOUT:\n")
		result.WriteString(stdout.String())
	}
	
	if stderr.Len() > 0 {
		if result.Len() > 0 {
			result.WriteString("\n")
		}
		result.WriteString("STDERR:\n")
		result.WriteString(stderr.String())
	}

	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			L_warn("exec tool: timed out", "cmd", cmdPreview, "timeout", timeout)
			return result.String(), fmt.Errorf("command timed out after %v", timeout)
		}
		if result.Len() > 0 {
			result.WriteString("\n")
		}
		result.WriteString(fmt.Sprintf("Exit error: %v", err))
		L_warn("exec tool: failed", "cmd", cmdPreview, "error", err, "elapsed", elapsed)
	} else {
		L_debug("exec tool: completed", "cmd", cmdPreview, "elapsed", elapsed, "stdoutLen", stdout.Len(), "stderrLen", stderr.Len())
	}

	if result.Len() == 0 {
		result.WriteString("Command completed successfully (no output)")
	}

	return result.String(), nil
}
