# JQ Tool Spec

## Overview

A built-in jq tool for JSON querying and manipulation, powered by `github.com/itchyny/gojq` (pure Go, highly compatible with jq).

## Motivation

- JSON is everywhere: configs, API responses, cron jobs, Home Assistant, etc.
- Currently using `exec` + shell jq = clunky, escaping issues, subprocess overhead
- Pure Go library = single binary, no external dependency
- Full jq syntax = agents (and humans) already know it

## Tool Definition

```json
{
  "name": "jq",
  "description": "Query and transform JSON using jq syntax. Can read from file, inline JSON, or command output.",
  "parameters": {
    "query": {
      "type": "string",
      "description": "jq query/filter expression (e.g., '.items[] | .name')",
      "required": true
    },
    "file": {
      "type": "string",
      "description": "Path to JSON file to query. Mutually exclusive with 'input' and 'exec'."
    },
    "input": {
      "type": "string",
      "description": "Inline JSON string to query. Mutually exclusive with 'file' and 'exec'."
    },
    "exec": {
      "type": "string",
      "description": "Shell command whose stdout is piped through jq. Mutually exclusive with 'file' and 'input'. Respects sandbox settings."
    },
    "raw": {
      "type": "boolean",
      "description": "Output raw strings without JSON encoding (like jq -r). Default: false"
    },
    "compact": {
      "type": "boolean",
      "description": "Compact output (no pretty-printing). Default: false"
    }
  }
}
```

## Usage Examples

### Query a file
```
jq(query=".jobs[] | .name", file="~/.openclaw/cron/jobs.json")
// Output: ["Twitter Scrape", "Morning Brief", ...]
```

### Pipe from command (exec)
```
jq(query=".results[0].id", exec="curl -s https://api.example.com/data")
// Output: "user-12345"
```

The JSON response never hits agent context — just the extracted result.

### API with auth header
```
jq(query=".items | length", exec="curl -s -H 'Authorization: Bearer $TOKEN' https://api.example.com/items")
// Output: 42
```

### Chain commands
```
jq(query=".name", exec="cat /workspace/config.json")
// Equivalent to: file="/workspace/config.json" but via exec
```

### Filter with conditions
```
jq(query=".jobs[] | select(.enabled == true) | {name, id}", file="cron/jobs.json")
```

### Inline JSON
```
jq(query=".users | keys", input="{\"users\": {\"alice\": 1, \"bob\": 2}}")
// Output: ["alice", "bob"]
```

### Raw string output
```
jq(query=".name", input="{\"name\": \"test\"}", raw=true)
// Output: test (not "test")
```

### Complex transformations
```
jq(query="[.items[] | {id: .id, label: .name}]", file="data.json")
```

### Config inspection
```
jq(query=".llm.embeddings", file="~/.openclaw/goclaw/goclaw.json")
```

## Implementation

### Dependencies
```go
import "github.com/itchyny/gojq"
```

### Core Logic
```go
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
            return "", err
        }
        results = append(results, v)
    }

    // Format output
    return formatOutput(results, raw, compact)
}

func formatOutput(results []interface{}, raw bool, compact bool) (string, error) {
    var lines []string
    for _, r := range results {
        if raw {
            // Raw string output (like jq -r)
            if s, ok := r.(string); ok {
                lines = append(lines, s)
            } else {
                b, _ := json.Marshal(r)
                lines = append(lines, string(b))
            }
        } else {
            // JSON output
            var b []byte
            if compact {
                b, _ = json.Marshal(r)
            } else {
                b, _ = json.MarshalIndent(r, "", "  ")
            }
            lines = append(lines, string(b))
        }
    }
    return strings.Join(lines, "\n"), nil
}
```

### File Reading & Exec
```go
func (t *JQTool) Execute(params JQParams) (string, error) {
    var data []byte
    var err error

    // Count how many input sources specified
    sources := 0
    if params.File != "" { sources++ }
    if params.Input != "" { sources++ }
    if params.Exec != "" { sources++ }

    if sources > 1 {
        return "", errors.New("cannot specify multiple input sources (file, input, exec)")
    }
    if sources == 0 {
        return "", errors.New("must specify one of: 'file', 'input', or 'exec'")
    }

    if params.File != "" {
        // Expand ~ and read file
        path := expandPath(params.File)
        data, err = os.ReadFile(path)
        if err != nil {
            return "", fmt.Errorf("failed to read file: %w", err)
        }
    } else if params.Input != "" {
        data = []byte(params.Input)
    } else if params.Exec != "" {
        // Run command and capture stdout
        data, err = t.execCommand(params.Exec)
        if err != nil {
            return "", fmt.Errorf("exec failed: %w", err)
        }
    }

    return executeJQ(params.Query, data, params.Raw, params.Compact)
}

func (t *JQTool) execCommand(command string) ([]byte, error) {
    // Uses same sandbox logic as exec tool
    // If sandbox enabled, runs through bwrap
    // Respects timeout settings
    ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
    defer cancel()

    var cmd *exec.Cmd
    if t.sandboxEnabled {
        cmd = t.buildSandboxedCommand(ctx, command)
    } else {
        cmd = exec.CommandContext(ctx, "sh", "-c", command)
    }

    return cmd.Output()
}
```

## Security Considerations

### Path Restrictions
- File access should respect sandbox settings
- Sandbox mode: restrict to workspace root
- Owner mode (sandbox = false in users.json): full filesystem access (like `read` tool)

### Exec Sandbox
- The `exec` parameter runs through the same sandbox as the `exec` tool
- If bwrap sandbox enabled, command runs in sandboxed environment
- Inherits timeout settings from exec tool config

### No Side Effects
- jq tool is **read-only** — cannot modify files
- For modifications, use `read` → `jq` → `write` pattern
- `exec` commands should be read-only (curl, cat, etc.) — writes would be sandboxed anyway

## Error Handling

| Error | Message |
|-------|---------|
| Invalid JSON | "invalid JSON: {parse error}" |
| Invalid query | "invalid jq query: {syntax error}" |
| File not found | "failed to read file: {path}" |
| Multiple sources | "cannot specify multiple input sources (file, input, exec)" |
| No source | "must specify one of: 'file', 'input', or 'exec'" |
| Exec failed | "exec failed: {error}" |
| Exec timeout | "exec failed: context deadline exceeded" |
| Runtime error | jq execution errors passed through |

## Future Enhancements

### Phase 2: Mutations (maybe)
```
jq(query=".enabled = true", file="config.json", write=true)
```
This would modify the file in place. Requires careful consideration for safety.

### Phase 2: Multiple files
```
jq(query=".name", files=["a.json", "b.json"])
```
Process multiple files, output combined results.

### Phase 2: JSONL/streaming
```
jq(query=".event", file="events.jsonl", slurp=false)
```
Process line-delimited JSON streams.

## Testing

```go
func TestJQTool(t *testing.T) {
    tests := []struct {
        name     string
        query    string
        input    string
        expected string
    }{
        {"simple key", ".name", `{"name":"test"}`, `"test"`},
        {"array index", ".[0]", `[1,2,3]`, `1`},
        {"filter", ".[] | select(. > 2)", `[1,2,3,4]`, "3\n4"},
        {"object construction", "{a: .x}", `{"x":1}`, `{"a":1}`},
        {"raw output", ".name", `{"name":"test"}`, `test`}, // with raw=true
    }
    // ...
}
```

## Notes

- gojq is highly compatible but not 100% identical to jq (edge cases in date functions, etc.)
- For most use cases (config parsing, API responses), it's indistinguishable
- Pure Go = compiles into GoClaw binary, no runtime dependency
