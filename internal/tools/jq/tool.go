package jq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/itchyny/gojq"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/sandbox"
	"github.com/roelfdiedericks/goclaw/internal/tools/exec"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// Tool queries and transforms JSON using jq syntax
type Tool struct {
	workingDir string
	runner     *exec.Runner
}

// NewTool creates a new JQ tool
func NewTool(workingDir string, runner *exec.Runner) *Tool {
	return &Tool{
		workingDir: workingDir,
		runner:     runner,
	}
}

func (t *Tool) Name() string {
	return "jq"
}

func (t *Tool) Description() string {
	return "Query and transform JSON using jq syntax. Can read from file, inline JSON, or command output."
}

func (t *Tool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "jq query/filter expression (e.g., '.items[] | .name')",
			},
			"file": map[string]any{
				"type":        "string",
				"description": "Path to JSON file to query. Mutually exclusive with 'input' and 'exec'.",
			},
			"input": map[string]any{
				"type":        "string",
				"description": "Inline JSON string to query. Mutually exclusive with 'file' and 'exec'.",
			},
			"exec": map[string]any{
				"type":        "string",
				"description": "Shell command whose stdout is piped through jq. Mutually exclusive with 'file' and 'input'. Respects sandbox settings.",
			},
			"raw": map[string]any{
				"type":        "boolean",
				"description": "Output raw strings without JSON encoding (like jq -r). Default: false",
			},
			"compact": map[string]any{
				"type":        "boolean",
				"description": "Compact output (no pretty-printing). Default: false",
			},
		},
		"required": []string{"query"},
	}
}

type jqInput struct {
	Query   string `json:"query"`
	File    string `json:"file,omitempty"`
	Input   string `json:"input,omitempty"`
	Exec    string `json:"exec,omitempty"`
	Raw     bool   `json:"raw,omitempty"`
	Compact bool   `json:"compact,omitempty"`
}

func (t *Tool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var params jqInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	if params.Query == "" {
		return nil, errors.New("query is required")
	}

	// Count how many input sources specified
	sources := 0
	if params.File != "" {
		sources++
	}
	if params.Input != "" {
		sources++
	}
	if params.Exec != "" {
		sources++
	}

	if sources > 1 {
		return nil, errors.New("cannot specify multiple input sources (file, input, exec)")
	}
	if sources == 0 {
		return nil, errors.New("must specify one of: 'file', 'input', or 'exec'")
	}

	// Check if user has sandbox disabled
	sandboxed := true
	if sessCtx := types.GetSessionContext(ctx); sessCtx != nil && sessCtx.User != nil {
		sandboxed = sessCtx.User.Sandbox
	}

	// Get JSON data from the appropriate source
	var data []byte
	var err error

	if params.File != "" {
		data, err = t.readFile(params.File, sandboxed)
		if err != nil {
			return nil, fmt.Errorf("failed to read file: %w", err)
		}
		L_debug("jq tool: read file", "file", params.File, "bytes", len(data), "sandboxed", sandboxed)
	} else if params.Input != "" {
		data = []byte(params.Input)
		L_debug("jq tool: using inline input", "bytes", len(data))
	} else if params.Exec != "" {
		data, err = t.execCommand(ctx, params.Exec, sandboxed)
		if err != nil {
			var execErr *exec.Error
			if errors.As(err, &execErr) {
				return nil, fmt.Errorf("command exited with code %d", execErr.ExitCode)
			}
			return nil, fmt.Errorf("exec failed: %w", err)
		}
		L_debug("jq tool: exec completed", "bytes", len(data))
	}

	// Execute jq query
	result, err := executeJQ(params.Query, data, params.Raw, params.Compact)
	if err != nil {
		return nil, err
	}

	L_debug("jq tool: query completed", "query", truncate(params.Query, 50), "resultLen", len(result))
	return types.TextResult(result), nil
}

// readFile reads a JSON file, respecting sandbox settings
func (t *Tool) readFile(path string, sandboxed bool) ([]byte, error) {
	if sandboxed {
		return sandbox.ReadFile(path, t.workingDir, t.workingDir)
	}

	expandedPath := expandPath(path)
	return os.ReadFile(expandedPath)
}

// execCommand runs a shell command and returns stdout
func (t *Tool) execCommand(ctx context.Context, command string, sandboxed bool) ([]byte, error) {
	useSandbox := sandboxed && t.runner.Config().Bubblewrap.Enabled
	return t.runner.Run(ctx, command, useSandbox)
}

// executeJQ parses and executes a jq query on JSON data
func executeJQ(query string, data []byte, raw bool, compact bool) (string, error) {
	var input interface{}
	if err := json.Unmarshal(data, &input); err != nil {
		return "", fmt.Errorf("invalid JSON: %w", err)
	}

	parsed, err := gojq.Parse(query)
	if err != nil {
		return "", fmt.Errorf("invalid jq query: %w", err)
	}

	var results []interface{}
	iter := parsed.Run(input)
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if err, isErr := v.(error); isErr {
			return "", fmt.Errorf("jq error: %w", err)
		}
		results = append(results, v)
	}

	return formatJQOutput(results, raw, compact)
}

// formatJQOutput formats jq results for output
func formatJQOutput(results []interface{}, raw bool, compact bool) (string, error) {
	var lines []string
	for _, r := range results {
		if raw {
			if s, ok := r.(string); ok {
				lines = append(lines, s)
			} else if r == nil {
				lines = append(lines, "null")
			} else {
				b, err := json.Marshal(r)
				if err != nil {
					return "", fmt.Errorf("failed to encode result: %w", err)
				}
				lines = append(lines, string(b))
			}
		} else {
			var b []byte
			var err error
			if compact {
				b, err = json.Marshal(r)
			} else {
				b, err = json.MarshalIndent(r, "", "  ")
			}
			if err != nil {
				return "", fmt.Errorf("failed to encode result: %w", err)
			}
			lines = append(lines, string(b))
		}
	}
	return strings.Join(lines, "\n"), nil
}

// expandPath handles ~ expansion
func expandPath(path string) string {
	if path == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
