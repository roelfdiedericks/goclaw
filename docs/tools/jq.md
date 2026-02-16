# JQ Tool

Query and transform JSON using jq syntax.

## Usage

The jq tool can read from files, inline JSON, or command output.

### From File

```json
{
  "file": "data.json",
  "query": ".items[] | select(.active == true)"
}
```

### From Inline JSON

```json
{
  "json": {"users": [{"name": "Alice"}, {"name": "Bob"}]},
  "query": ".users[].name"
}
```

### From Command

```json
{
  "command": "curl -s https://api.example.com/data",
  "query": ".results[:5]"
}
```

## Parameters

| Parameter | Description |
|-----------|-------------|
| `query` | jq filter expression (required) |
| `file` | JSON file path |
| `json` | Inline JSON object |
| `command` | Command that outputs JSON |

One of `file`, `json`, or `command` must be provided.

## jq Syntax

Common jq operations:

| Expression | Description |
|------------|-------------|
| `.` | Identity (whole input) |
| `.field` | Access field |
| `.[]` | Iterate array |
| `.[0]` | First element |
| `.[:5]` | First 5 elements |
| `select(.x == 1)` | Filter |
| `map(.x)` | Transform array |
| `keys` | Object keys |
| `length` | Length |
| `sort_by(.x)` | Sort by field |

## Examples

**Extract names from array:**
```json
{
  "json": [{"name": "Alice"}, {"name": "Bob"}],
  "query": ".[].name"
}
```

**Filter active items:**
```json
{
  "file": "items.json",
  "query": ".[] | select(.status == \"active\")"
}
```

**Get API response field:**
```json
{
  "command": "curl -s https://api.github.com/repos/user/repo",
  "query": "{name: .name, stars: .stargazers_count}"
}
```

---

## See Also

- [Tools](../tools.md) — Tool overview
- [jq Manual](https://stedolan.github.io/jq/manual/) — Full jq documentation
