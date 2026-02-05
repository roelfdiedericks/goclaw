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
  "description": "Query and transform JSON using jq syntax. Can read from file or inline JSON.",
  "parameters": {
    "query": {
      "type": "string",
      "description": "jq query/filter expression (e.g., '.items[] | .name')",
      "required": true
    },
    "file": {
      "type": "string",
      "description": "Path to JSON file to query. Mutually exclusive with 'input'."
    },
    "input": {
      "type": "string",
      "description": "Inline JSON string to query. Mutually exclusive with 'file'."
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

### File Reading
```go
func (t *JQTool) Execute(params JQParams) (string, error) {
    var data []byte
    var err error

    if params.File != "" && params.Input != "" {
        return "", errors.New("cannot specify both 'file' and 'input'")
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
    } else {
        return "", errors.New("must specify either 'file' or 'input'")
    }

    return executeJQ(params.Query, data, params.Raw, params.Compact)
}
```

## Security Considerations

### Path Restrictions
- File access should respect sandbox settings
- Sandbox mode: restrict to workspace root
- Owner mode: full filesystem access (like `read` tool)

### No Side Effects
- jq tool is **read-only** — cannot modify files
- For modifications, use `read` → `jq` → `write` pattern

## Error Handling

| Error | Message |
|-------|---------|
| Invalid JSON | "invalid JSON: {parse error}" |
| Invalid query | "invalid jq query: {syntax error}" |
| File not found | "failed to read file: {path}" |
| Both file and input | "cannot specify both 'file' and 'input'" |
| Neither file nor input | "must specify either 'file' or 'input'" |
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
