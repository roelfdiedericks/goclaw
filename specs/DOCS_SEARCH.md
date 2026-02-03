# Document Search Specification

## Overview

A `docs_search` tool for searching external knowledge bases, documentation, and shared information. Separate from personal memory, accessible to all user roles.

## Motivation

The `memory_search` tool searches the owner's personal memory files (MEMORY.md, memory/*.md). This is private, owner-only data.

For multi-user scenarios (e.g., support agent), we need a separate search over:
- Company documentation
- FAQs and knowledge base articles
- Product information
- Policies and procedures
- Public/shared knowledge

This should be accessible to all authenticated users, not just the owner.

## Data Separation

| Tool | Data Source | Owner | User | Guest |
|------|-------------|-------|------|-------|
| `memory_search` | MEMORY.md, memory/*.md | ✅ | ❌ | ❌ |
| `transcript_search` | sessions.db (own session) | ✅ | ✅ | ❌ |
| `docs_search` | docs/, knowledge base | ✅ | ✅ | ✅ |

**Key distinction:**
- Memory = Personal, curated, owner-only
- Transcripts = Per-user conversation history
- Docs = Shared knowledge, accessible to all

## Use Case: Support Agent

```
Customer: "How do I reset my router?"

Agent uses: docs_search("reset router")
→ Finds: docs/troubleshooting/router-reset.md
→ Returns relevant steps

Agent: "To reset your router, hold the reset button for 10 seconds..."
```

The agent doesn't need personal memories to help customers. It needs access to:
- Product documentation
- Troubleshooting guides
- FAQs
- Policy documents

## Tool Definition

```json
{
  "name": "docs_search",
  "description": "Search documentation and knowledge base. Use for product info, FAQs, troubleshooting, policies.",
  "input_schema": {
    "type": "object",
    "properties": {
      "query": {
        "type": "string",
        "description": "Search query (semantic search)"
      },
      "category": {
        "type": "string",
        "description": "Optional: limit to category (e.g., 'troubleshooting', 'billing', 'products')"
      },
      "maxResults": {
        "type": "integer",
        "default": 5,
        "description": "Maximum results to return"
      }
    },
    "required": ["query"]
  }
}
```

## Directory Structure

```
~/.openclaw/
├── workspace/
│   ├── MEMORY.md          # Owner's personal memory (private)
│   ├── memory/            # Owner's daily notes (private)
│   └── ...
└── docs/                  # Shared knowledge base (public)
    ├── products/
    │   ├── fiber.md
    │   └── wifi.md
    ├── troubleshooting/
    │   ├── router-reset.md
    │   └── slow-speeds.md
    ├── billing/
    │   ├── payment-methods.md
    │   └── pricing.md
    └── policies/
        ├── fair-use.md
        └── support-hours.md
```

## Implementation

### Embedding Index

Separate index from memory embeddings:

```go
type DocsIndex struct {
    embeddings *EmbeddingStore  // docs-specific index
    docsRoot   string           // ~/.openclaw/docs/
}

func (d *DocsIndex) Search(query string, maxResults int) ([]SearchResult, error) {
    // Semantic search over docs
}
```

### Indexing

Index docs on:
- Startup (scan docs/ directory)
- File change detection (fsnotify watcher)
- Manual refresh command

```go
func (d *DocsIndex) Reindex() error {
    files := walkDir(d.docsRoot, ".md")
    for _, f := range files {
        content := readFile(f)
        embedding := embed(content)
        d.embeddings.Store(f, embedding, metadata{
            category: extractCategory(f),  // from path
            title: extractTitle(content),
        })
    }
}
```

### No Permission Check Needed

Unlike `memory_search`, `docs_search` is available to all authenticated users:

```go
func (t *DocsSearchTool) Execute(ctx context.Context, input Input) (string, error) {
    // No role check - anyone can search docs
    results, err := t.index.Search(input.Query, input.MaxResults)
    // ...
}
```

## Agent Instructions

```markdown
## Knowledge Sources

- `memory_search` — Your personal memories (owner sessions only)
- `transcript_search` — Conversation history with current user
- `docs_search` — Documentation and knowledge base (all users)

For support/help questions, prefer `docs_search` to find official documentation.
For personal context ("what did we decide"), use `memory_search` (owner only).
For "when did we discuss X", use `transcript_search`.
```

## Configuration

```json
{
  "docs": {
    "enabled": true,
    "path": "~/.openclaw/docs",
    "categories": ["products", "troubleshooting", "billing", "policies"],
    "embedModel": "nomic-embed-text"
  }
}
```

## Future: External Knowledge Bases

Could extend to pull from external sources:
- Notion databases
- Confluence
- Google Drive
- Custom APIs

```json
{
  "docs": {
    "sources": [
      {"type": "local", "path": "~/.openclaw/docs"},
      {"type": "notion", "database_id": "abc123"},
      {"type": "url", "url": "https://docs.cooleas.com/api/search"}
    ]
  }
}
```

## Summary

| Aspect | memory_search | docs_search |
|--------|---------------|-------------|
| Data | Personal memories | Shared documentation |
| Access | Owner only | All users |
| Use case | "What do I know about X" | "How do I do X" |
| Index | memory/*.md | docs/**/*.md |

Keeps personal and shared knowledge cleanly separated while using the same embedding infrastructure.
