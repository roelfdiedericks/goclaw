package tools

import (
	"context"
	"encoding/json"
	"fmt"
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
	runner *ExecRunner
}

// NewExecTool creates a new exec tool
func NewExecTool(cfg ExecToolConfig) *ExecTool {
	timeout := 30 * time.Minute // default: 30 minutes (matches OpenClaw)
	if cfg.Timeout > 0 {
		timeout = time.Duration(cfg.Timeout) * time.Second
	}
	runner := NewExecRunner(ExecRunnerConfig{
		WorkingDir:     cfg.WorkingDir,
		Timeout:        timeout,
		BubblewrapPath: cfg.BubblewrapPath,
		Bubblewrap:     cfg.Bubblewrap,
	})
	return &ExecTool{runner: runner}
}

// NewExecToolWithRunner creates an exec tool using a shared ExecRunner.
// This allows sharing the runner with other tools like JQTool.
func NewExecToolWithRunner(runner *ExecRunner) *ExecTool {
	return &ExecTool{runner: runner}
}

// Runner returns the underlying ExecRunner for sharing with other tools.
func (t *ExecTool) Runner() *ExecRunner {
	return t.runner
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

	// Set working directory
	workDir := params.WorkingDir
	if workDir == "" {
		workDir = t.runner.Config().WorkingDir
	}

	// Check if user has sandbox disabled
	useSandbox := t.runner.Config().Bubblewrap.Enabled
	if sessCtx := GetSessionContext(ctx); sessCtx != nil && sessCtx.User != nil {
		if !sessCtx.User.Sandbox {
			useSandbox = false
			L_debug("exec tool: sandbox disabled for user", "user", sessCtx.User.Name)
		}
	}

	// Create command preview for INFO logging
	cmdPreview := strings.ReplaceAll(params.Command, "\n", " ")
	cmdPreview = strings.ReplaceAll(cmdPreview, "\r", "")
	if len(cmdPreview) > 30 {
		cmdPreview = cmdPreview[:30] + "..."
	}

	L_info("exec tool: running", "cmd", cmdPreview, "workDir", workDir, "sandboxed", useSandbox)

	// Handle custom timeout if specified
	execCtx := ctx
	if params.TimeoutSeconds > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, time.Duration(params.TimeoutSeconds)*time.Second)
		defer cancel()
	}

	// Run command using shared runner
	result, err := t.runner.RunFull(execCtx, params.Command, workDir, useSandbox)
	if err != nil {
		return "", err
	}

	// Build formatted result (ExecTool shows both stdout and stderr with headers)
	var output strings.Builder

	if len(result.Stdout) > 0 {
		output.WriteString("STDOUT:\n")
		output.Write(result.Stdout)
	}

	if len(result.Stderr) > 0 {
		if output.Len() > 0 {
			output.WriteString("\n")
		}
		output.WriteString("STDERR:\n")
		output.Write(result.Stderr)
	}

	if result.ExitCode != 0 {
		if output.Len() > 0 {
			output.WriteString("\n")
		}
		output.WriteString(fmt.Sprintf("Exit code: %d", result.ExitCode))
		L_warn("exec tool: non-zero exit", "cmd", cmdPreview, "exitCode", result.ExitCode)
	}

	if output.Len() == 0 {
		output.WriteString("Command completed successfully (no output)")
	}

	return output.String(), nil
}
