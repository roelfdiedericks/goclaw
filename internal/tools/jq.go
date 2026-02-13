package tools

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
)

// JQTool queries and transforms JSON using jq syntax
type JQTool struct {
	workingDir string
	runner     *ExecRunner // for exec parameter
}

// NewJQTool creates a new JQ tool
func NewJQTool(workingDir string, runner *ExecRunner) *JQTool {
	return &JQTool{
		workingDir: workingDir,
		runner:     runner,
	}
}

func (t *JQTool) Name() string {
	return "jq"
}

func (t *JQTool) Description() string {
	return "Query and transform JSON using jq syntax. Can read from file, inline JSON, or command output."
}

func (t *JQTool) Schema() map[string]any {
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

func (t *JQTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params jqInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if params.Query == "" {
		return "", errors.New("query is required")
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
		return "", errors.New("cannot specify multiple input sources (file, input, exec)")
	}
	if sources == 0 {
		return "", errors.New("must specify one of: 'file', 'input', or 'exec'")
	}

	// Check if user has sandbox disabled
	sandboxed := true
	if sessCtx := GetSessionContext(ctx); sessCtx != nil && sessCtx.User != nil {
		sandboxed = sessCtx.User.Sandbox
	}

	// Get JSON data from the appropriate source
	var data []byte
	var err error

	if params.File != "" {
		data, err = t.readFile(params.File, sandboxed)
		if err != nil {
			return "", fmt.Errorf("failed to read file: %w", err)
		}
		L_debug("jq tool: read file", "file", params.File, "bytes", len(data), "sandboxed", sandboxed)
	} else if params.Input != "" {
		data = []byte(params.Input)
		L_debug("jq tool: using inline input", "bytes", len(data))
	} else if params.Exec != "" {
		data, err = t.execCommand(ctx, params.Exec, sandboxed)
		if err != nil {
			// Non-zero exit or other error - return error, don't try to parse
			var execErr *ExecError
			if errors.As(err, &execErr) {
				return "", fmt.Errorf("command exited with code %d", execErr.ExitCode)
			}
			return "", fmt.Errorf("exec failed: %w", err)
		}
		L_debug("jq tool: exec completed", "bytes", len(data))
	}

	// Execute jq query
	result, err := executeJQ(params.Query, data, params.Raw, params.Compact)
	if err != nil {
		return "", err
	}

	L_debug("jq tool: query completed", "query", truncateQuery(params.Query, 50), "resultLen", len(result))
	return result, nil
}

// readFile reads a JSON file, respecting sandbox settings
func (t *JQTool) readFile(path string, sandboxed bool) ([]byte, error) {
	if sandboxed {
		// Use sandbox.ReadFile which validates path is within workspace
		return sandbox.ReadFile(path, t.workingDir, t.workingDir)
	}

	// Unsandboxed: expand ~ and read directly
	expandedPath := expandPath(path)
	return os.ReadFile(expandedPath)
}

// execCommand runs a shell command and returns stdout
func (t *JQTool) execCommand(ctx context.Context, command string, sandboxed bool) ([]byte, error) {
	// Use the shared ExecRunner which handles sandboxing
	// The runner's sandbox is enabled/disabled based on config,
	// but we also pass useSandbox based on user's sandbox setting
	useSandbox := sandboxed && t.runner.Config().Bubblewrap.Enabled
	return t.runner.Run(ctx, command, useSandbox)
}

// executeJQ parses and executes a jq query on JSON data
func executeJQ(query string, data []byte, raw bool, compact bool) (string, error) {
	// Parse JSON input
	var input interface{}
	if err := json.Unmarshal(data, &input); err != nil {
		return "", fmt.Errorf("invalid JSON: %w", err)
	}

	// Parse jq query
	parsed, err := gojq.Parse(query)
	if err != nil {
		return "", fmt.Errorf("invalid jq query: %w", err)
	}

	// Execute query
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

	// Format output
	return formatJQOutput(results, raw, compact)
}

// formatJQOutput formats jq results for output
func formatJQOutput(results []interface{}, raw bool, compact bool) (string, error) {
	var lines []string
	for _, r := range results {
		if raw {
			// Raw string output (like jq -r)
			if s, ok := r.(string); ok {
				lines = append(lines, s)
			} else if r == nil {
				lines = append(lines, "null")
			} else {
				// Non-string values still get JSON encoded
				b, err := json.Marshal(r)
				if err != nil {
					return "", fmt.Errorf("failed to encode result: %w", err)
				}
				lines = append(lines, string(b))
			}
		} else {
			// JSON output
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
