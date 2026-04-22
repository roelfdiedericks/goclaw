---
title: "Document Extract"
description: "Turn uploaded PDFs, Office docs, EPUBs, HTML, and plain text into LLM-ready markdown"
section: "Tools"
weight: 65
---

# Document Extract Tool

`document_extract` converts documents you upload to GoClaw into clean markdown
that your LLM can actually read. It handles formats that most LLMs can't open
natively: PDFs (including scanned ones), Microsoft Office files, EPUB / MOBI
e-books, HTML, and plain text.

Under the hood it uses the [go-markitdown](https://github.com/roelfdiedericks/go-markitdown)
library; embedded images and OCR route through your configured **agent** model
chain.

## When the agent uses it

Whenever you upload a supported document to GoClaw (over Telegram, Discord,
the TUI, HTTP, etc.), the gateway annotates the attachment with a hint telling
the agent it can call `document_extract` to actually read the contents. The
agent decides whether to call it — you don't have to ask.

You can also explicitly ask things like:

> "Read this PDF and summarise it."

and the agent will call `document_extract` on whatever file you attached.

## Supported formats

| Format | Notes |
|--------|-------|
| PDF | Text extraction; OCR optional (via vision chain) |
| DOCX | Microsoft Word |
| XLSX | Microsoft Excel (first sheet) |
| PPTX | Microsoft PowerPoint |
| EPUB / MOBI | E-books |
| HTML / XHTML | Web pages |
| Markdown / Plain text | Pass-through |

Images are not supported here — use the regular image tools (`read` with an
image path) or send the image directly; vision models handle those natively.

## Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| `path` | — | Path to the document. Absolute, relative to the working directory, or a media-store path like `./media/uploads/...` |
| `ocr` | `false` | Run OCR on pages with no extractable text via the agent vision chain. Slow, costs API credits |
| `include_images` | `false` | Describe embedded images inline via the agent vision chain. Costs API credits per image |
| `metadata` | `false` | Prepend YAML front-matter with title / author / page count where available |
| `max_vision_calls` | `50` | Cap on combined OCR + image description calls per extraction (`0` = unlimited). Remaining images become references with a `truncated_images` count in the result |
| `force_refresh` | `false` | Bypass the cache and re-extract |

## Response

A successful call returns structured JSON:

```json
{
  "ok": true,
  "format": "pdf",
  "output_path": "./media/extracted/abcdef...-5f9c.md",
  "bytes": 148210,
  "lines": 1842,
  "title": "Q3 Report",
  "toc": ["Introduction", "Revenue", "Outlook"],
  "preview": "# Q3 Report\n\nRevenue increased...",
  "image_count": 7,
  "truncated_images": 0,
  "warnings": [],
  "cached": false
}
```

- `preview` is a fixed-size head of the markdown (~1500 characters). For
  more, the agent uses the `read` tool against `output_path`:

  ```json
  {"path": "./media/extracted/abcdef...-5f9c.md", "start_line": 120, "end_line": 200}
  ```

- `cached: true` means this extraction was served from disk with no
  re-processing. Cache keys include a hash of the document contents **and** a
  hash of the flags (`ocr`, `include_images`, `metadata`), so two calls with
  different flags don't collide.

## Errors

| Code | Meaning |
|------|---------|
| `unsupported_format` | File type isn't supported by the library |
| `no_text` | PDF/scan has no extractable text — retry with `ocr: true` |
| `password_protected` | Document requires a password |
| `corrupt` | File is malformed |
| `read_failed` | Could not read the file from disk |
| `invalid_path` | Path could not be resolved |
| `invalid_input` | Missing/invalid `path` |
| `extraction_failed` | Other, non-typed extraction error |

Errors include a short `hint` where a retry strategy is obvious (for example,
`no_text` suggests enabling OCR).

## Storage & retention

Extracted markdown is cached under the `extracted` media category at
`media/extracted/<contentHash>-<flagsHash>.md` (plus a small `.json` sidecar).
Defaults:

- **TTL:** 30 days
- **Quota:** 2 GB

Adjust both in the setup wizard or TUI config editor under **Media Storage →
Extracted Documents**. The category is ephemeral — cleanup is automatic, and
anything you delete will simply be regenerated on the next call.

## Vision chain notes

- OCR and image descriptions use the **agent** model chain. If that chain has
  no vision-capable model, the tool degrades gracefully: images become
  references, OCR reports `no_text`, and a warning is surfaced in the
  response.
- Every vision call goes through normal failover + cooldown, so a single bad
  provider won't break an extraction.
- The `max_vision_calls` cap protects against pathological documents (e.g. a
  200-image PDF) running up an unbounded bill. Remaining images are reported
  in `truncated_images`; raise the cap or set it to `0` to disable.

## Disable the tool

Under **Tools → Document Extract** in the setup wizard or TUI editor, toggle
`documentExtract.enabled` off. The config key is
`tools.documentExtract.enabled` (default `true`).
