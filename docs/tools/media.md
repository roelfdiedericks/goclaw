---
title: "Media"
description: "Inspect live media storage usage, quotas, retention, and category policies"
section: "Tools"
weight: 80
---

# Media Tool

Inspect GoClaw's live media storage state so the agent can reason about capacity, cleanup risk, and where files should live.

Use `media` when you need to:
- check current store usage and global quota
- inspect one category such as `keeper`, `uploads`, or `browser`
- understand whether a directory is permanent or subject to cleanup
- see warning conditions before generating, downloading, or saving large files

Do not use this tool to show files to the user. Use `media_display` for that.

## Actions

### info

Return live media-store configuration, usage, and warnings.

```json
{
  "action": "info"
}
```

Optional category focus:

```json
{
  "action": "info",
  "category": "browser"
}
```

Optional warnings toggle:

```json
{
  "action": "info",
  "includeWarnings": false
}
```

| Parameter | Required | Description |
|-----------|----------|-------------|
| `action` | Yes | `info` |
| `category` | No | One of `uploads`, `keeper`, `browser`, `camera`, `generated`, `downloads`, `voice` |
| `includeWarnings` | No | Include warning details in the response. Defaults to `true` |

## Output

The tool returns:
- a short natural-language `summary`
- normalized store metadata such as base directory, max file size, cleanup settings, and global quota
- per-category usage, file counts, permanence, TTL, and over-quota state
- warning lines when storage pressure exists

## Category Guidance

- `uploads` and `keeper` are preserved and never auto-deleted
- `browser`, `camera`, `generated`, `downloads`, and `voice` are temporary categories that may be cleaned based on TTL and quota policy
- nested paths inherit the policy of their top-level category

## See Also

- [Message](./message.md) — Send files or mixed content to channels
- [Internal Tools](./internal.md) — Core agent tools
- [Configuration](../configuration.md) — Media retention and storage settings
