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

// ExecTool executes shell commands
type ExecTool struct {
	workingDir string
	timeout    time.Duration
}

// NewExecTool creates a new exec tool
func NewExecTool(workingDir string) *ExecTool {
	return &ExecTool{
		workingDir: workingDir,
		timeout:    5 * time.Minute, // default timeout
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
				"description": "Optional: Timeout in seconds. Defaults to 300 (5 minutes).",
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

	// Truncate command for logging (avoid huge commands in logs)
	cmdPreview := params.Command
	if len(cmdPreview) > 100 {
		cmdPreview = cmdPreview[:100] + "..."
	}
	L_info("exec tool: running command", "cmd", cmdPreview, "workDir", workDir, "timeout", timeout)

	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Create command
	cmd := exec.CommandContext(ctx, "bash", "-c", params.Command)
	cmd.Dir = workDir

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
		if ctx.Err() == context.DeadlineExceeded {
			L_warn("exec tool: command timed out", "cmd", cmdPreview, "timeout", timeout)
			return result.String(), fmt.Errorf("command timed out after %v", timeout)
		}
		if result.Len() > 0 {
			result.WriteString("\n")
		}
		result.WriteString(fmt.Sprintf("Exit error: %v", err))
		L_warn("exec tool: command failed", "cmd", cmdPreview, "error", err, "elapsed", elapsed)
	} else {
		L_info("exec tool: command completed", "cmd", cmdPreview, "elapsed", elapsed, "stdoutLen", stdout.Len(), "stderrLen", stderr.Len())
	}

	if result.Len() == 0 {
		result.WriteString("Command completed successfully (no output)")
	}

	return result.String(), nil
}
