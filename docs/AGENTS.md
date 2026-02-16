---
description: Guidelines for writing GoClaw documentation
alwaysApply: true
---

# AGENTS.md — GoClaw Documentation

Guidelines for writing and maintaining GoClaw documentation.

This file is excluded from the website build — it's for contributors and AI assistants working on docs.
DO NOT EMBELLISH ENVIRONMENT VARIABLES OR THINGS THAT DO NOT EXIST.
We are NOT documenting the code either, this is user-facing documentation.
---

## File Conventions

### Naming
- Lowercase, hyphenated: `session-management.md`, not `SessionManagement.md`
- Match the concept, not the code

### Directory Structure

```
docs/
├── providers/           # LLM provider docs
│   ├── anthropic.md
│   ├── openai.md
│   ├── ollama.md
│   └── xai.md
├── tools/               # Individual tool docs
│   ├── internal.md      # read, write, edit, exec
│   ├── browser.md
│   ├── hass.md
│   ├── cron.md
│   ├── web.md
│   ├── jq.md
│   ├── message.md
│   ├── user-auth.md
│   └── xai-imagine.md
├── *.md                 # Top-level and landing pages
└── sidebar.yaml         # Navigation definition
```

### Landing Pages

Each major section should have a landing page:
- `concepts.md` — Core concepts overview
- `llm-providers.md` — LLM provider overview
- `channels.md` — Channel overview
- `agent-memory.md` — Memory system overview
- `advanced.md` — Advanced topics overview
- `tools.md` — Tool index

### No YAML Frontmatter
Don't add YAML frontmatter to doc files. Navigation and metadata are handled by `sidebar.yaml`.

```markdown
# This is correct
Just start with the H1 title.

---
title: "Don't do this"
---
# This adds unnecessary complexity
```

---

## Document Structure

Each doc should follow this general structure:

1. **H1 Title** — Matches the sidebar title
2. **Overview** — 1-2 paragraphs explaining what this is and why it matters
3. **Configuration** — JSON examples with options tables
4. **Usage/Examples** — Practical examples
5. **Troubleshooting** — Common issues (where applicable)
6. **Next Steps** — Links to related docs

### Example Skeleton

```markdown
# Feature Name

Brief overview of what this feature does and why you'd use it.

## Configuration

\`\`\`json
{
  "feature": {
    "enabled": true,
    "option": "value"
  }
}
\`\`\`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable this feature |
| `option` | string | `"default"` | Some option |

## Usage

How to use it, with examples.

## Troubleshooting

### Common Issue

How to fix it.

## Next Steps

- [Related Doc](related-doc.md) — Description
```

---

## Cross-References

### Link Format

Use relative `.md` links. They work on both GitHub and goclaw.org:

```markdown
See [Configuration](configuration.md) for details.
See [LLM section](configuration.md#llm) for API key setup.
```

The website has a render hook that transforms `.md` links to clean URLs.

### Don't Use

```markdown
<!-- Don't use absolute paths -->
See [Configuration](/docs/configuration/)

<!-- Don't use Hugo shortcodes -->
See {{</* ref "configuration.md" */>}}
```

---

## Code Examples

### JSON Configuration

Show a minimal working example first, then a full options table:

```markdown
\`\`\`json
{
  "telegram": {
    "enabled": true,
    "botToken": "YOUR_BOT_TOKEN"
  }
}
\`\`\`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable Telegram |
| `botToken` | string | - | Token from @BotFather |
```

### Shell Commands

Prefix with comments explaining what they do:

```markdown
\`\`\`bash
# Download the binary
curl -LO https://github.com/roelfdiedericks/goclaw/releases/latest/download/goclaw-linux-amd64

# Make executable and move to PATH
chmod +x goclaw-linux-amd64
sudo mv goclaw-linux-amd64 /usr/local/bin/goclaw
\`\`\`
```

### Go Code

Only include Go code in architecture/internals docs. User-facing docs should focus on configuration and usage, not implementation.

---

## sidebar.yaml

The sidebar is defined in `docs/sidebar.yaml`:

```yaml
sections:
  - name: About
    link: readme           # Clickable section → /docs/readme/
    items:
      - title: Core Concepts
        file: concepts     # Landing page with links to sub-items
      - title: Architecture
        file: architecture

  - name: Getting Started
    link: /docs/           # Clickable header → /docs/
    items:
      - title: Installation
        file: installation

  - name: LLM Providers
    link: llm-providers    # Landing page
    items:
      - title: Anthropic
        file: providers/anthropic

  - name: Tools
    link: tools            # Index page
    items:
      - title: Browser
        file: tools/browser
```

### Adding a New Doc

1. Create the markdown file in `docs/` (or appropriate subdirectory)
2. Add an entry to `sidebar.yaml` under the appropriate section
3. `file:` is the path without `.md` (will be lowercased for URL)
4. For subdirectories: `file: tools/browser` → `/docs/tools/browser/`

### Section Types

| Type | Syntax | Example |
|------|--------|---------|
| Clickable standalone | `link: filename` | About → readme |
| Clickable with items | `link: filename` + `items:` | LLM Providers |
| Header only | `items:` only | (avoid — use landing pages) |

### Subdirectory Linking

For docs in subdirectories:
```yaml
- title: Anthropic Provider
  file: providers/anthropic    # → /docs/providers/anthropic/
```

---

## Style Guide

### Voice & Tone
- Direct and technical — no marketing fluff
- User-friendly — explain concepts, don't assume expertise
- Assume basic command-line familiarity (Linux/macOS)
- "You" for the reader, avoid "we" except for project decisions

### Consistent Examples
- Use `TheRoDent` for usernames in examples
- Use `Ratpup` for agent names
- Use placeholder API keys: `YOUR_API_KEY`, `sk-ant-...`

### Formatting
- Use `code` for filenames, commands, config keys
- Use **bold** for UI elements or emphasis
- Use tables for options/fields, not nested lists

### American English
- "color" not "colour"
- "behavior" not "behaviour"
- Use Oxford comma: "sessions, transcripts, and embeddings"

---

## Security — No Real Data

**Never include real credentials, keys, or personal information in documentation.**

| Use This | Not This |
|----------|----------|
| `YOUR_API_KEY` | Actual API keys |
| `sk-ant-api03-...` | Real Anthropic keys |
| `YOUR_BOT_TOKEN` | Real Telegram tokens |
| `user@example.com` | Real email addresses |
| `TheRoDent` | Real usernames |
| `192.168.1.x` | Real IP addresses |
| `example.com` | Real domains (unless public docs) |

This applies to:
- Configuration examples
- Code snippets
- Screenshots (redact before including)
- Log output examples
- Error messages

If you need realistic-looking examples, use:
- **Username:** `TheRoDent`
- **Agent name:** `Ratpup`
- **API keys:** `YOUR_API_KEY`, `sk-ant-...`, `gsk_...`
- **Tokens:** `YOUR_BOT_TOKEN`, `YOUR_TOKEN`
- **IPs:** `192.168.1.x`, `10.0.0.x`
- **Domains:** `example.com`, `homeassistant.local`

---

## Don'ts

- **Don't add YAML frontmatter** — sidebar.yaml handles metadata
- **Don't use absolute URLs** for internal links — use relative `.md` links
- **Don't include real secrets** — use placeholders (see Security section above)
- **Don't include PII** — no real names, emails, IPs, or identifiable data
- **Don't duplicate content** — link to the canonical source
- **Don't explain code internals** in user-facing docs — focus on config and usage

---

## Website Build Process

Documentation is published to [goclaw.org](https://goclaw.org) via a separate repository.

The build process:
1. Sparse checkout of `docs/` + `README.md` from this repo
2. Hugo generates static HTML with Bootstrap dark theme
3. Deployed to Cloudflare Pages

For website-specific changes (templates, styling, navigation), see the `AGENTS.md` in the goclaw.org repository.

This file (`docs/AGENTS.md`) is excluded from the website build.
