package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// Tool executes shell commands
type Tool struct {
	runner *Runner
}

// NewTool creates a new exec tool
func NewTool(cfg ToolConfig) *Tool {
	timeout := 30 * time.Minute
	if cfg.Timeout > 0 {
		timeout = time.Duration(cfg.Timeout) * time.Second
	}
	runner := NewRunner(RunnerConfig{
		WorkingDir:     cfg.WorkingDir,
		Timeout:        timeout,
		BubblewrapPath: cfg.BubblewrapPath,
		Bubblewrap:     cfg.Bubblewrap,
	})
	return &Tool{runner: runner}
}

// NewToolWithRunner creates an exec tool using a shared Runner.
// This allows sharing the runner with other tools like JQTool.
func NewToolWithRunner(runner *Runner) *Tool {
	return &Tool{runner: runner}
}

// Runner returns the underlying Runner for sharing with other tools.
func (t *Tool) Runner() *Runner {
	return t.runner
}

func (t *Tool) Name() string {
	return "exec"
}

func (t *Tool) Description() string {
	return "Execute a shell command. Returns stdout and stderr. Use with caution."
}

func (t *Tool) Schema() map[string]any {
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

func (t *Tool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var params execInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	// Set working directory
	workDir := params.WorkingDir
	if workDir == "" {
		workDir = t.runner.Config().WorkingDir
	}

	// Check if user has sandbox disabled
	useSandbox := t.runner.Config().Bubblewrap.Enabled
	if sessCtx := types.GetSessionContext(ctx); sessCtx != nil && sessCtx.User != nil {
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
		return nil, err
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

	return types.ExternalTextResult(output.String(), "exec"), nil
}

// ToolConfig holds configuration for the exec tool
type ToolConfig struct {
	WorkingDir     string
	Timeout        int // seconds, 0 = use default (1800)
	BubblewrapPath string
	Bubblewrap     BubblewrapConfig
}
